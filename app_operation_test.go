package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"syscall"
	"testing"

	encservice "TDrive/backend/services/encryption"
)

func TestOperationErrorCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code OperationErrorCode
	}{
		{name: "backend", err: errBackendUnavailable, code: OperationCodeBackendUnavailable},
		{name: "password required", err: encservice.ErrPasswordRequired, code: OperationCodeEncryptionPasswordRequired},
		{name: "wrong password", err: encservice.ErrWrongPassword, code: OperationCodeInvalidEncryptionPassword},
		{name: "canceled", err: context.Canceled, code: OperationCodeCanceled},
		{name: "deadline", err: context.DeadlineExceeded, code: OperationCodeDeadlineExceeded},
		{name: "missing", err: fs.ErrNotExist, code: OperationCodeNotFound},
		{name: "permission", err: fs.ErrPermission, code: OperationCodePermissionDenied},
		{name: "collision", err: fs.ErrExist, code: OperationCodeAlreadyExists},
		{name: "disk full", err: syscall.ENOSPC, code: OperationCodeInsufficientStorage},
		{name: "unknown", err: errors.New("localized display text"), code: OperationCodeFailed},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := operationErrorCode(test.err); got != test.code {
				t.Fatalf("operationErrorCode(%v) = %q, want %q", test.err, got, test.code)
			}
		})
	}
}

func TestOperationResultJSONKeepsControlCodeSeparateFromDisplayMessage(t *testing.T) {
	result := operationFailureMessage(fs.ErrExist, "A folder with this name is already here")
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	got := string(encoded)
	if !strings.Contains(got, `"code":"already_exists"`) {
		t.Fatalf("JSON %s does not contain the stable collision code", got)
	}
	if !strings.Contains(got, `"message":"A folder with this name is already here"`) {
		t.Fatalf("JSON %s does not contain the display message", got)
	}
	if strings.Contains(got, "cause") {
		t.Fatalf("JSON %s leaked the native error cause", got)
	}
}

func TestMountOperationResultPreservesStableErrorCodeAndState(t *testing.T) {
	mount := MountView{Phase: "idle", Label: "Tdrive personal"}
	result := mountOperationResult(mount, encservice.ErrPasswordRequired)

	if result.Result.OK || result.Result.Error == nil {
		t.Fatalf("mount operation result = %#v, want failure", result)
	}
	if result.Result.Error.Code != OperationCodeEncryptionPasswordRequired {
		t.Fatalf("mount operation code = %q, want %q", result.Result.Error.Code, OperationCodeEncryptionPasswordRequired)
	}
	if result.Mount != mount {
		t.Fatalf("mount operation state = %#v, want %#v", result.Mount, mount)
	}
}
