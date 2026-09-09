package core

import "sync"

// projectionChangeBroker isolates projection producers from mounted
// filesystem consumers. Notifications are copied under lock and delivered
// outside it so subscribers cannot block registration or removal.
type projectionChangeBroker struct {
	mu        sync.RWMutex
	nextID    uint64
	listeners map[uint64]func(int64)
}

func (broker *projectionChangeBroker) subscribe(listener func(int64)) func() {
	if broker == nil || listener == nil {
		return func() {}
	}
	broker.mu.Lock()
	if broker.listeners == nil {
		broker.listeners = make(map[uint64]func(int64))
	}
	broker.nextID++
	id := broker.nextID
	broker.listeners[id] = listener
	broker.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			broker.mu.Lock()
			delete(broker.listeners, id)
			broker.mu.Unlock()
		})
	}
}

func (broker *projectionChangeBroker) listenersSnapshot() []func(int64) {
	if broker == nil {
		return nil
	}
	broker.mu.RLock()
	listeners := make([]func(int64), 0, len(broker.listeners))
	for _, listener := range broker.listeners {
		listeners = append(listeners, listener)
	}
	broker.mu.RUnlock()
	return listeners
}

func (broker *projectionChangeBroker) close() {
	if broker == nil {
		return
	}
	broker.mu.Lock()
	broker.listeners = nil
	broker.mu.Unlock()
}

// SubscribeProjectionChanges registers a process-local observer for completed
// projection syncs. The returned function is safe to call more than once.
func (e *Engine) SubscribeProjectionChanges(listener func(channelID int64)) func() {
	if e == nil {
		return func() {}
	}
	return e.projectionChanges.subscribe(listener)
}

func (e *Engine) notifyProjectionChanged(channelID int64) {
	if e == nil || channelID <= 0 {
		return
	}
	for _, listener := range e.projectionChanges.listenersSnapshot() {
		e.notifyProjectionChangeListener(listener, channelID)
	}
}

func (e *Engine) notifyProjectionChangeListener(listener func(int64), channelID int64) {
	defer func() {
		if recovered := recover(); recovered != nil && e.warnf != nil {
			e.warnf("Warning: projection change subscriber failed: %v\n", recovered)
		}
	}()
	listener(channelID)
}
