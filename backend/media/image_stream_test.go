package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"io"
	"net/http"
	"testing"

	"TDrive/backend/projection"
)

func TestImageStreamTypesAreStrictlyAllowlisted(t *testing.T) {
	tests := map[string]struct {
		mime      string
		supported bool
	}{
		"photo.jpg":     {mime: "image/jpeg", supported: true},
		"PHOTO.JPEG":    {mime: "image/jpeg", supported: true},
		"photo.png":     {mime: "image/png", supported: true},
		"photo.gif":     {mime: "image/gif", supported: true},
		"photo.webp":    {mime: "image/webp", supported: true},
		"photo.bmp":     {mime: "image/bmp", supported: true},
		"photo.svg":     {},
		"photo.tiff":    {},
		"photo.jpg.exe": {},
		"photo":         {},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := IsSupportedImageName(name); got != test.supported {
				t.Fatalf("IsSupportedImageName(%q) = %v, want %v", name, got, test.supported)
			}
			if !test.supported {
				if got := streamKindForName(name); got == StreamKindImage {
					t.Fatalf("streamKindForName(%q) = image, want non-image", name)
				}
				return
			}
			if got := streamKindForName(name); got != StreamKindImage {
				t.Fatalf("streamKindForName(%q) = %q, want image", name, got)
			}
			if got := contentTypeFor(name); got != test.mime {
				t.Fatalf("contentTypeFor(%q) = %q, want %q", name, got, test.mime)
			}
		})
	}
}

func TestOpenImageServesEncryptedMultipartRangesAndClosesByToken(t *testing.T) {
	db := newResolverTestDB(t)
	plaintext := jpegFixture(t, 64, 48)
	key := bytes.Repeat([]byte{0x6d}, 32)
	ciphertext := encryptMediaFixture(t, plaintext, key)
	cut := len(ciphertext) / 2
	bodies := map[int64][]byte{
		100: append([]byte(nil), ciphertext[:cut]...),
		101: append([]byte(nil), ciphertext[cut:]...),
	}
	applyMultipart(t, db, "image-parts", []partSpec{
		{msgID: 100, size: int64(len(bodies[100]))},
		{msgID: 101, size: int64(len(bodies[101]))},
	}, 200, projection.Op{
		Type:              projection.OpFileManifest,
		UploadUUID:        "image-parts",
		Parent:            projection.RootParent,
		Name:              "original.jpg",
		FileSize:          int64(len(ciphertext)),
		PartCount:         2,
		Encrypted:         true,
		PlaintextSize:     int64(len(plaintext)),
		EncryptionVersion: 1,
	})

	ranges := newMediaRangeFake(bodies)
	svc := NewService(Config{
		DB:     db,
		Peers:  staticPeerResolver{peer: ranges.peer},
		Ranges: ranges,
		Keys: MasterKeyProviderFunc(func(context.Context, int64) ([]byte, error) {
			return append([]byte(nil), key...), nil
		}),
	})
	defer svc.Close()

	opened, err := svc.OpenImage(context.Background(), testChannelID, 200, 1)
	if err != nil {
		t.Fatalf("OpenImage: %v", err)
	}
	if opened.Kind != StreamKindImage || opened.MimeType != "image/jpeg" || !opened.SupportsRange {
		t.Fatalf("opened image metadata = %+v", opened)
	}
	if opened.ThumbnailURL != "" || opened.HLSURL != "" {
		t.Fatalf("original image unexpectedly exposed derivative URLs: %+v", opened)
	}

	start := int64(len(plaintext) / 3)
	end := start + 31
	req, err := http.NewRequest(http.MethodGet, opened.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Range", "bytes="+itoa(start)+"-"+itoa(end))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET image range: %v", err)
	}
	got, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		t.Fatalf("read image range: %v", readErr)
	}
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(got, plaintext[start:end+1]) {
		t.Fatalf("range response status=%d bytes=%d", resp.StatusCode, len(got))
	}
	assertNoStoreHeaders(t, resp.Header)
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}

	if err := svc.CloseSession(opened.Token); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	response, err := http.Get(opened.URL)
	if err != nil {
		t.Fatalf("GET closed capability: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("closed capability status = %d, want 404", response.StatusCode)
	}
}

func TestOpenImageDisablesBrowserCachingForPlainOriginals(t *testing.T) {
	db := newResolverTestDB(t)
	body := jpegFixture(t, 8, 8)
	mustApplyOp(t, db, 10, projection.Op{
		Type: projection.OpFileUpload, Parent: projection.RootParent,
		Name: "plain.jpg", FileSize: int64(len(body)),
	})
	ranges := newMediaRangeFake(map[int64][]byte{10: body})
	svc := NewService(Config{
		DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges,
	})
	defer svc.Close()

	opened, err := svc.OpenImage(context.Background(), testChannelID, 10, 1)
	if err != nil {
		t.Fatalf("OpenImage: %v", err)
	}
	resp, err := http.Get(opened.URL)
	if err != nil {
		t.Fatalf("GET image: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("image status = %d, want 200", resp.StatusCode)
	}
	assertNoStoreHeaders(t, resp.Header)
}

func TestImageByteAdmissionIsConfigurable(t *testing.T) {
	file := LogicalFile{PlaintextSize: 11}
	err := validateImageMetadata(file, ImageAdmissionLimits{MaxBytes: 10})
	if !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("validateImageMetadata error = %v, want ErrImageTooLarge", err)
	}
}

