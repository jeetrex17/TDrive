//go:build (darwin || ios) && cgo

package biometric

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -fblocks
#cgo LDFLAGS: -framework Foundation -framework Security -framework LocalAuthentication
#include "provider_apple.h"
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"fmt"
	"unsafe"
)

type appleNative interface {
	status(string) (Status, error)
	enroll(context.Context, string, []byte) error
	unlock(context.Context, string) ([]byte, error)
	delete(context.Context, string) error
}

type appleProvider struct {
	native appleNative
}

// NewProvider uses Apple's device-only data protection Keychain. The process
// must have provisioned Keychain entitlements; ad-hoc macOS builds fail closed.
func NewProvider(_ string) Provider {
	return &appleProvider{native: appleKeychain{}}
}

func (p *appleProvider) Status(ctx context.Context, scope Scope) (Status, error) {
	if err := scope.Valid(); err != nil {
		return Status{}, err
	}
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	return p.native.status(scope.ID())
}

func (p *appleProvider) Enroll(ctx context.Context, scope Scope, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	secret, err := EncodeSecret(scope, key)
	if err != nil {
		return err
	}
	defer clear(secret)
	return p.native.enroll(ctx, scope.ID(), secret)
}

func (p *appleProvider) Unlock(ctx context.Context, scope Scope) ([]byte, error) {
	if err := scope.Valid(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	secret, err := p.native.unlock(ctx, scope.ID())
	defer clear(secret)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return DecodeSecret(scope, secret)
}

func (p *appleProvider) Delete(ctx context.Context, scope Scope) error {
	if err := scope.Valid(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.native.delete(ctx, scope.ID())
}

type appleKeychain struct{}

func (appleKeychain) status(id string) (Status, error) {
	account := C.CString(id)
	defer C.free(unsafe.Pointer(account))
	var enrolled, kind C.int
	code := int(C.tdrive_bio_status(account, &enrolled, &kind))
	return appleStatus(code, enrolled != 0, int(kind)), nil
}

func appleStatus(code int, enrolled bool, kind int) Status {
	label := "biometric"
	if kind == 1 {
		label = "touch_id"
	} else if kind == 2 {
		label = "face_id"
	}
	status := Status{Available: code == 0, Enrolled: enrolled, Kind: label}
	if err := appleError(code); err != nil {
		// Capability and lockout failures are actionable availability states,
		// not a failing settings request. Password unlock remains available.
		switch code {
		case 3:
			status.Reason = "locked_out"
		case 4:
			status.Reason = "invalidated"
		case 5:
			status.Reason = "not_enrolled"
		case 6:
			status.Reason = "signing_required"
		default:
			status.Reason = "unavailable"
		}
	} else if !status.Enrolled {
		status.Reason = "not_enrolled"
	}
	return status
}

// appleAuthenticationContext invalidates an in-flight OS prompt on cancellation.
// Cleanup joins the cancellation callback before releasing the native context.
func appleAuthenticationContext(ctx context.Context) (unsafe.Pointer, func()) {
	handle := C.tdrive_bio_context_create()
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		C.tdrive_bio_context_cancel(handle)
		close(done)
	})
	return handle, func() {
		if !stop() {
			<-done
		}
		C.tdrive_bio_context_release(handle)
	}
}

func (appleKeychain) enroll(ctx context.Context, id string, secret []byte) error {
	handle, cleanup := appleAuthenticationContext(ctx)
	defer cleanup()
	account := C.CString(id)
	defer C.free(unsafe.Pointer(account))
	code := C.tdrive_bio_enroll(handle, account, (*C.uchar)(unsafe.Pointer(&secret[0])), C.size_t(len(secret)))
	if err := ctx.Err(); err != nil {
		// A cancellation concurrent with SecItemAdd must not leave behind a
		// new enrollment after the owning vault/account session was closed.
		if code == 0 {
			C.tdrive_bio_delete(account)
		}
		return err
	}
	return appleError(int(code))
}

func (appleKeychain) unlock(ctx context.Context, id string) ([]byte, error) {
	handle, cleanup := appleAuthenticationContext(ctx)
	defer cleanup()
	account := C.CString(id)
	defer C.free(unsafe.Pointer(account))
	var data *C.uchar
	var size C.size_t
	code := C.tdrive_bio_unlock(handle, account, &data, &size)
	defer C.tdrive_bio_secret_free(data, size)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := appleError(int(code)); err != nil {
		return nil, err
	}
	if data == nil || size != 64 {
		return nil, ErrInvalidated
	}
	return C.GoBytes(unsafe.Pointer(data), C.int(size)), nil
}

func (appleKeychain) delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	account := C.CString(id)
	defer C.free(unsafe.Pointer(account))
	return appleError(int(C.tdrive_bio_delete(account)))
}

func appleError(code int) error {
	switch code {
	case 0:
		return nil
	case 2:
		return ErrCanceled
	case 3:
		return fmt.Errorf("%w: biometrics are locked; use your encryption password", ErrUnavailable)
	case 4:
		return ErrInvalidated
	case 5:
		return ErrNotEnrolled
	case 6:
		return fmt.Errorf("%w: this build needs provisioned Keychain signing", ErrUnavailable)
	case 7:
		return fmt.Errorf("%w: authentication failed; use your encryption password", ErrUnavailable)
	default:
		return ErrUnavailable
	}
}
