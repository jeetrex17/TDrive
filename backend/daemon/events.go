package daemon

type eventSink interface {
	Emit(name string, args ...any)
}

type multiEventSink []eventSink

func (m multiEventSink) Emit(name string, args ...any) {
	for _, sink := range m {
		if sink != nil {
			sink.Emit(name, args...)
		}
	}
}

func (s *Server) Emit(name string, args ...any) {
	if s == nil {
		return
	}
	event := Event{Name: name, Args: args}

	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	for ch := range s.eventSubs {
		select {
		case ch <- event:
		default:
			// Progress events are best-effort. Dropping an old frame is better
			// than blocking the upload/download path behind a slow terminal.
		}
	}
}

func (s *Server) subscribeEvents() chan Event {
	ch := make(chan Event, 128)
	s.eventMu.Lock()
	if s.eventSubs == nil {
		s.eventSubs = make(map[chan Event]struct{})
	}
	s.eventSubs[ch] = struct{}{}
	s.eventMu.Unlock()
	return ch
}

func (s *Server) unsubscribeEvents(ch chan Event) {
	s.eventMu.Lock()
	delete(s.eventSubs, ch)
	close(ch)
	s.eventMu.Unlock()
}
