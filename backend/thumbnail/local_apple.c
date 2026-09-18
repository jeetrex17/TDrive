//go:build (darwin || ios) && cgo

#include <CoreFoundation/CoreFoundation.h>
#include <CoreGraphics/CoreGraphics.h>
#include <ImageIO/ImageIO.h>
#include <stdlib.h>
#include <string.h>

#include "apple_image.h"

// The one JPEG encoder on this platform, shared with the video poster bridge.
// A rendition larger than this was not produced by anything we asked for.
#define TDRIVE_MAX_ENCODED (8 * 1024 * 1024)

int tdrive_encode_jpeg(CGImageRef image, void **bytes, size_t *length) {
    *bytes = NULL;
    *length = 0;
    if (!image) return 1;
    CFMutableDataRef encoded = CFDataCreateMutable(NULL, 0);
    if (!encoded) return 1;
    CGImageDestinationRef destination = CGImageDestinationCreateWithData(encoded, CFSTR("public.jpeg"), 1, NULL);
    double quality = .82;
    CFNumberRef qualityNumber = CFNumberCreate(NULL, kCFNumberDoubleType, &quality);
    const void *outputKeys[] = {kCGImageDestinationLossyCompressionQuality};
    const void *outputValues[] = {qualityNumber};
    CFDictionaryRef outputOptions = CFDictionaryCreate(NULL, outputKeys, outputValues, 1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    bool ok = false;
    if (destination) {
        CGImageDestinationAddImage(destination, image, outputOptions);
        ok = CGImageDestinationFinalize(destination);
        CFRelease(destination);
    }
    CFRelease(outputOptions); CFRelease(qualityNumber);
    CFIndex count = CFDataGetLength(encoded);
    if (ok && count > 0 && count <= TDRIVE_MAX_ENCODED) {
        *bytes = malloc(count);
        if (*bytes) { memcpy(*bytes, CFDataGetBytePtr(encoded), count); *length = count; }
    }
    CFRelease(encoded);
    return *bytes ? 0 : 1;
}

// ImageIO's thumbnail API downsamples during decoding. Drawing a full UIImage
// into a smaller canvas would allocate the very bitmap this path avoids.
static int sample_source(CGImageSourceRef source, int edge, void **bytes, size_t *length) {
    *bytes = NULL;
    *length = 0;
    if (!source) return 1;
    CFDictionaryRef properties = CGImageSourceCopyPropertiesAtIndex(source, 0, NULL);
    int64_t width = 0, height = 0;
    if (properties) {
        CFNumberRef w = CFDictionaryGetValue(properties, kCGImagePropertyPixelWidth);
        CFNumberRef h = CFDictionaryGetValue(properties, kCGImagePropertyPixelHeight);
        if (w && CFGetTypeID(w) == CFNumberGetTypeID()) CFNumberGetValue(w, kCFNumberSInt64Type, &width);
        if (h && CFGetTypeID(h) == CFNumberGetTypeID()) CFNumberGetValue(h, kCFNumberSInt64Type, &height);
        CFRelease(properties);
    }
    if (width <= 0 || height <= 0) { CFRelease(source); return 1; }
    if (width > 200000000 / height) { CFRelease(source); return 2; }
    // Large photographic codecs support decode-time subsampling. Other formats
    // may still expand the full raster internally, so admit only a small source.
    CFStringRef type = CGImageSourceGetType(source);
    bool sampled = type && (CFEqual(type, CFSTR("public.jpeg")) || CFEqual(type, CFSTR("public.heic")) || CFEqual(type, CFSTR("public.heif")));
    if (!sampled && width > 12000000 / height) { CFRelease(source); return 2; }
    CFNumberRef size = CFNumberCreate(NULL, kCFNumberIntType, &edge);
    const void *keys[] = { kCGImageSourceCreateThumbnailFromImageAlways, kCGImageSourceThumbnailMaxPixelSize, kCGImageSourceCreateThumbnailWithTransform, kCGImageSourceShouldCacheImmediately };
    const void *values[] = { kCFBooleanTrue, size, kCFBooleanTrue, kCFBooleanTrue };
    CFDictionaryRef thumbOptions = CFDictionaryCreate(NULL, keys, values, 4, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CGImageRef image = CGImageSourceCreateThumbnailAtIndex(source, 0, thumbOptions);
    CFRelease(thumbOptions); CFRelease(size); CFRelease(source);
    if (!image) return 1;
    CGColorSpaceRef color = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
    size_t outWidth = CGImageGetWidth(image), outHeight = CGImageGetHeight(image);
    if (!outWidth || !outHeight || outWidth > edge || outHeight > edge) { CGColorSpaceRelease(color); CGImageRelease(image); return 2; }
    CGContextRef canvas = CGBitmapContextCreate(NULL, outWidth, outHeight, 8, outWidth * 4, color, kCGImageAlphaPremultipliedLast);
    CGColorSpaceRelease(color);
    if (!canvas) { CGImageRelease(image); return 1; }
    CGContextSetRGBFillColor(canvas, 1, 1, 1, 1);
    CGContextFillRect(canvas, CGRectMake(0, 0, outWidth, outHeight));
    CGContextDrawImage(canvas, CGRectMake(0, 0, outWidth, outHeight), image);
    CGImageRelease(image);
    CGImageRef flattened = CGBitmapContextCreateImage(canvas);
    CGContextRelease(canvas);
    if (!flattened) return 1;
    int encoded = tdrive_encode_jpeg(flattened, bytes, length);
    CGImageRelease(flattened);
    return encoded;
}

static CFDictionaryRef source_options(void) {
    const void *keys[] = { kCGImageSourceShouldCache };
    const void *values[] = { kCFBooleanFalse };
    return CFDictionaryCreate(NULL, keys, values, 1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
}

int tdrive_image_thumbnail(const char *path, int edge, void **bytes, size_t *length) {
    CFURLRef url = CFURLCreateFromFileSystemRepresentation(NULL, (const UInt8 *)path, strlen(path), false);
    if (!url) return 1;
    CFDictionaryRef options = source_options();
    CGImageSourceRef source = CGImageSourceCreateWithURL(url, options);
    CFRelease(url); CFRelease(options);
    return sample_source(source, edge, bytes, length);
}

int tdrive_image_thumbnail_data(const void *input, size_t count, int edge, void **bytes, size_t *length) {
    // Borrow the Go buffer only during this synchronous call. No decrypted
    // bytes or decoder handles escape into persistent native state.
    CFDataRef data = CFDataCreateWithBytesNoCopy(NULL, input, count, kCFAllocatorNull);
    if (!data) return 1;
    CFDictionaryRef options = source_options();
    CGImageSourceRef source = CGImageSourceCreateWithData(data, options);
    CFRelease(options);
    int result = sample_source(source, edge, bytes, length);
    CFRelease(data);
    return result;
}
