//go:build (darwin && !ios) || windows || (linux && cgo && !android)

package nativeplayer

// State publication shares the player lock with platform lifecycle changes.
// Invoke callbacks after unlocking so handlers may call back into the player.
func (p *Player) publishState(state State) {
	state = normalizeState(state)
	p.mu.Lock()
	if p.closed || p.terminal {
		p.mu.Unlock()
		return
	}
	p.lastState = state
	onState := p.onState
	p.mu.Unlock()
	if onState != nil {
		onState(state)
	}
}

func (p *Player) emitTerminal(status PlaybackStatus) {
	state := terminalState(status)
	p.mu.Lock()
	if p.terminal {
		p.mu.Unlock()
		return
	}
	p.terminal = true
	p.lastState = state
	onState := p.onState
	p.mu.Unlock()
	if onState != nil {
		onState(state)
	}
}
