package file

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"

	tdcrypto "TDrive/backend/crypto"
	"TDrive/backend/projection"
)

const renditionEnvelopeMagic = "TDR1"

// envelopeBinding excludes the remote receipt and ciphertext size, which are
// unknown before encryption. Every source/format property is authenticated by
// the existing TDE1 stream with independent per-file salt and nonce randomness.
func envelopeBinding(ref projection.FileRendition) projection.FileRendition {
	ref.MsgID = 0
	ref.Size = 0
	return ref
}
func encryptRendition(jpeg, masterKey []byte, ref projection.FileRendition) ([]byte, error) {
	descriptor, err := json.Marshal(envelopeBinding(ref))
	if err != nil {
		return nil, err
	}
	if len(descriptor) > 2048 || len(jpeg) > int(projection.MaxRenditionBytes) {
		return nil, fmt.Errorf("rendition envelope exceeds size budget")
	}
	plain := make([]byte, 8+len(descriptor)+len(jpeg))
	defer clear(plain)
	copy(plain, renditionEnvelopeMagic)
	binary.BigEndian.PutUint32(plain[4:8], uint32(len(descriptor)))
	copy(plain[8:], descriptor)
	copy(plain[8+len(descriptor):], jpeg)
	var cipher bytes.Buffer
	if err := tdcrypto.EncryptStream(bytes.NewReader(plain), &cipher, masterKey, int64(len(plain))); err != nil {
		return nil, err
	}
	return cipher.Bytes(), nil
}

// decryptRendition authenticates the parent, channel, class and dimensions before
// image bytes can reach a renderer. Plain derivatives receive the same encoded
// size/dimension checks. The caller has already bounded the network writer.
func decryptRendition(raw, masterKey []byte, ref projection.FileRendition) ([]byte, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	if int64(len(raw)) != ref.Size {
		return nil, fmt.Errorf("rendition stored size mismatch")
	}
	jpeg := raw
	if ref.Encrypted {
		var plain bytes.Buffer
		if _, err := tdcrypto.DecryptStream(bytes.NewReader(raw), &plain, masterKey); err != nil {
			return nil, err
		}
		defer clear(plain.Bytes())
		data := plain.Bytes()
		if len(data) < 8 || string(data[:4]) != renditionEnvelopeMagic {
			return nil, fmt.Errorf("invalid rendition envelope")
		}
		n := int(binary.BigEndian.Uint32(data[4:8]))
		if n <= 0 || n > 2048 || n > len(data)-8 {
			return nil, fmt.Errorf("invalid rendition descriptor length")
		}
		var got projection.FileRendition
		if err := json.Unmarshal(data[8:8+n], &got); err != nil {
			return nil, fmt.Errorf("invalid rendition descriptor: %w", err)
		}
		if got != envelopeBinding(ref) {
			return nil, fmt.Errorf("rendition source binding mismatch")
		}
		jpeg = append([]byte(nil), data[8+n:]...)
	}
	if int64(len(jpeg)) != ref.PlaintextSize {
		return nil, fmt.Errorf("rendition image size mismatch")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(jpeg))
	if err != nil || format != "jpeg" || cfg.Width != ref.Width || cfg.Height != ref.Height {
		return nil, fmt.Errorf("rendition image dimensions mismatch")
	}
	return jpeg, nil
}
