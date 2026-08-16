package harness

import (
	"context"
	"sync"
)

// FakeHarness is a scriptable Harness for runner/engine tests that need no
// real backend. Set NameVal/Caps/Sess/OpenErr directly before use.
type FakeHarness struct {
	NameVal string
	Caps    CapabilitySet
	Sess    *FakeSession
	OpenErr error

	// OpenSpec records the SessionSpec passed to the most recent Open call, so
	// tests can assert which capability-gated fields the executor set.
	OpenSpec SessionSpec
}

func (h *FakeHarness) Name() string { return h.NameVal }

func (h *FakeHarness) Capabilities() CapabilitySet { return h.Caps }

func (h *FakeHarness) Open(_ context.Context, spec SessionSpec) (Session, error) {
	h.OpenSpec = spec
	if h.OpenErr != nil {
		return nil, h.OpenErr
	}
	return h.Sess, nil
}

// FakeSession is a scriptable Session: Messages() replays a fixed slice of
// Events (closing the channel once exhausted), and Send/Close calls are
// recorded for assertions rather than forwarded anywhere.
type FakeSession struct {
	mu       sync.Mutex
	events   chan Event
	Sent     []ToolResult
	Closed   bool
	SendErr  error
	CloseErr error
}

// NewFakeSession returns a FakeSession whose Messages() channel replays evts
// in order, then closes — enough to drive a scripted turn in a test without a
// live backend.
func NewFakeSession(evts []Event) *FakeSession {
	ch := make(chan Event, len(evts))
	for _, e := range evts {
		ch <- e
	}
	close(ch)
	return &FakeSession{events: ch}
}

func (s *FakeSession) Messages() <-chan Event { return s.events }

func (s *FakeSession) Send(_ context.Context, result ToolResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.SendErr != nil {
		return s.SendErr
	}
	s.Sent = append(s.Sent, result)
	return nil
}

func (s *FakeSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Closed = true
	return s.CloseErr
}

var (
	_ Harness = (*FakeHarness)(nil)
	_ Session = (*FakeSession)(nil)
)
