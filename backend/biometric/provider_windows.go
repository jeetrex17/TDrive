//go:build windows

package biometric

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func NewProvider(dataDir string) Provider { return newWindowsProvider(dataDir, nativeWindowsHello{}) }

type nativeWindowsHello struct{}

// GUIDs and vtable positions come from Microsoft's generated WinRT metadata:
// https://github.com/microsoft/windows-rs/blob/master/crates/libs/windows/src/Windows/Security/Credentials/mod.rs
// https://github.com/microsoft/windows-rs/blob/master/crates/libs/windows/src/Windows/Security/Cryptography/mod.rs
// Both IAsyncOperation.GetResults and IAsyncAction.GetResults are slot 8.
var (
	winRT                = windows.NewLazySystemDLL("combase.dll")
	winRoInitialize      = winRT.NewProc("RoInitialize")
	winRoUninitialize    = winRT.NewProc("RoUninitialize")
	winCreateString      = winRT.NewProc("WindowsCreateString")
	winDeleteString      = winRT.NewProc("WindowsDeleteString")
	winGetFactory        = winRT.NewProc("RoGetActivationFactory")
	iidCredentialManager = windows.GUID{Data1: 0x6aac468b, Data2: 0x0ef1, Data3: 0x4ce0, Data4: [8]byte{0x82, 0x90, 0x41, 0x06, 0xda, 0x6a, 0x63, 0xb5}}
	iidCryptoBuffer      = windows.GUID{Data1: 0x320b7e22, Data2: 0x3cb0, Data3: 0x4cdf, Data4: [8]byte{0x86, 0x63, 0x1d, 0x28, 0x91, 0x00, 0x65, 0xeb}}
	iidAsyncInfo         = windows.GUID{Data1: 0x00000036, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidBufferBytes       = windows.GUID{Data1: 0x905a0fef, Data2: 0xbc53, Data3: 0x11df, Data4: [8]byte{0x8c, 0x49, 0, 0x1e, 0x4f, 0xc6, 0x86, 0xda}}
)

type winObject struct{ vtable *[16]uintptr }

//go:uintptrescapes
func (o *winObject) call(slot int, args ...uintptr) error {
	if o == nil {
		return ErrUnavailable
	}
	params := append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)
	result, _, _ := syscall.SyscallN(o.vtable[slot], params...)
	return windowsHRESULT(result)
}

func (o *winObject) release() {
	if o != nil {
		syscall.SyscallN(o.vtable[2], uintptr(unsafe.Pointer(o)))
	}
}

func (o *winObject) query(iid *windows.GUID) (*winObject, error) {
	var result *winObject
	err := o.call(0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&result)))
	return result, err
}

func windowsHRESULT(result uintptr) error {
	code := uint32(result)
	if int32(code) >= 0 {
		return nil
	}
	if code == 0x800704c7 || code == 0x80090036 {
		return ErrCanceled
	}
	// Missing package identity, unavailable WinRT class, or unsupported operation
	// must fail closed. Desktop installer packaging may affect API availability.
	return fmt.Errorf("%w: Windows Hello operation failed (0x%08x)", ErrUnavailable, code)
}

func withWindowsRuntime(work func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	result, _, _ := winRoInitialize.Call(1) // RO_INIT_MULTITHREADED
	// An existing STA requires a message pump while waiting for async results.
	// Refuse that incompatible apartment rather than blocking its UI thread.
	if err := windowsHRESULT(result); err != nil {
		return err
	}
	defer winRoUninitialize.Call()
	return work()
}

func windowsString(value string) (uintptr, error) {
	utf16, err := windows.UTF16FromString(value)
	if err != nil {
		return 0, err
	}
	var handle uintptr
	result, _, _ := winCreateString.Call(uintptr(unsafe.Pointer(&utf16[0])), uintptr(len(utf16)-1), uintptr(unsafe.Pointer(&handle)))
	return handle, windowsHRESULT(result)
}

func windowsFactory(name string, iid *windows.GUID) (*winObject, error) {
	class, err := windowsString(name)
	if err != nil {
		return nil, err
	}
	defer winDeleteString.Call(class)
	var factory *winObject
	result, _, _ := winGetFactory.Call(class, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&factory)))
	return factory, windowsHRESULT(result)
}

