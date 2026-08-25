package encryption

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestStoreMasterKeyOwnsInputAndLoadedMasterKeyReturnsCopy(t *testing.T) {
	svc, _ := newTestService(t)
	input := bytes.Repeat([]byte{0x11}, 32)
	want := append([]byte(nil), input...)

	svc.StoreMasterKey(input)
	clear(input)

	loaded, ok := svc.LoadedMasterKey()
	if !ok || !bytes.Equal(loaded, want) {
		t.Fatalf("loaded key = %x, %t; want an owned copy of %x", loaded, ok, want)
	}
	clear(loaded)
	reloaded, ok := svc.LoadedMasterKey()
	if !ok || !bytes.Equal(reloaded, want) {
		t.Fatalf("reloaded key = %x, %t; caller mutated service storage", reloaded, ok)
	}
}

func TestStoreMasterKeyZeroesReplacedOwnedBuffer(t *testing.T) {
	svc, _ := newTestService(t)
	svc.StoreMasterKey(bytes.Repeat([]byte{0x22}, 32))

	svc.masterKeyMu.RLock()
	replaced := svc.masterKey
	svc.masterKeyMu.RUnlock()
	svc.StoreMasterKey(bytes.Repeat([]byte{0x33}, 32))

	assertZeroed(t, replaced)
}

func TestStoreMasterKeyRejectsInvalidLengthsAndLocksTheVault(t *testing.T) {
	for _, size := range []int{0, 31, 33, 1 << 20} {
		t.Run(fmt.Sprintf("size-%d", size), func(t *testing.T) {
			svc, _ := newTestService(t)
			svc.StoreMasterKey(bytes.Repeat([]byte{0x22}, 32))
			svc.masterKeyMu.RLock()
			previous := svc.masterKey
			svc.masterKeyMu.RUnlock()

			err := svc.StoreMasterKey(bytes.Repeat([]byte{0x33}, size))

			assertZeroed(t, previous)
			if !errors.Is(err, ErrInvalidMasterKey) {
				t.Fatalf("StoreMasterKey error = %v, want ErrInvalidMasterKey", err)
			}
			if key, ok := svc.LoadedMasterKey(); ok || key != nil {
				t.Fatalf("LoadedMasterKey = %x, %t; invalid replacement must lock the vault", key, ok)
			}
		})
	}
}

func TestClearZeroesOwnedBuffer(t *testing.T) {
	svc, _ := newTestService(t)
	svc.StoreMasterKey(bytes.Repeat([]byte{0x44}, 32))

	svc.masterKeyMu.RLock()
	cleared := svc.masterKey
	svc.masterKeyMu.RUnlock()
	svc.Clear()

	assertZeroed(t, cleared)
	if key, ok := svc.LoadedMasterKey(); ok || key != nil {
		t.Fatalf("LoadedMasterKey after Clear = %x, %t; want nil, false", key, ok)
	}
}

func TestAcquireMasterKeyLeaseRequiresConfiguredUnlockedVault(t *testing.T) {
	t.Run("not configured", func(t *testing.T) {
		svc, _ := newTestService(t)
		svc.StoreMasterKey(bytes.Repeat([]byte{0x55}, 32))

		lease, err := svc.AcquireMasterKeyLease()
		if err == nil || lease != nil {
			t.Fatalf("AcquireMasterKeyLease = %#v, %v; want configuration error", lease, err)
		}
	})

	t.Run("locked", func(t *testing.T) {
		svc, _ := newTestService(t)
		if err := svc.CreatePassword("password-1", ""); err != nil {
			t.Fatalf("CreatePassword: %v", err)
		}
		svc.Clear()

		lease, err := svc.AcquireMasterKeyLease()
		if !errors.Is(err, ErrPasswordRequired) || lease != nil {
			t.Fatalf("AcquireMasterKeyLease = %#v, %v; want ErrPasswordRequired", lease, err)
		}
	})
}

