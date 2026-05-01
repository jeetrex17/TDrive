package crypto

import (
	"bytes"
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

	kek := DeriveKEK(password, salt, params)
	wrapped, err := WrapMasterKey(master, kek)
	if err != nil {
		t.Fatalf("WrapMasterKey: %v", err)
	}
	check, err := EncodeKeyCheck(master)
	if err != nil {
		t.Fatalf("EncodeKeyCheck: %v", err)
	}

	// Correct password.
	kek2 := DeriveKEK(password, salt, params)
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
	wrongKEK := DeriveKEK([]byte("wrong"), salt, params)
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
