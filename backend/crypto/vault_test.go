package crypto

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

func TestKeyWrapRoundTrip(t *testing.T) {
	password := []byte("correct horse battery staple")
	params := DefaultParams()
	salt, err := NewSalt(params)
	if err != nil {
		t.Fatalf("NewSalt: %v", err)
	}
	master, err := NewMasterKey()
	if err != nil {
		t.Fatalf("NewMasterKey: %v", err)
	}

	kek, err := DeriveKEK(password, salt, params)
	if err != nil {
		t.Fatalf("DeriveKEK: %v", err)
	}
	wrapped, err := WrapMasterKey(master, kek)
	if err != nil {
		t.Fatalf("WrapMasterKey: %v", err)
	}
	check, err := EncodeKeyCheck(master)
	if err != nil {
		t.Fatalf("EncodeKeyCheck: %v", err)
	}

	// Correct password.
	kek2, err := DeriveKEK(password, salt, params)
	if err != nil {
		t.Fatalf("DeriveKEK second call: %v", err)
	}
	got, err := UnwrapMasterKey(wrapped, kek2)
	if err != nil {
		t.Fatalf("UnwrapMasterKey: %v", err)
	}
	if !bytes.Equal(got, master) {
		t.Fatal("unwrapped master does not match")
	}
	if err := VerifyKeyCheck(got, check); err != nil {
		t.Fatalf("VerifyKeyCheck: %v", err)
	}

	// Wrong password.
	wrongKEK, err := DeriveKEK([]byte("wrong"), salt, params)
	if err != nil {
		t.Fatalf("DeriveKEK wrong password: %v", err)
	}
	if _, err := UnwrapMasterKey(wrapped, wrongKEK); err != ErrWrongPassword {
		t.Fatalf("wrong password: want ErrWrongPassword, got %v", err)
	}

	// Tampered wrapped blob.
	tampered := append([]byte(nil), wrapped...)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := UnwrapMasterKey(tampered, kek); err != ErrWrongPassword {
		t.Fatalf("tampered wrap: want ErrWrongPassword, got %v", err)
	}

	// Tampered key-check.
	tcheck := append([]byte(nil), check...)
	tcheck[len(tcheck)-1] ^= 0x01
	if err := VerifyKeyCheck(master, tcheck); err != ErrWrongPassword {
		t.Fatalf("tampered keycheck: want ErrWrongPassword, got %v", err)
	}
}

func TestParamsRoundTrip(t *testing.T) {
	in := DefaultParams()
	s, err := MarshalParams(in)
	if err != nil {
		t.Fatalf("MarshalParams: %v", err)
	}
	out, err := UnmarshalParams(s)
	if err != nil {
		t.Fatalf("UnmarshalParams: %v", err)
	}
	if out != in {
		t.Fatalf("round trip mismatch: %+v vs %+v", in, out)
	}
}

func TestUnmarshalParamsAcceptsLegacyArgon2idMetadata(t *testing.T) {
	legacy := `{"memory":65536,"time":3,"parallelism":4,"key_len":32,"salt_len":16}`
	params, err := UnmarshalParams(legacy)
	if err != nil {
		t.Fatalf("UnmarshalParams legacy metadata: %v", err)
	}
	if params.KDF != KDFArgon2id {
		t.Fatalf("KDF = %q, want %q", params.KDF, KDFArgon2id)
	}
	if _, err := DeriveKEK([]byte("password"), bytes.Repeat([]byte{1}, 16), params); err != nil {
		t.Fatalf("DeriveKEK legacy metadata: %v", err)
	}
}

func TestUnmarshalParamsRejectsUntrustedKDFMetadata(t *testing.T) {
	tests := []struct {
		name string
		json string
		want error
	}{
		{name: "zero memory", json: `{"memory":0,"time":3,"parallelism":4,"key_len":32,"salt_len":16}`, want: ErrInvalidKDFParams},
		{name: "extreme memory", json: `{"memory":4294967295,"time":3,"parallelism":4,"key_len":32,"salt_len":16}`, want: ErrInvalidKDFParams},
		{name: "zero time", json: `{"memory":65536,"time":0,"parallelism":4,"key_len":32,"salt_len":16}`, want: ErrInvalidKDFParams},
		{name: "extreme time", json: `{"memory":65536,"time":4294967295,"parallelism":4,"key_len":32,"salt_len":16}`, want: ErrInvalidKDFParams},
		{name: "zero parallelism", json: `{"memory":65536,"time":3,"parallelism":0,"key_len":32,"salt_len":16}`, want: ErrInvalidKDFParams},
		{name: "extreme parallelism", json: `{"memory":65536,"time":3,"parallelism":255,"key_len":32,"salt_len":16}`, want: ErrInvalidKDFParams},
		{name: "zero key length", json: `{"memory":65536,"time":3,"parallelism":4,"key_len":0,"salt_len":16}`, want: ErrInvalidKDFParams},
		{name: "extreme key length", json: `{"memory":65536,"time":3,"parallelism":4,"key_len":4294967295,"salt_len":16}`, want: ErrInvalidKDFParams},
		{name: "zero salt length", json: `{"memory":65536,"time":3,"parallelism":4,"key_len":32,"salt_len":0}`, want: ErrInvalidKDFParams},
		{name: "extreme salt length", json: `{"memory":65536,"time":3,"parallelism":4,"key_len":32,"salt_len":2147483647}`, want: ErrInvalidKDFParams},
		{name: "unsupported kdf", json: `{"kdf":"scrypt","memory":65536,"time":3,"parallelism":4,"key_len":32,"salt_len":16}`, want: ErrUnsupportedKDF},
		{name: "empty explicit kdf", json: `{"kdf":"","memory":65536,"time":3,"parallelism":4,"key_len":32,"salt_len":16}`, want: ErrUnsupportedKDF},
		{name: "null explicit kdf", json: `{"kdf":null,"memory":65536,"time":3,"parallelism":4,"key_len":32,"salt_len":16}`, want: ErrUnsupportedKDF},
		{name: "unknown field", json: `{"memory":65536,"time":3,"parallelism":4,"key_len":32,"salt_len":16,"surprise":true}`, want: ErrInvalidKDFParams},
		{name: "trailing document", json: `{"memory":65536,"time":3,"parallelism":4,"key_len":32,"salt_len":16}{}`, want: ErrInvalidKDFParams},
		{name: "oversized metadata", json: `{"memory":65536,"time":3,"parallelism":4,"key_len":32,"salt_len":16}` + string(bytes.Repeat([]byte(" "), 1024)), want: ErrInvalidKDFParams},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params, err := UnmarshalParams(test.json)
			if !errors.Is(err, test.want) || params != (Params{}) {
				t.Fatalf("UnmarshalParams = %+v, %v; want zero params and %v", params, err, test.want)
			}
		})
	}
}

