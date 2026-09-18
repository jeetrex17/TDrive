// Shared image plumbing for the Apple bridges.
//
// Both renditions the app produces natively -- a sampled photo and a video
// poster -- end the same way: one CGImage becomes JPEG bytes the Go side owns.
// That step lives once, in local_apple.c, and the poster bridge calls it.

#ifndef TDRIVE_APPLE_IMAGE_H
#define TDRIVE_APPLE_IMAGE_H

#include <CoreGraphics/CoreGraphics.h>
#include <stdlib.h>

// tdrive_encode_jpeg writes image as a JPEG into a malloc'd buffer the caller
// frees, returning 0 on success and 1 on any refusal. The image is not
// released; ownership stays with the caller.
int tdrive_encode_jpeg(CGImageRef image, void **bytes, size_t *length);

#endif // TDRIVE_APPLE_IMAGE_H
