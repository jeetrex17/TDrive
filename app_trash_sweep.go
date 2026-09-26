package main

import (
	"fmt"
	"time"
)

// The retention sweep is the half of the trash model nobody sees. It is what
// makes "deleted" eventually mean the bytes left Telegram, instead of a promise
// that only pays off if somebody happens to open the Trash panel.
//
// It runs on its own ticker rather than off a bound call because the work is
// slow (Telegram round trips), destructive, and best effort -- none of which
// belongs on a path a human is waiting on. Listing the trash stays a pure read.

const (
	// trashSweepInterval is how often the sweep looks for expired entries.
	// Retention is 30 days, so being a few hours late is invisible; the
	// interval only has to be short enough that a machine left running
	// reclaims storage the same day, and long enough that an idle app is not
	// waking up to query SQLite every few minutes.
	trashSweepInterval = 6 * time.Hour
	// trashSweepStartupDelay is when the first pass runs. A desktop session
	// shorter than the interval would otherwise never purge anything, but
	// sweeping the instant the process starts would almost always find no
	// signed-in account, no selected drive and no Telegram connection. This is
	// long enough for startup to settle and short enough that even a brief
	// session gets one pass.
	trashSweepStartupDelay = 2 * time.Minute
)

// startTrashSweep launches the background sweep. It is safe to call once per
// process; ServiceShutdown stops it through the same channel.
func (a *App) startTrashSweep() {
	a.trashSweepStop = make(chan struct{})
	startup := time.NewTimer(trashSweepStartupDelay)
	ticker := time.NewTicker(trashSweepInterval)
	go func() {
		defer startup.Stop()
		defer ticker.Stop()
		runTrashSweeps(a.trashSweepStop, startup.C, ticker.C, a.sweepExpiredTrash)
	}()
}

func (a *App) stopTrashSweep() {
	if a.trashSweepStop == nil {
		return
	}
	close(a.trashSweepStop)
	a.trashSweepStop = nil
}

// runTrashSweeps runs one startup pass and then one pass per tick until stop is
// closed. Passes run inline on this one goroutine, so they cannot overlap each
// other however long one takes: a slow pass simply delays the next tick, which
// a ticker drops rather than queues. pass is a parameter so the schedule itself
// is testable without a Telegram client behind it.
func runTrashSweeps(stop <-chan struct{}, startup, tick <-chan time.Time, pass func()) {
	for {
		select {
		case <-stop:
			return
		case <-startup:
			pass()
		case <-tick:
			pass()
		}
	}
}

// sweepExpiredTrash purges every entry whose retention window has closed. It
// destroys user data, so the decision is not taken here: PurgeExpiredTrash
// passes this instant down to the projection, which refuses any entry whose
// purge_after it has not reached. Failures are logged and dropped -- an entry
// that cannot be purged now stays in the trash for the next pass rather than
// stopping the ones behind it.
func (a *App) sweepExpiredTrash() {
	if !a.authReady.Load() {
		return
	}
	svc, err := a.requireFileService()
	if err != nil {
		return
	}
	channelID := a.ActiveChannelID()
	if channelID == 0 {
		return
	}
	if err := svc.PurgeExpiredTrash(a.ctx, channelID, time.Now().Unix()); err != nil {
		fmt.Printf("warn: expired trash sweep failed: %v\n", err)
	}
}
