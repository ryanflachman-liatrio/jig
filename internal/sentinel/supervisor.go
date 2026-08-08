package sentinel

import (
	"context"
	"strings"
	"sync"
	"time"

	"jig/internal/transcript"
)

// BatchSize is the number of StepSignals that trigger an immediate flush for a
// step without waiting for the debounce timer. Exported so tests can size their
// signal bursts exactly.
const BatchSize = 5

// DebounceInterval is the maximum time a step's pending signals may wait before
// a time-based flush fires.
const DebounceInterval = 500 * time.Millisecond

// entryCountCap is the maximum number of transcript entries in one bounded window.
const entryCountCap = 20

// tokenCeiling is the estimated maximum bytes per window (8 000 tokens × 4 B).
const tokenCeiling = 8000 * 4

// StepSignal carries the minimal liveness data the supervisor needs from the
// engine bus. Callers bridge engine.StepMessage → StepSignal before forwarding.
// Defining this type in sentinel (not engine) keeps sentinel free of engine imports,
// avoiding an import cycle (engine already imports sentinel for StepRequest.Guard).
type StepSignal struct {
	RunID     string
	StepID    string
	Seq       int
	Iteration int
}

// MonitorResult is the structured output from one monitor agent invocation.
type MonitorResult struct {
	Flagged  bool
	Severity string  // "low" | "medium" | "high" | "critical"
	Detail   string  // human-readable finding description
	CostUSD  float64 // actual cost of this invocation (may be 0 if unreported)
}

// MonitorDispatcher runs one monitor invocation against a transcript window.
// runner.AgentExecutor (wrapped in a thin adapter) implements this interface;
// tests use a stub. Defining the interface here keeps the supervisor decoupled
// from the runner package.
type MonitorDispatcher interface {
	Dispatch(ctx context.Context, monitorFile, windowText string) (MonitorResult, error)
}

// MonitorDef pairs a monitor agent file path with its dispatcher and a stable
// name for finding records.
type MonitorDef struct {
	File       string // path to the monitor agent .md file
	Monitor    string // finding monitor name (e.g. "prompt-injection")
	Dispatcher MonitorDispatcher
}

// Supervisor subscribes to liveness signals (StepSignal) from the engine bus and
// dispatches Tier-2 monitor agents out-of-band without blocking the observed run.
// It batches signals per step, reads transcript windows, deduplicates findings by
// fingerprint, and enforces a per-run USD budget.
type Supervisor struct {
	runID          string
	signals        <-chan StepSignal
	sink           *Writer // nil = findings persistence off
	monitors       []MonitorDef
	budget         float64                    // per-run USD ceiling; ≤0 = unlimited
	transcriptPath func(stepID string) string // "" → persistence off for that step

	mu        sync.Mutex
	spentUSD  float64
	degraded  bool
	cursors   map[string]int             // stepID → entries consumed (0-based offset for next Window call)
	seenFPs   map[string]map[string]bool // stepID → fingerprint → reported
	pending   map[string]int             // stepID → buffered signal count
	lastFlush map[string]time.Time
}

// NewSupervisor creates a Supervisor ready to Run. Pass a nil sink to disable
// findings persistence. The supervisor does not start automatically; call Run.
// It returns immediately from Run when no monitors are configured.
func NewSupervisor(
	runID string,
	signals <-chan StepSignal,
	sink *Writer,
	monitors []MonitorDef,
	budgetUSD float64,
	transcriptPath func(stepID string) string,
) *Supervisor {
	return &Supervisor{
		runID:          runID,
		signals:        signals,
		sink:           sink,
		monitors:       monitors,
		budget:         budgetUSD,
		transcriptPath: transcriptPath,
		cursors:        make(map[string]int),
		seenFPs:        make(map[string]map[string]bool),
		pending:        make(map[string]int),
		lastFlush:      make(map[string]time.Time),
	}
}

// Run starts the supervisor's event loop. It blocks until ctx is cancelled or
// the signals channel is closed. Run is typically called in a goroutine.
func (s *Supervisor) Run(ctx context.Context) {
	if len(s.monitors) == 0 {
		return
	}
	ticker := time.NewTicker(DebounceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case sig, ok := <-s.signals:
			if !ok {
				return
			}
			s.mu.Lock()
			s.pending[sig.StepID]++
			count := s.pending[sig.StepID]
			s.mu.Unlock()
			if count >= BatchSize {
				s.flushStep(ctx, sig.StepID)
			}
		case <-ticker.C:
			s.mu.Lock()
			var due []string
			for stepID, n := range s.pending {
				if n > 0 && time.Since(s.lastFlush[stepID]) >= DebounceInterval {
					due = append(due, stepID)
				}
			}
			s.mu.Unlock()
			for _, stepID := range due {
				s.flushStep(ctx, stepID)
			}
		}
	}
}

