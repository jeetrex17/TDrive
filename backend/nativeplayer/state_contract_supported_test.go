//go:build darwin || windows || (linux && cgo)

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