func windowsWait(ctx context.Context, operation *winObject) error {
	if operation == nil {
		return ErrUnavailable
	}
	info, err := operation.query(&iidAsyncInfo)
	if err != nil {
		return err
	}
	defer info.release()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			// Cancel requests that Windows stop its native prompt. Even if Windows
			// finishes late, neither GetResults nor secret derivation is performed.
			return errors.Join(err, info.call(9))
		}
		var status int32
		if err := info.call(7, uintptr(unsafe.Pointer(&status))); err != nil {
			return err
		}
		switch status {
		case 1:
			return nil // Completed
		case 2:
			return ErrCanceled
		case 3:
			var code int32
			if err := info.call(8, uintptr(unsafe.Pointer(&code))); err != nil {
				return err
			}
			if err := windowsHRESULT(uintptr(uint32(code))); err != nil {
				return err
			}
			return ErrUnavailable
		case 0: // Started
		default:
			return ErrUnavailable
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}

func (nativeWindowsHello) Supported(ctx context.Context) (available bool, err error) {
	err = withWindowsRuntime(func() error {
		factory, err := windowsFactory("Windows.Security.Credentials.KeyCredentialManager", &iidCredentialManager)
		if err != nil {
			return err
		}
		defer factory.release()
		var operation *winObject
		if err := factory.call(6, uintptr(unsafe.Pointer(&operation))); err != nil {
			return err
		}
		defer operation.release()
		if err := windowsWait(ctx, operation); err != nil {
			return err
		}
		var supported byte // WinRT boolean is 8 bits, unlike Win32 BOOL.
		if err := operation.call(8, uintptr(unsafe.Pointer(&supported))); err != nil {
			return err
		}
		available = supported != 0
		return nil
	})
	return available, err
}

// retrieval opens or creates a credential, returning an owned interface.
// Creation uses ReplaceExisting for TDrive's account/drive-specific identifier.
func windowsCredential(ctx context.Context, name string, create bool) (*winObject, error) {
	factory, err := windowsFactory("Windows.Security.Credentials.KeyCredentialManager", &iidCredentialManager)
	if err != nil {
		return nil, err
	}
	defer factory.release()
	identifier, err := windowsString(name)
	if err != nil {
		return nil, err
	}
	defer winDeleteString.Call(identifier)
	var operation *winObject
	if create {
		err = factory.call(8, identifier, 0, uintptr(unsafe.Pointer(&operation)))
	} else {
		err = factory.call(9, identifier, uintptr(unsafe.Pointer(&operation)))
	}
	if err != nil {
		return nil, err
	}
	defer operation.release()
	if err := windowsWait(ctx, operation); err != nil {
		return nil, err
	}
	var result *winObject
	if err := operation.call(8, uintptr(unsafe.Pointer(&result))); err != nil {
		return nil, err
	}
	defer result.release()
	if err := windowsCredentialResultStatus(result); err != nil {
		return nil, err
	}
	var credential *winObject
	if err := result.call(6, uintptr(unsafe.Pointer(&credential))); err != nil {
		return nil, err
	}
	if credential == nil {
		return nil, ErrUnavailable
	}
	return credential, nil
}

func windowsCredentialResultStatus(result *winObject) error {
	var status int32
	if err := result.call(7, uintptr(unsafe.Pointer(&status))); err != nil {
		return err
	}
	switch status {
	case 0:
		return nil
	case 2:
		return ErrInvalidated
	case 3, 4:
		return ErrCanceled // UserCanceled, UserPrefersPassword
	default:
		return fmt.Errorf("%w: Windows Hello credential status %d", ErrUnavailable, status)
	}
}

func (nativeWindowsHello) Create(ctx context.Context, name string) error {
	return withWindowsRuntime(func() error {
		credential, err := windowsCredential(ctx, name, true)
		if err != nil {
			return err
		}
		defer credential.release()
		return nil
	})
}

