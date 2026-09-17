//go:build (darwin || ios || android) && cgo

package projection

import "runtime"

// Increment generator_version in gallery_preparation.go when decoder behavior
// changes. Native and portable builds never share permanent failure hints.
const galleryPreparationDecoderProfile = runtime.GOOS + ":native"
