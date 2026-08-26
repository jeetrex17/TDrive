package updater

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

type manifestTestKey struct {
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
}

func newManifestTestKey(t *testing.T) manifestTestKey {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return manifestTestKey{publicKey: publicKey, privateKey: privateKey}
}

func manifestRecord(t *testing.T, key manifestTestKey, manifest []byte) string {
	t.Helper()
	keyID, err := manifestPublicKeyID(key.publicKey)
	if err != nil {
		t.Fatalf("manifestPublicKeyID: %v", err)
	}
	signature := ed25519.Sign(key.privateKey, manifestMessage(manifest))
	return fmt.Sprintf("%s %s %s\n", manifestSignatureScheme, keyID, base64.StdEncoding.EncodeToString(signature))
}

func TestManifestKeyRingVerifiesDomainSeparatedManifest(t *testing.T) {
	key := newManifestTestKey(t)
	manifest := []byte("abc123  TDrive-v1.7.0-macos-arm64.zip\n")
	envelope := []byte(manifestRecord(t, key, manifest))
	ring := newManifestKeyRing([]ed25519.PublicKey{key.publicKey})

	if err := ring.Verify(manifest, envelope); err != nil {
		t.Fatalf("Verify valid signature: %v", err)
	}
	tamperedManifest := append(append([]byte(nil), manifest...), 'x')
	if err := ring.Verify(tamperedManifest, envelope); !errors.Is(err, ErrManifestSignature) {
		t.Fatalf("Verify tampered manifest = %v, want ErrManifestSignature", err)
	}
	if err := ring.Verify(manifest, nil); !errors.Is(err, ErrManifestSignature) {
		t.Fatalf("Verify missing signature = %v, want ErrManifestSignature", err)
	}

	keyID, err := manifestPublicKeyID(key.publicKey)
	if err != nil {
		t.Fatal(err)
	}
	undomained := ed25519.Sign(key.privateKey, manifest)
	undomainedEnvelope := fmt.Sprintf("%s %s %s\n", manifestSignatureScheme, keyID, base64.StdEncoding.EncodeToString(undomained))
	if err := ring.Verify(manifest, []byte(undomainedEnvelope)); !errors.Is(err, ErrManifestSignature) {
		t.Fatalf("Verify signature without domain separation = %v, want ErrManifestSignature", err)
	}
}

func TestManifestKeyRingSupportsRotationAndCopiesKeys(t *testing.T) {
	oldKey := newManifestTestKey(t)
	newKey := newManifestTestKey(t)
	manifest := []byte("signed manifest\n")
	ring := newManifestKeyRing([]ed25519.PublicKey{oldKey.publicKey, newKey.publicKey})
	envelope := []byte(manifestRecord(t, oldKey, manifest) + manifestRecord(t, newKey, manifest))

	for i := range oldKey.publicKey {
		oldKey.publicKey[i] = 0
	}
	for i := range newKey.publicKey {
		newKey.publicKey[i] = 0
	}

	if err := ring.Verify(manifest, envelope); err != nil {
		t.Fatalf("Verify multi-signature envelope with copied rotation keys: %v", err)
	}
}

func TestManifestKeyRingAllowsUnknownKeyAlongsideValidKey(t *testing.T) {
	trustedKey := newManifestTestKey(t)
	unknownKey := newManifestTestKey(t)
	manifest := []byte("manifest\n")
	envelope := []byte(manifestRecord(t, unknownKey, manifest) + manifestRecord(t, trustedKey, manifest))
	ring := newManifestKeyRing([]ed25519.PublicKey{trustedKey.publicKey})

	if err := ring.Verify(manifest, envelope); err != nil {
		t.Fatalf("Verify unknown plus valid signature: %v", err)
	}
}

func TestManifestKeyRingRejectsMalformedOrDuplicateRecords(t *testing.T) {
	key := newManifestTestKey(t)
	manifest := []byte("manifest\n")
	validRecord := strings.TrimSuffix(manifestRecord(t, key, manifest), "\n")
	keyID, err := manifestPublicKeyID(key.publicKey)
	if err != nil {
		t.Fatal(err)
	}
	validSignature := base64.StdEncoding.EncodeToString(ed25519.Sign(key.privateKey, manifestMessage(manifest)))
	shortSignature := base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize-1))

	cases := map[string]string{
		"wrong scheme":      "tdrive-ed25519-v2 " + keyID + " " + validSignature + "\n",
		"extra spaces":      manifestSignatureScheme + "  " + keyID + " " + validSignature + "\n",
		"invalid key id":    manifestSignatureScheme + " xyz " + validSignature + "\n",
		"uppercase key id":  manifestSignatureScheme + " " + strings.ToUpper(keyID) + " " + validSignature + "\n",
		"invalid base64":    manifestSignatureScheme + " " + keyID + " !!!\n",
		"short signature":   manifestSignatureScheme + " " + keyID + " " + shortSignature + "\n",
		"blank record":      validRecord + "\n\n" + validRecord + "\n",
		"duplicate trusted": validRecord + "\n" + validRecord + "\n",
	}
	for name, envelope := range cases {
		t.Run(name, func(t *testing.T) {
			ring := newManifestKeyRing([]ed25519.PublicKey{key.publicKey})
			if err := ring.Verify(manifest, []byte(envelope)); !errors.Is(err, ErrManifestSignature) {
				t.Fatalf("Verify = %v, want ErrManifestSignature", err)
			}
		})
	}
}

