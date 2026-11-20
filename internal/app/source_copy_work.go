package app

import (
	"context"
	"errors"
	"sync"
)

// sourceCopyWork cancels and drains the Wails calls that can read the copy
// journal or publish to My Drive before the account database is torn down.
type sourceCopyWork struct {
	mu           sync.Mutex
	active       sync.WaitGroup
	suspensions  int
	lifetime     context.Context
	cancelWork   context.CancelFunc
	batchAccount int64
	cancelBatch  context.CancelFunc
}

func (w *sourceCopyWork) begin(ctx context.Context, batchAccount int64) (context.Context, func(), error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.suspensions > 0 {
		return nil, nil, errors.New("the Telegram session is closing")
	}
	if batchAccount != 0 && w.cancelBatch != nil {
		return nil, nil, errors.New("a My Drive save is already running")
	}
	if w.cancelWork == nil {
		w.lifetime, w.cancelWork = context.WithCancel(context.Background())
	}
	callCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(w.lifetime, cancel)
	if batchAccount != 0 {
		w.batchAccount, w.cancelBatch = batchAccount, cancel
	}
	w.active.Add(1)
	return callCtx, sync.OnceFunc(func() {
		stop()
		cancel()
		w.mu.Lock()
		defer w.mu.Unlock()
		if batchAccount != 0 {
			w.batchAccount, w.cancelBatch = 0, nil
		}
		w.active.Done()
	}), nil
}

func (w *sourceCopyWork) cancelBatchFor(accountID int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.batchAccount != accountID || w.cancelBatch == nil {
		return false
	}
	w.cancelBatch()
	return true
}

func (w *sourceCopyWork) suspend() func() {
	w.mu.Lock()
	w.suspensions++
	if w.cancelWork != nil {
		w.cancelWork()
		w.lifetime, w.cancelWork = nil, nil
	}
	w.mu.Unlock()
	w.active.Wait()
	return sync.OnceFunc(func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.suspensions--
	})
}
