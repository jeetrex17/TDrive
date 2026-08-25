package core

import "testing"

func TestNewFileServiceWiresUploadConcurrencyLimit(t *testing.T) {
	engine := &Engine{maxUploads: 5}
	service := engine.newFileService()
	if service.MaxConcurrentUploads != 5 {
		t.Fatalf("MaxConcurrentUploads = %d, want 5", service.MaxConcurrentUploads)
	}
}
