package updater

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrManifestSignature means checksums.txt was not authenticated by any
// trusted TDrive release key. The updater must never parse or trust it.
var ErrManifestSignature = errors.New("update manifest signature verification failed")

const (
	manifestSignatureDomain   = "TDrive update manifest v1\x00"
	manifestSignatureScheme   = "tdrive-ed25519-v1"
	manifestSignatureMaxBytes = 16 * 1024
	manifestSignatureTimeout  = 15 * time.Second
)

// bootstrapManifestPublicKeyPlaceholderHex is compile-valid but not shippable.
// The release gate explicitly rejects a production ring containing this value.
const bootstrapManifestPublicKeyPlaceholderHex = "0000000000000000000000000000000000000000000000000000000000000000"

// productionManifestPublicKeyHexes returns a fresh trust-root declaration.
// Add both old and new raw public keys here for a bridge-release rotation.
func productionManifestPublicKeyHexes() []string {
	return []string{
		// Release signing key, generated 2026-08-26. The private half lives only in
		// the release-signing GitHub environment as TDRIVE_UPDATE_SIGNING_KEY_PEM.
		"d899038fd8adec0b152680f87c479620c38abac576adc67d3556a668606e15d8",
	}
}

type manifestPublicKey [ed25519.PublicKeySize]byte

type manifestTrustedKey struct {
	id  string
	key manifestPublicKey
}

// manifestKeyRing stores value copies so callers cannot change trusted keys
// after constructing a Service. Multiple entries allow overlap during rotation.
type manifestKeyRing struct {
	keys []manifestTrustedKey
}

func newManifestKeyRing(publicKeys []ed25519.PublicKey) manifestKeyRing {
	keys := make([]manifestTrustedKey, 0, len(publicKeys))
	seen := make(map[string]struct{}, len(publicKeys))
	for _, publicKey := range publicKeys {
		id, err := manifestPublicKeyID(publicKey)
		if err != nil {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		var key manifestPublicKey
		copy(key[:], publicKey)
		keys = append(keys, manifestTrustedKey{id: id, key: key})
		seen[id] = struct{}{}
	}
	return manifestKeyRing{keys: keys}
}

func productionManifestKeyRing() manifestKeyRing {
	encodedPublicKeys := productionManifestPublicKeyHexes()
	publicKeys := make([]string, 0, len(encodedPublicKeys))
	for _, encodedPublicKey := range encodedPublicKeys {
		if encodedPublicKey == bootstrapManifestPublicKeyPlaceholderHex {
			continue
		}
		publicKeys = append(publicKeys, encodedPublicKey)
	}
	if len(publicKeys) == 0 {
		return manifestKeyRing{}
	}
	return mustManifestKeyRingFromHex(publicKeys)
}

func mustManifestKeyRingFromHex(encodedPublicKeys []string) manifestKeyRing {
	if len(encodedPublicKeys) == 0 {
		panic("updater: embedded manifest key ring is empty")
	}
	publicKeys := make([]ed25519.PublicKey, 0, len(encodedPublicKeys))
	keyIDs := make(map[string]struct{}, len(encodedPublicKeys))
	for _, encodedPublicKey := range encodedPublicKeys {
		if len(encodedPublicKey) != ed25519.PublicKeySize*2 || !validLowerHex(encodedPublicKey) {
			panic("updater: invalid embedded manifest public key")
		}
		publicKey, err := hex.DecodeString(encodedPublicKey)
		if err != nil || len(publicKey) != ed25519.PublicKeySize {
			panic("updater: invalid embedded manifest public key")
		}
		typedPublicKey := ed25519.PublicKey(publicKey)
		keyID, err := manifestPublicKeyID(typedPublicKey)
		if err != nil {
			panic("updater: invalid embedded manifest public key")
		}
		if _, duplicate := keyIDs[keyID]; duplicate {
			panic("updater: duplicate embedded manifest public key")
		}
		keyIDs[keyID] = struct{}{}
		publicKeys = append(publicKeys, typedPublicKey)
	}
	return newManifestKeyRing(publicKeys)
}

func manifestPublicKeyID(publicKey ed25519.PublicKey) (string, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return "", fmt.Errorf("ed25519 public key has %d bytes", len(publicKey))
	}
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return "", fmt.Errorf("marshal manifest public key: %w", err)
	}
	digest := sha256.Sum256(der)
	return hex.EncodeToString(digest[:]), nil
}

