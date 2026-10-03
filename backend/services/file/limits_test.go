package file

import (
	"errors"
	"testing"

	tdcrypto "TDrive/backend/crypto"
)

func TestUploadByteSize(t *testing.T) {
	if got := uploadByteSize(1000, false); got != 1000 {
		t.Errorf("plain size = %d, want 1000", got)
	}
	if got, want := uploadByteSize(1000, true), tdcrypto.CiphertextSize(1000); got != want {
		t.Errorf("encrypted size = %d, want %d (ciphertext)", got, want)
	}
}

func TestPlanUpload(t *testing.T) {
	// MaxUploadBytes overrides the part size: 1000 here, hard cap = 1000*MaxParts.
	s := &Service{MaxUploadBytes: 1000}
	hardCap := int64(1000) * int64(MaxParts)

	// At or under the part size: a single upload, no split, no error.
	if stored, multi, err := s.planUpload("ok.bin", 900, false); err != nil || multi || stored != 900 {
		t.Errorf("900 plain: stored=%d multi=%v err=%v, want 900,false,nil", stored, multi, err)
	}
	if _, multi, err := s.planUpload("edge.bin", 1000, false); err != nil || multi {
		t.Errorf("1000 plain should be single, got multi=%v err=%v", multi, err)
	}

	// Over the part size but under the hard cap: multipart, not an error.
	if stored, multi, err := s.planUpload("big.bin", 1001, false); err != nil || !multi || stored != 1001 {
		t.Errorf("1001 plain: stored=%d multi=%v err=%v, want 1001,true,nil", stored, multi, err)
	}

	// Encryption overhead pushes a part-sized file into multipart.
	if _, multi, err := s.planUpload("enc.bin", 1000, true); err != nil || !multi {
		t.Errorf("encrypted 1000 should be multipart, got multi=%v err=%v", multi, err)
	}

	// Exactly at the hard cap is allowed (multipart); beyond it is rejected.
	if _, multi, err := s.planUpload("max.bin", hardCap, false); err != nil || !multi {
		t.Errorf("at hard cap: multi=%v err=%v, want true,nil", multi, err)
	}
	if _, _, err := s.planUpload("huge.bin", hardCap+1, false); err == nil || !errors.Is(err, ErrFileTooLarge) {
		t.Errorf("over hard cap should be ErrFileTooLarge, got %v", err)
	}

	// With no override, a 1 GiB file is well under the part size: single upload.
	if _, multi, err := (&Service{}).planUpload("g.bin", 1<<30, false); err != nil || multi {
		t.Errorf("1 GiB default should be single, got multi=%v err=%v", multi, err)
	}
	if got := (&Service{}).minResumableUploadBytes(); got != 2_000_000_000 || got <= MaxPartBytes {
		t.Errorf("default recovery threshold = %d, want 2 GB above the multipart boundary", got)
	}
	if got := s.minResumableUploadBytes(); got != s.MaxUploadBytes {
		t.Errorf("test recovery threshold = %d, want %d", got, s.MaxUploadBytes)
	}
}

func TestResumablePartPlan(t *testing.T) {
	s := &Service{}
	for _, size := range []int64{MaxPartBytes + 1, 10 << 30, LargeFileMaxBytes} {
		plan, err := s.buildResumablePartPlan(size)
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if plan.partCount < 2 || plan.partCount > MaxParts || plan.partSize > MaxPartBytes {
			t.Fatalf("size %d: invalid plan %+v", size, plan)
		}
		if size == MaxPartBytes+1 && plan.partSize != 512<<20 {
			t.Fatalf("small large file: part size %d, want 512 MiB", plan.partSize)
		}
		last, length, err := plan.window(size, plan.partCount-1)
		if err != nil || last+length != size || length <= 0 {
			t.Fatalf("size %d: final window %d+%d, err %v", size, last, length, err)
		}
	}
}
