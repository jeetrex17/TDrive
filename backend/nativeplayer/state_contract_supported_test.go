//go:build darwin || windows || (linux && cgo)

package nativeplayer

import (
	"reflect"
	"testing"
)

func TestTerminalStateSequenceReportsFailureThenExplicitCloseOnce(t *testing.T) {
	statuses := make([]PlaybackStatus, 0, 2)
	player := &Player{onState: func(state State) {
		statuses = append(statuses, state.Status)
	}}

	player.emitTerminal(StatusFailed)
	player.emitTerminal(StatusFailed)
	player.emitTerminal(StatusClosed)
	player.emitTerminal(StatusClosed)

	want := []PlaybackStatus{StatusFailed, StatusClosed}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("terminal status sequence = %v, want %v", statuses, want)
	}
}