func (nativeWindowsHello) Exists(ctx context.Context, name string) (exists bool, err error) {
	err = withWindowsRuntime(func() error {
		credential, err := windowsCredential(ctx, name, false)
		if errors.Is(err, ErrInvalidated) {
			return nil
		}
		if err != nil {
			return err
		}
		defer credential.release()
		exists = true
		return nil
	})
	return exists, err
}

func (nativeWindowsHello) Delete(ctx context.Context, name string) error {
	return withWindowsRuntime(func() error {
		credential, err := windowsCredential(ctx, name, false)
		if errors.Is(err, ErrInvalidated) {
			return nil
		}
		if err != nil {
			return err
		}
		credential.release()
		factory, err := windowsFactory("Windows.Security.Credentials.KeyCredentialManager", &iidCredentialManager)
		if err != nil {
			return err
		}
		defer factory.release()
		identifier, err := windowsString(name)
		if err != nil {
			return err
		}
		defer winDeleteString.Call(identifier)
		var operation *winObject
		if err := factory.call(10, identifier, uintptr(unsafe.Pointer(&operation))); err != nil {
			return err
		}
		defer operation.release()
		if err := windowsWait(ctx, operation); err != nil {
			return err
		}
		return operation.call(8) // IAsyncAction.GetResults
	})
}

func windowsBuffer(data []byte) (*winObject, error) {
	factory, err := windowsFactory("Windows.Security.Cryptography.CryptographicBuffer", &iidCryptoBuffer)
	if err != nil {
		return nil, err
	}
	defer factory.release()
	var buffer *winObject
	err = factory.call(9, uintptr(len(data)), uintptr(unsafe.Pointer(unsafe.SliceData(data))), uintptr(unsafe.Pointer(&buffer)))
	return buffer, err
}

func windowsBufferCopy(buffer *winObject, sensitive bool) ([]byte, error) {
	var length uint32
	if err := buffer.call(7, uintptr(unsafe.Pointer(&length))); err != nil {
		return nil, err
	}
	if length == 0 || length > 4096 {
		return nil, ErrInvalidated
	}
	access, err := buffer.query(&iidBufferBytes)
	if err != nil {
		return nil, err
	}
	defer access.release()
	var data *byte
	if err := access.call(3, uintptr(unsafe.Pointer(&data))); err != nil {
		return nil, err
	}
	if data == nil {
		return nil, ErrInvalidated
	}
	source := unsafe.Slice(data, int(length))
	result := bytes.Clone(source)
	if sensitive {
		clear(source)
	}
	return result, nil
}

func (nativeWindowsHello) Sign(ctx context.Context, name string, message []byte) (signature, publicKey []byte, err error) {
	err = withWindowsRuntime(func() error {
		credential, err := windowsCredential(ctx, name, false)
		if err != nil {
			return err
		}
		defer credential.release()
		var public *winObject
		if err := credential.call(7, uintptr(unsafe.Pointer(&public))); err != nil {
			return err
		}
		defer public.release()
		publicKey, err = windowsBufferCopy(public, false)
		if err != nil {
			return err
		}
		input, err := windowsBuffer(message)
		if err != nil {
			return err
		}
		defer input.release()
		var operation *winObject
		// The documented RequestSignAsync always requests a Windows Hello
		// gesture. No consent-verifier boolean or DPAPI fallback is involved.
		if err := credential.call(9, uintptr(unsafe.Pointer(input)), uintptr(unsafe.Pointer(&operation))); err != nil {
			return err
		}
		defer operation.release()
		if err := windowsWait(ctx, operation); err != nil {
			return err
		}
		var result *winObject
		if err := operation.call(8, uintptr(unsafe.Pointer(&result))); err != nil {
			return err
		}
		defer result.release()
		if err := windowsCredentialResultStatus(result); err != nil {
			return err
		}
		var output *winObject
		if err := result.call(6, uintptr(unsafe.Pointer(&output))); err != nil {
			return err
		}
		defer output.release()
		signature, err = windowsBufferCopy(output, true)
		return err
	})
	if err != nil {
		clear(signature)
		signature = nil
	}
	return signature, publicKey, err
}