func TestDeriveKEKRejectsDangerousInputsBeforeArgon2(t *testing.T) {
	valid := DefaultParams()
	tests := []struct {
		name   string
		params Params
		salt   []byte
	}{
		{name: "extreme memory", params: withParams(valid, func(p *Params) { p.Memory = math.MaxUint32 }), salt: bytes.Repeat([]byte{1}, valid.SaltLen)},
		{name: "extreme time", params: withParams(valid, func(p *Params) { p.Time = math.MaxUint32 }), salt: bytes.Repeat([]byte{1}, valid.SaltLen)},
		{name: "extreme parallelism", params: withParams(valid, func(p *Params) { p.Parallelism = math.MaxUint8 }), salt: bytes.Repeat([]byte{1}, valid.SaltLen)},
		{name: "extreme key length", params: withParams(valid, func(p *Params) { p.KeyLen = math.MaxUint32 }), salt: bytes.Repeat([]byte{1}, valid.SaltLen)},
		{name: "empty salt", params: valid, salt: nil},
		{name: "oversized salt", params: valid, salt: bytes.Repeat([]byte{1}, 65)},
		{name: "mismatched salt", params: valid, salt: bytes.Repeat([]byte{1}, valid.SaltLen-1)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key, err := DeriveKEK([]byte("password"), test.salt, test.params)
			if key != nil || !errors.Is(err, ErrInvalidKDFParams) {
				t.Fatalf("DeriveKEK = %x, %v; want nil, ErrInvalidKDFParams", key, err)
			}
		})
	}
}

func TestNewSaltRejectsInvalidParamsBeforeAllocation(t *testing.T) {
	params := DefaultParams()
	params.SaltLen = math.MaxInt
	if salt, err := NewSalt(params); salt != nil || !errors.Is(err, ErrInvalidKDFParams) {
		t.Fatalf("NewSalt = %x, %v; want nil, ErrInvalidKDFParams", salt, err)
	}
}

func TestVaultBlobsRequireExactAuthenticatedEnvelopeSizes(t *testing.T) {
	master := bytes.Repeat([]byte{0x41}, masterKeyLen)
	kek := bytes.Repeat([]byte{0x42}, masterKeyLen)
	wrapped, err := WrapMasterKey(master, kek)
	if err != nil {
		t.Fatalf("WrapMasterKey: %v", err)
	}
	check, err := EncodeKeyCheck(master)
	if err != nil {
		t.Fatalf("EncodeKeyCheck: %v", err)
	}

	for _, size := range []int{0, len(wrapped) - 1, len(wrapped) + 1, 1 << 20} {
		if _, err := UnwrapMasterKey(bytes.Repeat([]byte{1}, size), kek); !errors.Is(err, ErrCorruptKeyData) {
			t.Errorf("UnwrapMasterKey size %d error = %v, want ErrCorruptKeyData", size, err)
		}
	}
	for _, size := range []int{0, len(check) - 1, len(check) + 1, 1 << 20} {
		if err := VerifyKeyCheck(master, bytes.Repeat([]byte{1}, size)); !errors.Is(err, ErrCorruptKeyData) {
			t.Errorf("VerifyKeyCheck size %d error = %v, want ErrCorruptKeyData", size, err)
		}
	}

	params := DefaultParams()
	salt := bytes.Repeat([]byte{1}, params.SaltLen)
	if err := ValidateVaultMaterial(salt, params, wrapped, check); err != nil {
		t.Fatalf("ValidateVaultMaterial(valid): %v", err)
	}
	if err := ValidateVaultMaterial(salt[:len(salt)-1], params, wrapped, check); !errors.Is(err, ErrInvalidKDFParams) {
		t.Fatalf("ValidateVaultMaterial(short salt) = %v, want ErrInvalidKDFParams", err)
	}
	if err := ValidateVaultMaterial(salt, params, wrapped[:len(wrapped)-1], check); !errors.Is(err, ErrCorruptKeyData) {
		t.Fatalf("ValidateVaultMaterial(short wrapped key) = %v, want ErrCorruptKeyData", err)
	}
	if err := ValidateVaultMaterial(salt, params, wrapped, check[:len(check)-1]); !errors.Is(err, ErrCorruptKeyData) {
		t.Fatalf("ValidateVaultMaterial(short key check) = %v, want ErrCorruptKeyData", err)
	}
}

func withParams(params Params, update func(*Params)) Params {
	copy := params
	update(&copy)
	return copy
}
