//go:build darwin && cgo

package media

import (
	"bytes"
	"image"
	"image/jpeg"
	"net/http"
	"testing"

	"TDrive/backend/projection"
)

// This native integration exercises the reported camera dimensions against
// production limits. A synthetic grayscale JPEG keeps fixture memory at one
// byte per pixel; ImageIO samples directly to the bounded viewing bitmap.
func TestOpenImageSamples50MegapixelJPEG(t *testing.T) {
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, image.NewGray(image.Rect(0, 0, 6144, 8192)), nil); err != nil {
		t.Fatal(err)
	}
	body := encoded.Bytes()
	db := newResolverTestDB(t)
	mustApplyOp(t, db, 10, projection.Op{Type: projection.OpFileUpload, Parent: projection.RootParent, Name: "camera.jpg", FileSize: int64(len(body))})
	ranges := newMediaRangeFake(map[int64][]byte{10: body})
	svc := NewService(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges})
	t.Cleanup(func() { _ = svc.Close() })
	opened, err := svc.OpenImage(t.Context(), testChannelID, 10, 1)
	if err != nil || !opened.ImagePreview {
		t.Fatalf("50MP photo = %+v, %v", opened, err)
	}
	response, err := http.Get(opened.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	preview, format, err := image.Decode(response.Body)
	if err != nil || format != "jpeg" {
		t.Fatalf("preview decode = %s, %v", format, err)
	}
	if preview.Bounds().Dx() != 1200 || preview.Bounds().Dy() != 1600 {
		t.Fatalf("50MP preview dimensions = %v", preview.Bounds())
	}
	if !bytes.Equal(ranges.bodies[10], body) {
		t.Fatal("50MP original was modified")
	}
}