// Verify authenticates the exact raw checksums.txt bytes with at least one
// recognized signature. Unknown key IDs are retained for rotation overlap.
func (r manifestKeyRing) Verify(manifest, envelope []byte) error {
	records, err := parseManifestSignatureEnvelope(envelope)
	if err != nil {
		return err
	}
	message := manifestMessage(manifest)
	seenRecognized := make(map[string]struct{}, len(r.keys))
	recognized, valid := false, false
	for _, record := range records {
		key, ok := r.key(record.keyID)
		if !ok {
			continue
		}
		if _, duplicate := seenRecognized[record.keyID]; duplicate {
			return fmt.Errorf("%w: duplicate signature for trusted key %s", ErrManifestSignature, record.keyID)
		}
		seenRecognized[record.keyID] = struct{}{}
		recognized = true
		if ed25519.Verify(ed25519.PublicKey(key[:]), message, record.signature[:]) {
			valid = true
		}
	}
	if !recognized {
		return fmt.Errorf("%w: no recognized signing key", ErrManifestSignature)
	}
	if !valid {
		return fmt.Errorf("%w: no valid trusted signature", ErrManifestSignature)
	}
	return nil
}

func (r manifestKeyRing) key(id string) (manifestPublicKey, bool) {
	for _, trustedKey := range r.keys {
		if trustedKey.id == id {
			return trustedKey.key, true
		}
	}
	return manifestPublicKey{}, false
}

type manifestSignatureRecord struct {
	keyID     string
	signature [ed25519.SignatureSize]byte
}

func parseManifestSignatureEnvelope(envelope []byte) ([]manifestSignatureRecord, error) {
	if len(envelope) == 0 {
		return nil, fmt.Errorf("%w: signature file is empty", ErrManifestSignature)
	}
	if len(envelope) > manifestSignatureMaxBytes {
		return nil, fmt.Errorf("%w: signature file exceeds %d bytes", ErrManifestSignature, manifestSignatureMaxBytes)
	}
	if bytes.IndexByte(envelope, '\r') >= 0 {
		return nil, fmt.Errorf("%w: carriage returns are not allowed", ErrManifestSignature)
	}

	lines := bytes.Split(envelope, []byte{'\n'})
	if len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("%w: signature file has no records", ErrManifestSignature)
	}

	records := make([]manifestSignatureRecord, 0, len(lines))
	for index, line := range lines {
		if len(line) == 0 {
			return nil, fmt.Errorf("%w: record %d is blank", ErrManifestSignature, index+1)
		}
		fields := bytes.Split(line, []byte{' '})
		if len(fields) != 3 || len(fields[0]) == 0 || len(fields[1]) == 0 || len(fields[2]) == 0 {
			return nil, fmt.Errorf("%w: record %d is malformed", ErrManifestSignature, index+1)
		}
		if string(fields[0]) != manifestSignatureScheme {
			return nil, fmt.Errorf("%w: record %d has an unsupported scheme", ErrManifestSignature, index+1)
		}
		keyID := string(fields[1])
		if !validManifestKeyID(keyID) {
			return nil, fmt.Errorf("%w: record %d has a malformed key id", ErrManifestSignature, index+1)
		}
		signature, err := base64.StdEncoding.Strict().DecodeString(string(fields[2]))
		if err != nil || len(signature) != ed25519.SignatureSize {
			return nil, fmt.Errorf("%w: record %d has a malformed signature", ErrManifestSignature, index+1)
		}
		var signatureValue [ed25519.SignatureSize]byte
		copy(signatureValue[:], signature)
		records = append(records, manifestSignatureRecord{keyID: keyID, signature: signatureValue})
	}
	return records, nil
}

func validManifestKeyID(keyID string) bool {
	if len(keyID) != sha256.Size*2 {
		return false
	}
	return validLowerHex(keyID)
}

func validLowerHex(value string) bool {
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

// fetchManifestSignature bounds both time and bytes independently of the
// caller's HTTP client. The envelope is intentionally tiny even during
// multi-key rotation.
func fetchManifestSignature(ctx context.Context, client *http.Client, userAgent, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, manifestSignatureTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d", url, resp.StatusCode)
	}
	if resp.ContentLength > manifestSignatureMaxBytes {
		return nil, fmt.Errorf("%w: response exceeds %d bytes", ErrManifestSignature, manifestSignatureMaxBytes)
	}
	envelope, err := io.ReadAll(io.LimitReader(resp.Body, manifestSignatureMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(envelope) > manifestSignatureMaxBytes {
		return nil, fmt.Errorf("%w: response exceeds %d bytes", ErrManifestSignature, manifestSignatureMaxBytes)
	}
	return envelope, nil
}

func manifestMessage(manifest []byte) []byte {
	message := make([]byte, len(manifestSignatureDomain)+len(manifest))
	copy(message, manifestSignatureDomain)
	copy(message[len(manifestSignatureDomain):], manifest)
	return message
}