func TestManifestKeyRingRejectsUnknownOrAllInvalidSignatures(t *testing.T) {
	trustedKey := newManifestTestKey(t)
	otherKey := newManifestTestKey(t)
	manifest := []byte("manifest\n")
	ring := newManifestKeyRing([]ed25519.PublicKey{trustedKey.publicKey})

	if err := ring.Verify(manifest, []byte(manifestRecord(t, otherKey, manifest))); !errors.Is(err, ErrManifestSignature) {
		t.Fatalf("Verify unknown-only envelope = %v, want ErrManifestSignature", err)
	}
	trustedID, err := manifestPublicKeyID(trustedKey.publicKey)
	if err != nil {
		t.Fatal(err)
	}
	wrongSignature := ed25519.Sign(otherKey.privateKey, manifestMessage(manifest))
	invalidEnvelope := fmt.Sprintf("%s %s %s\n", manifestSignatureScheme, trustedID, base64.StdEncoding.EncodeToString(wrongSignature))
	if err := ring.Verify(manifest, []byte(invalidEnvelope)); !errors.Is(err, ErrManifestSignature) {
		t.Fatalf("Verify all-invalid envelope = %v, want ErrManifestSignature", err)
	}
}

func TestManifestKeyRingRejectsInvalidKeys(t *testing.T) {
	manifest := []byte("manifest\n")
	ring := newManifestKeyRing([]ed25519.PublicKey{make(ed25519.PublicKey, ed25519.PublicKeySize-1)})

	if err := ring.Verify(manifest, []byte("anything\n")); !errors.Is(err, ErrManifestSignature) {
		t.Fatalf("Verify with invalid key ring = %v, want ErrManifestSignature", err)
	}
}

func TestMustManifestKeyRingFromHexRejectsInvalidConfiguration(t *testing.T) {
	key := newManifestTestKey(t)
	validKey := hex.EncodeToString(key.publicKey)
	cases := map[string][]string{
		"empty":     nil,
		"malformed": {"xyz"},
		"uppercase": {strings.ToUpper(validKey)},
		"duplicate": {validKey, validKey},
	}
	for name, encodedKeys := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("mustManifestKeyRingFromHex(%v) did not panic", encodedKeys)
				}
			}()
			_ = mustManifestKeyRingFromHex(encodedKeys)
		})
	}
}

func TestProductionManifestKeyRingExcludesBootstrapPlaceholder(t *testing.T) {
	placeholderKey, err := hex.DecodeString(bootstrapManifestPublicKeyPlaceholderHex)
	if err != nil {
		t.Fatal(err)
	}
	placeholderID, err := manifestPublicKeyID(ed25519.PublicKey(placeholderKey))
	if err != nil {
		t.Fatal(err)
	}

	ring := productionManifestKeyRing()
	if _, ok := ring.key(placeholderID); ok {
		t.Fatal("bootstrap placeholder must never be accepted as a production trust anchor")
	}
}

func TestReleaseSigningArtifactsUseEmbeddedKeys(t *testing.T) {
	manifestPath := os.Getenv("TDRIVE_RELEASE_MANIFEST_PATH")
	envelopePath := os.Getenv("TDRIVE_RELEASE_SIGNATURE_PATH")
	encodedKeyIDs := os.Getenv("TDRIVE_RELEASE_SIGNING_KEY_IDS")
	if manifestPath == "" && envelopePath == "" && encodedKeyIDs == "" {
		t.Skip("release artifact verification only runs in the signing workflow")
	}
	if manifestPath == "" || envelopePath == "" || encodedKeyIDs == "" {
		t.Fatal("release artifact verification requires manifest, signature, and key-id inputs")
	}
	for _, encodedPublicKey := range productionManifestPublicKeyHexes() {
		if encodedPublicKey == bootstrapManifestPublicKeyPlaceholderHex {
			t.Fatal("replace the bootstrap manifest public key before publishing a release")
		}
	}

	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read release manifest: %v", err)
	}
	envelope, err := os.ReadFile(envelopePath)
	if err != nil {
		t.Fatalf("read release signature: %v", err)
	}
	records, err := parseManifestSignatureEnvelope(envelope)
	if err != nil {
		t.Fatalf("parse release signature: %v", err)
	}

	expectedKeyIDs := make(map[string]struct{})
	for _, keyID := range strings.Split(encodedKeyIDs, ",") {
		if !validManifestKeyID(keyID) {
			t.Fatalf("release signing key id %q is malformed", keyID)
		}
		expectedKeyIDs[keyID] = struct{}{}
	}
	if len(records) != len(expectedKeyIDs) {
		t.Fatalf("signature records = %d, signing keys = %d", len(records), len(expectedKeyIDs))
	}

	ring := productionManifestKeyRing()
	message := manifestMessage(manifest)
	for _, record := range records {
		if _, ok := expectedKeyIDs[record.keyID]; !ok {
			t.Fatalf("unexpected signature for key %s", record.keyID)
		}
		publicKey, ok := ring.key(record.keyID)
		if !ok {
			t.Fatalf("release signing key %s is not embedded in the updater", record.keyID)
		}
		if !ed25519.Verify(ed25519.PublicKey(publicKey[:]), message, record.signature[:]) {
			t.Fatalf("release signature from key %s is invalid", record.keyID)
		}
		delete(expectedKeyIDs, record.keyID)
	}
	if len(expectedKeyIDs) != 0 {
		t.Fatalf("release signature is missing %d configured signing key(s)", len(expectedKeyIDs))
	}
}
