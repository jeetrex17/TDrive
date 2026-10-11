//go:build android && cgo

package biometric

/*
#include <stdlib.h>
#include <stdint.h>
int tdrive_android_biometric_call(uint64_t id, int operation, const char *scope,
    const char *config, const void *secret, size_t secret_length, void **bytes, size_t *length);
void tdrive_android_biometric_cancel(uint64_t id);
*/
import "C"

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"
)

type androidProvider struct{}

var androidRequests atomic.Uint64

// NewProvider uses the Java host's app-private no-backup directory. A CLI or
// other process without that host fails closed and retains password fallback.
func NewProvider(_ string) Provider { return androidProvider{} }

func (androidProvider) Status(ctx context.Context, scope Scope) (Status, error) {
	result, err := callAndroid(ctx, 1, scope, nil)
	if errors.Is(err, ErrUnavailable) {
		return Status{Kind: "biometric", Reason: "unavailable"}, nil
	}
	if err != nil {
		return Status{}, err
	}
	defer clear(result)
	if len(result) != 4 {
		return Status{}, errors.New("invalid Android biometric status")
	}
	reasons := [...]string{"", "unsupported", "not_enrolled", "unavailable", "invalidated"}
	if int(result[3]) >= len(reasons) {
		return Status{}, errors.New("invalid Android biometric availability")
	}
	status := Status{Available: result[1] == 1, Enrolled: result[2] == 1,
		Kind: "biometric", Reason: reasons[result[3]]}
	if !status.Enrolled && status.Reason == "" {
		status.Reason = "not_enrolled"
	}
	return status, nil
}

func (androidProvider) Enroll(ctx context.Context, scope Scope, master []byte) error {
	secret, err := EncodeSecret(scope, master)
	if err != nil {
		return err
	}
	defer clear(secret)
	result, err := callAndroid(ctx, 2, scope, secret)
	clear(result)
	return err
}

func (androidProvider) Unlock(ctx context.Context, scope Scope) ([]byte, error) {
	result, err := callAndroid(ctx, 3, scope, nil)
	if err != nil {
		return nil, err
	}
	defer clear(result)
	return DecodeSecret(scope, result[1:])
}

func (androidProvider) Delete(ctx context.Context, scope Scope) error {
	result, err := callAndroid(ctx, 4, scope, nil)
	clear(result)
	return err
}

type androidResult struct {
	bytes []byte
	err   error
}

func callAndroid(ctx context.Context, operation int, scope Scope, secret []byte) ([]byte, error) {
	if err := scope.Valid(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	id := androidRequests.Add(1)
	done := make(chan androidResult)
	payload := bytes.Clone(secret)
	go func() {
		defer clear(payload)
		result, err := nativeAndroidCall(id, operation, scope, payload)
		select {
		case done <- androidResult{result, err}:
		case <-ctx.Done():
			clear(result)
		}
	}()
	select {
	case <-ctx.Done():
		C.tdrive_android_biometric_cancel(C.uint64_t(id))
		return nil, ctx.Err()
	case result := <-done:
		if err := ctx.Err(); err != nil {
			clear(result.bytes)
			C.tdrive_android_biometric_cancel(C.uint64_t(id))
			return nil, err
		}
		return result.bytes, result.err
	}
}

func nativeAndroidCall(id uint64, operation int, scope Scope, secret []byte) ([]byte, error) {
	scopeName := C.CString(scope.ID())
	defer C.free(unsafe.Pointer(scopeName))
	config := C.CString(strings.ToLower(scope.ConfigHash))
	defer C.free(unsafe.Pointer(config))
	var input unsafe.Pointer
	if len(secret) > 0 {
		input = unsafe.Pointer(&secret[0])
	}
	var output unsafe.Pointer
	var size C.size_t
	code := C.tdrive_android_biometric_call(C.uint64_t(id), C.int(operation), scopeName, config,
		input, C.size_t(len(secret)), &output, &size)
	if output != nil {
		defer func() {
			clear(unsafe.Slice((*byte)(output), int(size)))
			C.free(output)
		}()
	}
	if code != 0 || output == nil || size == 0 || size > 65 {
		return nil, ErrUnavailable
	}
	result := C.GoBytes(output, C.int(size))
	if result[0] != 0 {
		code := result[0]
		clear(result)
		switch code {
		case 1:
			return nil, ErrUnavailable
		case 2:
			return nil, ErrNotEnrolled
		case 3:
			return nil, ErrCanceled
		case 4:
			return nil, ErrInvalidated
		default:
			return nil, errors.New("Android biometric operation failed")
		}
	}
	return result, nil
}
