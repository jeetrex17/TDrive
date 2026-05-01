package projection

import "testing"

func TestEncryptionConfigStoresHint(t *testing.T) {
	db := newTestDB(t)

	cfg := EncryptionConfig{
		ChannelID:        testChan,
		Enabled:          true,
		KDFSalt:          []byte("salt"),
		KDFParamsJSON:    `{"memory":1,"time":1,"parallelism":1,"key_len":32,"salt_len":4}`,
		WrappedMasterKey: []byte("wrapped"),
		KeyCheck:         []byte("check"),
		Hint:             "pet name",
		Version:          1,
	}
	if err := PutEncryptionConfig(db, cfg); err != nil {
		t.Fatalf("put config: %v", err)
	}

	got, err := GetEncryptionConfig(db, testChan)
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if got.Hint != "pet name" {
		t.Fatalf("hint = %q", got.Hint)
	}

	cfg.Hint = ""
	if err := PutEncryptionConfig(db, cfg); err != nil {
		t.Fatalf("clear hint: %v", err)
	}
	got, err = GetEncryptionConfig(db, testChan)
	if err != nil {
		t.Fatalf("get cleared config: %v", err)
	}
	if got.Hint != "" {
		t.Fatalf("hint after clear = %q", got.Hint)
	}
}
