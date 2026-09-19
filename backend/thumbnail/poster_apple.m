//go:build ios && cgo

#import <AVFoundation/AVFoundation.h>
#import <CoreMedia/CoreMedia.h>
#import <Foundation/Foundation.h>

#include <string.h>

#include "apple_image.h"

// AVAsset's synchronous duration/tracks accessors are deprecated in favour of
// load-with-completion-handler variants. This call already runs on a Go worker
// that owns the app-wide decode slot and is bounded by the caller's timeout, so
// the asynchronous forms would buy nothing and cost a semaphore wait that can
// deadlock on a thread with no run loop.
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"

// posterTolerance is how far from the requested time a frame may be taken.
//
// Generous on purpose: a poster is a representative frame, not a specific one,
// and an exact seek makes the generator decode every frame from the preceding
// keyframe forward. Two seconds keeps it to one keyframe on any ordinary GOP.
static const Float64 posterTolerance = 2.0;

// posterTimescale is the fixed timescale the tolerances are expressed in. 600
// is the conventional video timescale: it divides evenly by every common frame
// rate, so the tolerance is exact rather than rounded.
static const int32_t posterTimescale = 600;

// tdrive_video_poster draws one frame with AVFoundation.
//
// The frame is taken seekPercent into the asset, or at fallbackMicros when the
// container declares no usable duration. Returns 0 with a malloc'd JPEG the
// caller frees, and 1 for every "cannot draw this" -- no video track, a codec
// this device lacks, a damaged file -- each of which the Go side reports as
// ErrPosterUnsupported.
int tdrive_video_poster(const char *path, int edge, int seekPercent, long long fallbackMicros, void **bytes, size_t *length) {
    *bytes = NULL;
    *length = 0;
    if (!path || edge < 1) return 1;
    @autoreleasepool {
        NSString *file = [[NSFileManager defaultManager] stringWithFileSystemRepresentation:path length:strlen(path)];
        if (!file) return 1;
        AVURLAsset *asset = [AVURLAsset URLAssetWithURL:[NSURL fileURLWithPath:file isDirectory:NO] options:nil];
        // An audio file someone named .mp4 fails here rather than after a
        // pointless seek, and so does anything that is not a media container.
        if (!asset || [asset tracksWithMediaType:AVMediaTypeVideo].count == 0) return 1;

        CMTime duration = asset.duration;
        CMTime target = CMTimeMakeWithSeconds((Float64)fallbackMicros / 1e6, posterTimescale);
        if (CMTIME_IS_NUMERIC(duration) && CMTimeGetSeconds(duration) > 0) {
            target = CMTimeMultiplyByFloat64(duration, (Float64)seekPercent / 100.0);
        }

        AVAssetImageGenerator *generator = [[AVAssetImageGenerator alloc] initWithAsset:asset];
        // Without this a video shot in portrait comes back on its side: the
        // rotation lives in the track's transform, not in the pixels.
        generator.appliesPreferredTrackTransform = YES;
        // Scale during extraction. A 4K frame never becomes a 4K bitmap on a
        // phone, and the Go side only has to bound what is already small.
        generator.maximumSize = CGSizeMake(edge, edge);
        generator.requestedTimeToleranceBefore = CMTimeMakeWithSeconds(posterTolerance, posterTimescale);
        generator.requestedTimeToleranceAfter = CMTimeMakeWithSeconds(posterTolerance, posterTimescale);

        CGImageRef frame = [generator copyCGImageAtTime:target actualTime:NULL error:NULL];
        // A clip shorter than the offset, or one whose only decodable frame is
        // its first, yields nothing at the seek point but plenty at zero.
        if (!frame && CMTimeGetSeconds(target) > 0) {
            frame = [generator copyCGImageAtTime:kCMTimeZero actualTime:NULL error:NULL];
        }
        [generator release];
        if (!frame) return 1;

        int result = tdrive_encode_jpeg(frame, bytes, length);
        CGImageRelease(frame);
        return result;
    }
}

#pragma clang diagnostic pop
