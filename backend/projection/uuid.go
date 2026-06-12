package projection

import "github.com/google/uuid"

func newUUID() string {
	return uuid.NewString()
}

// NewUploadUUID returns a fresh random identifier used to group the parts and
// manifest of one multipart upload.
func NewUploadUUID() string {
	return newUUID()
}