func TestAcquireMasterKeyLeaseRejectsCorruptInMemoryKeyLength(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.CreatePassword("password-1", ""); err != nil {
		t.Fatalf("CreatePassword: %v", err)
	}
	svc.masterKeyMu.Lock()
	zeroBytes(svc.masterKey)
	svc.masterKey = bytes.Repeat([]byte{0x44}, 31)
	svc.masterKeyMu.Unlock()

	lease, err := svc.AcquireMasterKeyLease()
	if lease != nil || !errors.Is(err, ErrInvalidMasterKey) {
		t.Fatalf("AcquireMasterKeyLease = %#v, %v; want ErrInvalidMasterKey", lease, err)
	}
}

func TestMasterKeyLeaseOwnsIndependentKeyAndReturnsCopies(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.CreatePassword("password-1", ""); err != nil {
		t.Fatalf("CreatePassword: %v", err)
	}
	want, ok := svc.LoadedMasterKey()
	if !ok {
		t.Fatal("master key was not loaded")
	}

	lease, err := svc.AcquireMasterKeyLease()
	if err != nil {
		t.Fatalf("AcquireMasterKeyLease: %v", err)
	}
	svc.StoreMasterKey(bytes.Repeat([]byte{0x66}, len(want)))
	svc.Clear()

	leased, err := lease.Key()
	if err != nil || !bytes.Equal(leased, want) {
		t.Fatalf("lease Key = %x, %v; want independent %x", leased, err, want)
	}
	clear(leased)
	leasedAgain, err := lease.Key()
	if err != nil || !bytes.Equal(leasedAgain, want) {
		t.Fatalf("second lease Key = %x, %v; caller mutated lease storage", leasedAgain, err)
	}
}

func TestMasterKeyLeaseCloseIsIdempotentAndZeroesKey(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.CreatePassword("password-1", ""); err != nil {
		t.Fatalf("CreatePassword: %v", err)
	}
	lease, err := svc.AcquireMasterKeyLease()
	if err != nil {
		t.Fatalf("AcquireMasterKeyLease: %v", err)
	}

	lease.keyMu.RLock()
	owned := lease.key
	lease.keyMu.RUnlock()
	const closers = 8
	var wg sync.WaitGroup
	wg.Add(closers)
	for range closers {
		go func() {
			defer wg.Done()
			lease.Close()
		}()
	}
	wg.Wait()
	lease.Close()

	assertZeroed(t, owned)
	if key, err := lease.Key(); !errors.Is(err, ErrMasterKeyLeaseClosed) || key != nil {
		t.Fatalf("Key after Close = %x, %v; want ErrMasterKeyLeaseClosed", key, err)
	}
}

func TestWrongPasswordDoesNotReplaceLoadedMasterKey(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.CreatePassword("password-1", ""); err != nil {
		t.Fatalf("CreatePassword: %v", err)
	}
	want, ok := svc.LoadedMasterKey()
	if !ok {
		t.Fatal("master key was not loaded")
	}

	if err := svc.UsePassword("definitely-wrong"); err == nil {
		t.Fatal("UsePassword with wrong password unexpectedly succeeded")
	}
	got, ok := svc.LoadedMasterKey()
	if !ok || !bytes.Equal(got, want) {
		t.Fatalf("loaded key after wrong password = %x, %t; want unchanged %x", got, ok, want)
	}
}

func TestLoadedMasterKeyAndClearAreConcurrentSafe(t *testing.T) {
	svc, _ := newTestService(t)
	want := bytes.Repeat([]byte{0x77}, 32)
	svc.StoreMasterKey(want)

	const iterations = 1_000
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for range iterations {
			svc.Clear()
			svc.StoreMasterKey(want)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for range iterations {
			key, ok := svc.LoadedMasterKey()
			if ok && !bytes.Equal(key, want) {
				t.Errorf("observed partial key %x", key)
				return
			}
		}
	}()
	close(start)
	wg.Wait()
}

func assertZeroed(t *testing.T, key []byte) {
	t.Helper()
	if !bytes.Equal(key, make([]byte, len(key))) {
		t.Fatalf("key buffer was not zeroed: %x", key)
	}
}
