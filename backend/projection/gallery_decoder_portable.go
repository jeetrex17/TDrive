//go:build (!darwin && !ios && !android) || !cgo

package projection

import "runtime"

const galleryPreparationDecoderProfile = runtime.GOOS + ":go"
