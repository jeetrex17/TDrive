package biometric

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func testScope() Scope {
	return Scope{AccountID: 12, ChannelID: 34, ConfigHash: strings.Repeat("ab", 32)}
}

func TestScopeValidationAndStableNativeID(t *testing.T) {
	scope := testScope()
	if err := scope.Valid(); err != nil {
		t.Fatalf("valid scope: %v", err)
	}
	// Independent SHA256 fixture for the v1 domain and big-endian identifiers.
	if want := "5ed6ac9da397a5a2ff0494bc9fdde4cf375dc8bd789805ce2b143df2e852b482"; scope.ID() != want {
		t.Fatalf("native identifier = %q, want %q", scope.ID(), want)
	}
	if id, err := hex.DecodeString(scope.ID()); err != nil || len(id) != 32 {
		t.Fatalf("native identifier is not a SHA256 hex digest: %q, %v", scope.ID(), err)
	}
	changedConfig := scope
	changedConfig.ConfigHash = strings.Repeat("cd", 32)
	if changedConfig.ID() != scope.ID() {
		t.Fatal("password changes must retain the native item identity for cleanup")
	}
	for _, changed := range []Scope{
		{AccountID: 13, ChannelID: 34, ConfigHash: scope.ConfigHash},
		{AccountID: 12, ChannelID: 35, ConfigHash: scope.ConfigHash},
	} {
		if changed.ID() == scope.ID() {
			t.Fatal("different account or channel shares native storage identity")
		}
	}
	for _, invalid := range []Scope{
		{AccountID: 0, ChannelID: 34, ConfigHash: scope.ConfigHash},
		{AccountID: 12, ChannelID: -1, ConfigHash: scope.ConfigHash},
		{AccountID: 12, ChannelID: 34, ConfigHash: "ab"},
		{AccountID: 12, ChannelID: 34, ConfigHash: strings.Repeat("z", 64)},
	} {
		if err := invalid.Valid(); err == nil {
			t.Fatalf("invalid scope accepted: %+v", invalid)
		}
	}
}

func TestSecretEncodingBindsScopeAndOwnsBuffers(t *testing.T) {
	scope := testScope()
	key := bytes.Repeat([]byte{0x42}, 32)
	payload, err := EncodeSecret(scope, key)
	if err != nil {
		t.Fatalf("encode secret: %v", err)
	}
	if len(payload) != 64 || !bytes.Equal(payload[32:], key) {
		t.Fatal("native payload must contain a 32-byte scope digest and 32-byte key")
	}
	if want := "e603f8938e7f7a02812e1de8318f5116da557ad86adcd9708e9fa0be10f65fff"; hex.EncodeToString(payload[:32]) != want {
		t.Fatalf("native payload scope digest = %x, want %s", payload[:32], want)
	}
	clear(key)
	decoded, err := DecodeSecret(scope, payload)
	if err != nil || !bytes.Equal(decoded, bytes.Repeat([]byte{0x42}, 32)) {
		t.Fatalf("decode secret did not retain owned key: %v", err)
	}
	clear(payload)
	if !bytes.Equal(decoded, bytes.Repeat([]byte{0x42}, 32)) {
		t.Fatal("decoded key shares native payload backing memory")
	}
	clear(decoded)
}

func TestSecretDecodingRejectsOtherVaultsAndMalformedPayloads(t *testing.T) {
	scope := testScope()
	payload, err := EncodeSecret(scope, bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	for _, wrong := range []Scope{
		{AccountID: 13, ChannelID: scope.ChannelID, ConfigHash: scope.ConfigHash},
		{AccountID: scope.AccountID, ChannelID: 35, ConfigHash: scope.ConfigHash},
		{AccountID: scope.AccountID, ChannelID: scope.ChannelID, ConfigHash: strings.Repeat("cd", 32)},
	} {
		if key, err := DecodeSecret(wrong, payload); !errors.Is(err, ErrInvalidated) || key != nil {
			t.Fatalf("other scope returned key=%v, err=%v", key != nil, err)
		}
	}
	for _, malformed := range [][]byte{nil, payload[:63], append(bytes.Clone(payload), 0)} {
		if key, err := DecodeSecret(scope, malformed); !errors.Is(err, ErrInvalidated) || key != nil {
			t.Fatalf("malformed payload returned key=%v, err=%v", key != nil, err)
		}
	}
	payload[0] ^= 1
	if _, err := DecodeSecret(scope, payload); !errors.Is(err, ErrInvalidated) {
		t.Fatalf("tampered digest error = %v", err)
	}
	if _, err := EncodeSecret(scope, make([]byte, 31)); err == nil {
		t.Fatal("short master key accepted")
	}
	if _, err := EncodeSecret(Scope{}, make([]byte, 32)); !errors.Is(err, ErrInvalidated) {
		t.Fatalf("invalid encoding scope error = %v", err)
	}
	if key, err := DecodeSecret(Scope{}, payload); !errors.Is(err, ErrInvalidated) || key != nil {
		t.Fatalf("invalid decoding scope returned key=%v, err=%v", key != nil, err)
	}
}