// flushStep reads new transcript entries for stepID since the last cursor,
// assembles the dual-bounded window, deduplicates, and dispatches monitors.
func (s *Supervisor) flushStep(ctx context.Context, stepID string) {
	s.mu.Lock()
	s.pending[stepID] = 0
	s.lastFlush[stepID] = time.Now()
	cursor := s.cursors[stepID]
	degraded := s.degraded
	s.mu.Unlock()

	if degraded {
		return
	}

	tPath := s.transcriptPath(stepID)
	if tPath == "" {
		return
	}
	r, err := transcript.Open(tPath)
	if err != nil {
		return
	}

	entries, err := r.Window(cursor, 0) // 0 = no cap: all entries from cursor onward
	if err != nil || len(entries) == 0 {
		return
	}

	s.mu.Lock()
	s.cursors[stepID] = cursor + len(entries)
	if s.seenFPs[stepID] == nil {
		s.seenFPs[stepID] = make(map[string]bool)
	}
	s.mu.Unlock()

	window := boundWindow(entries)
	if len(window) == 0 {
		return
	}
	windowText := renderWindow(window)

	for _, mon := range s.monitors {
		if ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		if s.degraded {
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()

		result, err := mon.Dispatcher.Dispatch(ctx, mon.File, windowText)
		if err != nil {
			continue
		}

		s.mu.Lock()
		if result.CostUSD > 0 {
			s.spentUSD += result.CostUSD
		}
		overBudget := s.budget > 0 && s.spentUSD >= s.budget
		s.mu.Unlock()

		if result.Flagged {
			sev := Severity(result.Severity)
			if sev == "" {
				sev = SeverityMedium
			}
			fp := NewFingerprint(stepID, mon.Monitor, result.Detail)

			s.mu.Lock()
			alreadySeen := s.seenFPs[stepID][fp]
			s.mu.Unlock()

			if !alreadySeen {
				f := Finding{
					Ts:          time.Now().UTC(),
					RunID:       s.runID,
					StepID:      stepID,
					Tier:        TierMonitor,
					Monitor:     mon.Monitor,
					Severity:    sev,
					Action:      ActionObserved,
					Detail:      result.Detail,
					Evidence:    "transcript-window",
					Fingerprint: fp,
				}
				if s.sink != nil {
					_ = s.sink.Append(f)
				}
				s.mu.Lock()
				s.seenFPs[stepID][fp] = true
				s.mu.Unlock()
			}
		}

		if overBudget {
			s.egressDegrade()
			return
		}
	}
}

// egressDegrade marks the supervisor as budget-exhausted and appends exactly
// one degraded-to-tier1 finding. Subsequent calls are no-ops.
func (s *Supervisor) egressDegrade() {
	s.mu.Lock()
	if s.degraded {
		s.mu.Unlock()
		return
	}
	s.degraded = true
	s.mu.Unlock()

	f := Finding{
		Ts:          time.Now().UTC(),
		RunID:       s.runID,
		Tier:        TierMonitor,
		Monitor:     "budget-exhausted",
		Severity:    SeverityLow,
		Action:      ActionObserved,
		Detail:      "Tier-2 fleet budget exhausted; degraded to Tier-1 only",
		Fingerprint: NewFingerprint(s.runID, "budget-exhausted", "singleton"),
	}
	if s.sink != nil {
		_ = s.sink.Append(f)
	}
}

// boundWindow truncates entries to the dual bound: at most entryCountCap entries
// and total estimated text ≤ tokenCeiling bytes. Returns the most-recent entries
// that fit, oldest-first, without splitting any single entry. At least one entry
// is always returned when the input is non-empty.
func boundWindow(entries []transcript.Entry) []transcript.Entry {
	if len(entries) == 0 {
		return nil
	}
	if len(entries) > entryCountCap {
		entries = entries[len(entries)-entryCountCap:]
	}
	// Walk from the most-recent entry backward, accumulating size. Always include
	// at least the last entry even if it exceeds the ceiling on its own.
	var total int
	start := len(entries) - 1
	for i := len(entries) - 1; i >= 0; i-- {
		size := entryByteSize(entries[i])
		if total > 0 && total+size > tokenCeiling {
			break
		}
		total += size
		start = i
	}
	return entries[start:]
}

// entryByteSize estimates the text payload of an entry in bytes (used as a
// token-count proxy at 4 bytes/token).
func entryByteSize(e transcript.Entry) int {
	n := 0
	for _, b := range e.Blocks {
		n += len(b.Text) + len(b.Content) + len(b.Input)
	}
	return n
}

// renderWindow serializes a transcript window to plain text for a monitor prompt.
func renderWindow(entries []transcript.Entry) string {
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString("[")
		sb.WriteString(string(e.Role))
		sb.WriteString("]\n")
		for _, b := range e.Blocks {
			switch b.Type {
			case transcript.BlockText, transcript.BlockThinking:
				sb.WriteString(b.Text)
			case transcript.BlockToolUse:
				sb.WriteString("<tool_use name=\"")
				sb.WriteString(b.Name)
				sb.WriteString("\">\n")
				sb.Write(b.Input)
				sb.WriteString("\n</tool_use>")
			case transcript.BlockToolResult:
				sb.WriteString("<tool_result>\n")
				sb.WriteString(b.Content)
				sb.WriteString("\n</tool_result>")
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}