func TestOpenStreamRejectsGIFDirectDisplayWithTypedSafeError(t *testing.T) {
	db := newResolverTestDB(t)
	var encoded bytes.Buffer
	if err := gif.Encode(&encoded, image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black}), nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	body := encoded.Bytes()
	mustApplyOp(t, db, 10, projection.Op{
		Type: projection.OpFileUpload, Parent: projection.RootParent,
		Name: "animation.gif", FileSize: int64(len(body)),
	})
	ranges := newMediaRangeFake(map[int64][]byte{10: body})
	svc := NewService(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges})
	defer svc.Close()

	_, err := svc.OpenStream(context.Background(), testChannelID, 10)
	if !errors.Is(err, ErrAnimatedImageUnsafe) {
		t.Fatalf("GIF direct display error = %v, want ErrAnimatedImageUnsafe", err)
	}
	if got := len(svc.server.sessions); got != 0 {
		t.Fatalf("GIF direct display published %d sessions", got)
	}
}

func TestAnimatedWebPFeatureFlag(t *testing.T) {
	prefix := make([]byte, imageAnimationProbeBytes)
	copy(prefix[0:4], "RIFF")
	copy(prefix[8:12], "WEBP")
	copy(prefix[12:16], "VP8X")
	if animatedWebP(prefix) {
		t.Fatal("WebP without animation flag reported animated")
	}
	prefix[20] = 0x02
	if !animatedWebP(prefix) {
		t.Fatal("WebP animation feature flag was not rejected")
	}
	if animatedWebP(prefix[:20]) {
		t.Fatal("truncated WebP prefix reported animated")
	}
}

func TestDefaultImageAdmissionLimitsProtectDirectDisplay(t *testing.T) {
	limits := (ImageAdmissionLimits{}).normalized()
	if limits.MaxBytes != 256<<20 || limits.MaxPixels != 32_000_000 || limits.MaxHeaderBytes != 4<<20 {
		t.Fatalf("default image admission = %+v", limits)
	}
}

func TestOpenImageRejectsStaleRevisionBeforeOpeningSession(t *testing.T) {
	db := newResolverTestDB(t)
	body := jpegFixture(t, 8, 8)
	mustApplyOp(t, db, 10, projection.Op{
		Type: projection.OpFileUpload, Parent: projection.RootParent,
		Name: "photo.jpg", FileSize: int64(len(body)),
	})
	ranges := newCountingMediaRangeFake(map[int64][]byte{10: body})
	svc := NewService(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges})
	defer svc.Close()

	_, err := svc.OpenImage(context.Background(), testChannelID, 10, 2)
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("OpenImage stale revision error = %v, want ErrStaleRevision", err)
	}
	if calls := ranges.readCalls.Load(); calls != 0 {
		t.Fatalf("stale image performed %d range reads", calls)
	}
	if got := len(svc.server.sessions); got != 0 {
		t.Fatalf("stale image published %d sessions", got)
	}
}

func TestOpenStreamUsesDetectedImageFormatAndRejectsOversizeDimensions(t *testing.T) {
	t.Run("detected format", func(t *testing.T) {
		db := newResolverTestDB(t)
		body := mediaPNGHeader(8, 8)
		mustApplyOp(t, db, 10, projection.Op{
			Type: projection.OpFileUpload, Parent: projection.RootParent,
			Name: "renamed.jpg", FileSize: int64(len(body)),
		})
		ranges := newMediaRangeFake(map[int64][]byte{10: body})
		svc := NewService(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges})
		defer svc.Close()

		opened, err := svc.OpenStream(context.Background(), testChannelID, 10)
		if err != nil {
			t.Fatalf("OpenStream: %v", err)
		}
		defer func() { _ = svc.CloseSession(opened.Token) }()
		if opened.MimeType != "image/png" {
			t.Fatalf("MimeType = %q, want image/png", opened.MimeType)
		}

		resp, err := http.Get(opened.URL)
		if err != nil {
			t.Fatalf("GET image: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if got := resp.Header.Get("Content-Type"); got != "image/png" {
			t.Fatalf("Content-Type = %q, want image/png", got)
		}
	})

	t.Run("dimension admission", func(t *testing.T) {
		db := newResolverTestDB(t)
		body := mediaPNGHeader(20_000, 20_000)
		mustApplyOp(t, db, 10, projection.Op{
			Type: projection.OpFileUpload, Parent: projection.RootParent,
			Name: "bomb.png", FileSize: int64(len(body)),
		})
		ranges := newMediaRangeFake(map[int64][]byte{10: body})
		svc := NewService(Config{DB: db, Peers: staticPeerResolver{peer: ranges.peer}, Ranges: ranges})
		defer svc.Close()

		_, err := svc.OpenStream(context.Background(), testChannelID, 10)
		if !errors.Is(err, ErrImageTooLarge) {
			t.Fatalf("dimension admission error = %v, want ErrImageTooLarge", err)
		}
		if got := len(svc.server.sessions); got != 0 {
			t.Fatalf("oversize image published %d sessions", got)
		}
	})
}

func jpegFixture(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0x80, A: 0xff})
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return out.Bytes()
}

func mediaPNGHeader(width, height uint32) []byte {
	var out bytes.Buffer
	out.Write([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], width)
	binary.BigEndian.PutUint32(ihdr[4:8], height)
	ihdr[8] = 8
	ihdr[9] = 2
	_ = binary.Write(&out, binary.BigEndian, uint32(len(ihdr)))
	out.WriteString("IHDR")
	out.Write(ihdr)
	_ = binary.Write(&out, binary.BigEndian, crc32.ChecksumIEEE(append([]byte("IHDR"), ihdr...)))
	return out.Bytes()
}

func itoa(value int64) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	index := len(buf)
	for value > 0 {
		index--
		buf[index] = digits[value%10]
		value /= 10
	}
	return string(buf[index:])
}
