package notification

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	// Bounded recent-history capacity for diagnostic entries. Older entries
	// roll off in FIFO order.
	MaxDiagnosticHistory = 100
)

// Diagnostic is one sanitized delivery record. It never carries raw
// destination URLs, bearer tokens, response bodies, or error strings —
// every source of external content is reduced to fixed reason codes.
type Diagnostic struct {
	Time      time.Time
	Alias     string
	RunID     string
	Event     string
	Outcome   string
	Reason    string
	Attempt   int
	Aggregate int
	NotifID   string
}

// DiagnosticSink is the non-blocking observer interface for diagnostic
// consumers. Every implementation MUST NOT block: use a channel with drop-
// on-full, or copy the fields into a bounded queue and return.
type DiagnosticSink interface {
	OnDiagnostic(d Diagnostic)
}

// DiagnosticStore is a bounded, thread-safe ring of recent diagnostics with
// identical-failure aggregation. It exposes a Snapshot for the TUI/headless
// consumer and a Publish helper that fans out to registered non-blocking
// sinks.
type DiagnosticStore struct {
	mu       sync.Mutex
	entries  []Diagnostic
	seen     map[string]int
	sinks    []DiagnosticSink
	overflow map[string]int
}

// NewDiagnosticStore constructs an empty store.
func NewDiagnosticStore() *DiagnosticStore {
	return &DiagnosticStore{
		seen:     make(map[string]int),
		overflow: make(map[string]int),
	}
}

// Attach registers a non-blocking diagnostic subscriber.
func (s *DiagnosticStore) Attach(sink DiagnosticSink) {
	if sink == nil {
		return
	}
	s.mu.Lock()
	s.sinks = append(s.sinks, sink)
	s.mu.Unlock()
}

// Snapshot returns a copy of the current diagnostics ordered oldest-first.
func (s *DiagnosticStore) Snapshot() []Diagnostic {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Diagnostic, len(s.entries))
	copy(out, s.entries)
	return out
}

// Record adds one diagnostic entry, aggregating identical repeated failures
// so the ring is not flooded by a stuck destination.
func (s *DiagnosticStore) Record(d Diagnostic) {
	sanitized := sanitizeDiagnostic(d)
	sinksSnapshot := s.appendLocked(sanitized)
	for _, sink := range sinksSnapshot {
		func() {
			defer func() { _ = recover() }()
			sink.OnDiagnostic(sanitized)
		}()
	}
}

// RecordOverflow accounts for a lossy ingestion boundary. It aggregates counts
// keyed by the loss reason so a burst produces one visible entry rather than
// one per rejected notification.
func (s *DiagnosticStore) RecordOverflow(reason string) {
	s.mu.Lock()
	s.overflow[reason]++
	count := s.overflow[reason]
	s.mu.Unlock()
	s.Record(Diagnostic{
		Time:      time.Now().UTC(),
		Outcome:   "dropped",
		Reason:    reason,
		Aggregate: count,
	})
}

// OverflowCounts returns a copy of the aggregate loss counts by reason.
func (s *DiagnosticStore) OverflowCounts() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.overflow))
	for k, v := range s.overflow {
		out[k] = v
	}
	return out
}

// Render prints a fixed textual dump suitable for stderr or a debug view.
func (s *DiagnosticStore) Render(indent string) string {
	entries := s.Snapshot()
	if len(entries) == 0 {
		return indent + "no notification diagnostics\n"
	}
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%s%s alias=%s run=%s event=%s outcome=%s reason=%s attempt=%d aggregate=%d\n",
			indent, e.Time.UTC().Format(time.RFC3339), safeString(e.Alias), safeString(e.RunID),
			safeString(e.Event), e.Outcome, e.Reason, e.Attempt, e.Aggregate)
	}
	return b.String()
}

func (s *DiagnosticStore) appendLocked(d Diagnostic) []DiagnosticSink {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := d.aggregateKey()
	if idx, ok := s.seen[key]; ok && idx < len(s.entries) {
		prior := s.entries[idx]
		prior.Aggregate++
		prior.Time = d.Time
		s.entries[idx] = prior
		out := make([]DiagnosticSink, len(s.sinks))
		copy(out, s.sinks)
		return out
	}
	entry := d
	if entry.Aggregate == 0 {
		entry.Aggregate = 1
	}
	if len(s.entries) >= MaxDiagnosticHistory {
		dropped := s.entries[0]
		s.entries = s.entries[1:]
		delete(s.seen, dropped.aggregateKey())
		for k := range s.seen {
			s.seen[k]--
		}
	}
	s.entries = append(s.entries, entry)
	s.seen[key] = len(s.entries) - 1
	out := make([]DiagnosticSink, len(s.sinks))
	copy(out, s.sinks)
	return out
}

func (d Diagnostic) aggregateKey() string {
	return d.Alias + "|" + d.Outcome + "|" + d.Reason + "|" + d.RunID + "|" + d.Event
}

func sanitizeDiagnostic(d Diagnostic) Diagnostic {
	d.Alias = safeString(d.Alias)
	d.RunID = safeString(d.RunID)
	d.Event = safeString(d.Event)
	d.Reason = safeString(d.Reason)
	d.Outcome = safeString(d.Outcome)
	d.NotifID = safeString(d.NotifID)
	if d.Time.IsZero() {
		d.Time = time.Now().UTC()
	}
	return d
}
