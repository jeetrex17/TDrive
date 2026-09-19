package file

import (
	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
	"bytes"
	"image"
	"image/jpeg"
	"testing"
)

func tinyRenditionJPEG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 3)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestRenditionEncryptionBindsSourceAndUsesIndependentRandomness(t *testing.T) {
	raw := tinyRenditionJPEG(t)
	key := bytes.Repeat([]byte{7}, 32)
	ref := projection.FileRendition{ChannelID: 1, FileMsgID: 2, ContentMsgID: 2, Kind: "thumbnail", Version: 1, PlaintextSize: int64(len(raw)), Width: 4, Height: 3, Encrypted: true}
	a, err := encryptRendition(raw, key, ref)
	if err != nil {
		t.Fatal(err)
	}
	b, err := encryptRendition(raw, key, ref)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) || bytes.Contains(a, raw) {
		t.Fatal("ciphertext reused randomness or exposed JPEG")
	}
	ref.Size = int64(len(a))
	got, err := decryptRendition(a, key, ref)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("round trip: %v", err)
	}
	for _, mutate := range []func(*projection.FileRendition){func(r *projection.FileRendition) { r.ChannelID++ }, func(r *projection.FileRendition) { r.FileMsgID++ }, func(r *projection.FileRendition) { r.ContentMsgID++ }, func(r *projection.FileRendition) { r.Kind = "preview" }, func(r *projection.FileRendition) { r.Width++ }} {
		wrong := ref
		mutate(&wrong)
		if _, err := decryptRendition(a, key, wrong); err == nil {
			t.Fatalf("accepted swapped binding %+v", wrong)
		}
	}
	a[len(a)-1] ^= 1
	if _, err := decryptRendition(a, key, ref); err == nil {
		t.Fatal("accepted corrupt ciphertext")
	}
}
func TestPlainRenditionValidatesDimensionsAndLength(t *testing.T) {
	raw := tinyRenditionJPEG(t)
	ref := projection.FileRendition{ChannelID: 1, FileMsgID: 2, ContentMsgID: 2, Kind: "thumbnail", Version: 1, Size: int64(len(raw)), PlaintextSize: int64(len(raw)), Width: 4, Height: 3}
	if _, err := decryptRendition(raw, nil, ref); err != nil {
		t.Fatal(err)
	}
	ref.Width = 100
	if _, err := decryptRendition(raw, nil, ref); err == nil {
		t.Fatal("accepted incorrect dimensions")
	}
}

func TestAuthenticatedMalformedRenditionEnvelopeNeverReachesImageDecoder(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	for _, plain := range [][]byte{
		[]byte("WRONG HEADER"),
		append([]byte("TDR1"), []byte{0, 0, 16, 0}...),
		append([]byte("TDR1"), []byte{0, 0, 0, 1, '!'}...),
	} {
		var cipher bytes.Buffer
		if err := tdcrypto.EncryptStream(bytes.NewReader(plain), &cipher, key, int64(len(plain))); err != nil {
			t.Fatal(err)
		}
		ref := projection.FileRendition{ChannelID: 1, FileMsgID: 2, ContentMsgID: 2, Kind: "thumbnail", Version: 1, Size: int64(cipher.Len()), PlaintextSize: 10, Width: 4, Height: 3, Encrypted: true}
		if _, err := decryptRendition(cipher.Bytes(), key, ref); err == nil {
			t.Fatal("authenticated malformed descriptor was accepted")
		}
	}
	raw := tinyRenditionJPEG(t)
	ref := projection.FileRendition{ChannelID: 1, FileMsgID: 2, ContentMsgID: 2, Kind: "thumbnail", Version: 1, Size: int64(len(raw) + 1), PlaintextSize: int64(len(raw)), Width: 4, Height: 3}
	if _, err := decryptRendition(raw, nil, ref); err == nil {
		t.Fatal("stored size mismatch accepted")
	}
	ref.Size = int64(len(raw))
	ref.PlaintextSize++
	if _, err := decryptRendition(raw, nil, ref); err == nil {
		t.Fatal("declared JPEG size mismatch accepted")
	}
	ref.Kind = "original"
	if _, err := decryptRendition(raw, nil, ref); err == nil {
		t.Fatal("unknown rendition class accepted")
	}
}
