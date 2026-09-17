package file

import (
	"context"
	"sync"
	"time"
)

const maxRenditionFlights = 128

// renditionFlightGroup shares only active work. Subscribers own cancellation:
// dropping the last subscriber immediately cancels the transfer and removes
// its identity, so a fresh request never joins an already canceled flight.
type renditionFlightGroup struct {
	mu      sync.Mutex
	flights map[string]*renditionFlight
}
type renditionFlight struct {
	done        chan struct{}
	cancel      context.CancelFunc
	subscribers int
	result      Rendition
	err         error
}

func (g *renditionFlightGroup) do(ctx context.Context, key string, load func(context.Context) (Rendition, error)) (Rendition, error) {
	if err := ctx.Err(); err != nil {
		return Rendition{}, err
	}
	g.mu.Lock()
	if g.flights == nil {
		g.flights = make(map[string]*renditionFlight)
	}
	f := g.flights[key]
	if f == nil {
		if len(g.flights) >= maxRenditionFlights {
			g.mu.Unlock()
			return Rendition{}, ErrRenditionBusy
		}
		// Telegram's bounded FloodWait policy governs actual waits; this outer
		// deadline only prevents broken transports from retaining a flight forever.
		flightCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		f = &renditionFlight{done: make(chan struct{}), cancel: cancel}
		g.flights[key] = f
		go func() {
			result, err := load(flightCtx)
			g.mu.Lock()
			f.result = result
			f.err = err
			if g.flights[key] == f {
				delete(g.flights, key)
			}
			close(f.done)
			g.mu.Unlock()
			cancel()
		}()
	}
	f.subscribers++
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		f.subscribers--
		if f.subscribers == 0 {
			if g.flights[key] == f {
				delete(g.flights, key)
			}
			f.cancel()
		}
		g.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return Rendition{}, ctx.Err()
	case <-f.done:
		if err := ctx.Err(); err != nil {
			return Rendition{}, err
		}
		return f.result, f.err
	}
}
