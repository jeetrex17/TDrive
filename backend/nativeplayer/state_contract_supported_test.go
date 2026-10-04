//go:build (darwin && !ios) || windows || (linux && cgo && !android)

package nativeplayer

import (
	"reflect"
	"testing"
)

func TestTerminalStateSequenceKeepsTheFirstTerminalState(t *testing.T) {
	statuses := make([]PlaybackStatus, 0, 1)
	player := &Player{onState: func(state State) {
		statuses = append(statuses, state.Status)
	}}

	player.emitTerminal(StatusFailed)
	player.emitTerminal(StatusFailed)
	player.emitTerminal(StatusClosed)
	player.emitTerminal(StatusClosed)

	want := []PlaybackStatus{StatusFailed}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("terminal status sequence = %v, want %v", statuses, want)
	}

	statuses = statuses[:0]
	player = &Player{onState: func(state State) {
		statuses = append(statuses, state.Status)
	}}
	player.emitTerminal(StatusClosed)
	player.emitTerminal(StatusFailed)
	if want := []PlaybackStatus{StatusClosed}; !reflect.DeepEqual(statuses, want) {
		t.Fatalf("closed-first terminal status sequence = %v, want %v", statuses, want)
	}
}

func TestTerminalStateSuppressesLaterPlayback(t *testing.T) {
	for _, status := range []PlaybackStatus{StatusFailed, StatusClosed} {
		t.Run(string(status), func(t *testing.T) {
			var states []State
			player := &Player{}
			player.onState = func(state State) {
				// Callbacks can inspect player state without deadlocking its lock.
				player.mu.Lock()
				last := player.lastState
				player.mu.Unlock()
				if last.Status != state.Status {
					t.Errorf("stored status = %q, callback = %q", last.Status, state.Status)
				}
				states = append(states, state)
			}
			player.emitTerminal(status)
			player.publishState(State{Paused: true})
			if len(states) != 1 || states[0].Status != status {
				t.Fatalf("states = %+v, want only %s", states, status)
			}
		})
	}
}
