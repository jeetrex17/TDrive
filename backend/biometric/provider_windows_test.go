//go:build windows

package biometric

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

// These synthetic COM objects exercise the real native syscall boundary on
// Windows, without prompting for biometrics or using personal credentials.
type fakeWindowsAsync struct {
	operation      winObject
	info           winObject
	operationTable [16]uintptr
	infoTable      [16]uintptr
	status         int32
	errorCode      int32
	cancelCalls    int
	infoReleases   int
}

func newFakeWindowsAsync(status int32) *fakeWindowsAsync {
	fake := &fakeWindowsAsync{status: status}
	fake.operation.vtable = &fake.operationTable
	fake.info.vtable = &fake.infoTable
	fake.operationTable[0] = windows.NewCallback(func(_ *winObject, iid *windows.GUID, result **winObject) uintptr {
		if *iid != iidAsyncInfo {
			return 0x80004002
		}
		*result = &fake.info
		return 0
	})
	fake.infoTable[2] = windows.NewCallback(func(_ *winObject) uintptr {
		fake.infoReleases++
		return 1
	})
	fake.infoTable[7] = windows.NewCallback(func(_ *winObject, result *int32) uintptr {
		*result = fake.status
		return 0
	})
	fake.infoTable[8] = windows.NewCallback(func(_ *winObject, result *int32) uintptr {
		*result = fake.errorCode
		return 0
	})
	fake.infoTable[9] = windows.NewCallback(func(_ *winObject) uintptr {
		fake.cancelCalls++
		return 0
	})
	return fake
}

func TestWindowsHelloNativeWaitMapsCompletionCancellationAndFailure(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		status    int32
		errorCode int32
		want      error
	}{
		{name: "completed", status: 1},
		{name: "native canceled", status: 2, want: ErrCanceled},
		{name: "native error", status: 3, errorCode: -2147024891, want: ErrUnavailable},
		{name: "failed operation with absent error", status: 3, want: ErrUnavailable},
		{name: "invalid status", status: 4, want: ErrUnavailable},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fake := newFakeWindowsAsync(scenario.status)
			fake.errorCode = scenario.errorCode
			err := windowsWait(t.Context(), &fake.operation)
			if scenario.want == nil && err != nil || scenario.want != nil && !errors.Is(err, scenario.want) {
				t.Fatalf("native wait error = %v, want %v", err, scenario.want)
			}
			if fake.infoReleases != 1 {
				t.Fatalf("async-info references leaked: %d releases", fake.infoReleases)
			}
		})
	}
}

func TestWindowsHelloNativeWaitCancelsPendingOperation(t *testing.T) {
	fake := newFakeWindowsAsync(0)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := windowsWait(ctx, &fake.operation); !errors.Is(err, context.Canceled) {
		t.Fatalf("native wait ignored canceled context: %v", err)
	}
	if fake.cancelCalls != 1 || fake.infoReleases != 1 {
		t.Fatalf("canceled operation cancel=%d release=%d", fake.cancelCalls, fake.infoReleases)
	}
}

func TestWindowsHelloNativeHRESULTDoesNotAcceptCanceledOrFailedOperations(t *testing.T) {
	for _, code := range []uintptr{0x800704c7, 0x80090036} {
		if err := windowsHRESULT(code); !errors.Is(err, ErrCanceled) {
			t.Fatalf("cancellation code %x error = %v", code, err)
		}
	}
	for _, code := range []uintptr{0x80073d54, 0x80040154, 0x80004001} {
		if err := windowsHRESULT(code); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("unsupported code %x error = %v", code, err)
		}
	}
	if err := windowsHRESULT(1); err != nil {
		t.Fatalf("S_FALSE must succeed: %v", err)
	}
}
