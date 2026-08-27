package tgclient

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// writeClass separates logical message creation from upload-part traffic.
// Telegram can accept file parts concurrently, while serializing message
// creation prevents a large import from bursting thousands of channel writes.
type writeClass uint8

const (
	writeClassMessage writeClass = iota + 1
	writeClassUploadPart
)

// writeCoordinator owns process-wide backpressure for one Telegram client.
// It does not invent a client-side rate: Telegram's longest observed
// FLOOD_WAIT is the source of truth for the shared cooldown.
type writeCoordinator struct {
	now   func() time.Time
	sleep func(context.Context, time.Duration) error

	messageSlot chan struct{}

	mu           sync.Mutex
	blockedUntil time.Time
}

func newWriteCoordinator(now func() time.Time, sleep func(context.Context, time.Duration) error) *writeCoordinator {
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = sleepContext
	}
	return &writeCoordinator{
		now:         now,
		sleep:       sleep,
		messageSlot: make(chan struct{}, 1),
	}
}

// Do waits for the shared Telegram cooldown, executes one write, then records
// any server-directed FLOOD_WAIT for every subsequent write class. Message
// writes are serialized; upload parts remain concurrent.
func (c *writeCoordinator) Do(ctx context.Context, class writeClass, action func() error) error {
	if ctx == nil {
		return fmt.Errorf("tgclient: write coordinator requires a context")
	}
	if action == nil {
		return fmt.Errorf("tgclient: write coordinator requires an action")
	}
	if class != writeClassMessage && class != writeClassUploadPart {
		return fmt.Errorf("tgclient: unknown write class %d", class)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	if class == writeClassMessage {
		select {
		case c.messageSlot <- struct{}{}:
			defer func() { <-c.messageSlot }()
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Recheck after acquiring the message slot: a concurrent upload part or
	// the preceding message may have extended the cooldown while this call was
	// queued.
	if err := c.waitForCooldown(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	err := action()
	c.observe(err)
	return err
}

func (c *writeCoordinator) waitForCooldown(ctx context.Context) error {
	for {
		c.mu.Lock()
		wait := c.blockedUntil.Sub(c.now())
		c.mu.Unlock()
		if wait <= 0 {
			return nil
		}
		if err := c.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

func (c *writeCoordinator) observe(err error) {
	wait, ok := FloodWaitDuration(err)
	if !ok || wait < 0 {
		return
	}
	deadline := c.now().Add(wait)
	c.mu.Lock()
	if deadline.After(c.blockedUntil) {
		c.blockedUntil = deadline
	}
	c.mu.Unlock()
}
