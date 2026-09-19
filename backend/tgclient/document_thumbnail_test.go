package tgclient

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"testing"
)

func TestValidateDocumentThumbnail(t *testing.T) {
	for _, edge := range []int{320, 321} {
		var b bytes.Buffer
		if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, edge, 10)), nil); err != nil {
			t.Fatal(err)
		}
		err := validateDocumentThumbnail(b.Bytes())
		if (edge == 320) != (err == nil) {
			t.Fatalf("edge%d error=%v", edge, err)
		}
	}
	for _, bad := range [][]byte{[]byte("not jpeg"), make([]byte, 200*1024+1)} {
		if err := validateDocumentThumbnail(bad); err == nil {
			t.Fatal("accepted invalid thumbnail")
		}
	}
}

func TestDocumentThumbnailAPIFailsClosedBeforeUpload(t *testing.T) {
	var client *Gotd
	if _, err := client.SendFileWithThumbnail(context.Background(), InputPeer{}, bytes.NewReader(nil), "", "", 0, nil, 1, []byte("not image")); err == nil {
		t.Fatal("invalid JPEG reached upload")
	}
	if err := validateDocumentThumbnail(nil); err != nil {
		t.Fatal("optional nil thumb rejected")
	}
}
