package sentinel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"jig/internal/transcript"
)

const (
	BatchSize              = 5
	DebounceInterval       = 500 * time.Millisecond
	entryCountCap          = 20
	renderByteCap          = 32_000
	tokenCeiling           = renderByteCap
	defaultDispatchTimeout = 30 * time.Second
)

type ExecutionCoordinates struct {
	Generation int
	Iteration  int
	Attempt    int
}

type StepSignal struct {
	RunID  string
	StepID string
	Seq    int
	ExecutionCoordinates
	OutboundAllowlist []string
}

type MonitorSpec struct {
	Model  string
	Prompt string
}

type MonitorResult struct {
	Flagged   bool
	Severity  string
	Detail    string
	CostUSD   float64
	CostKnown bool
	Launched  bool
}

type MonitorDispatcher interface {
	Dispatch(context.Context, MonitorSpec, string) (MonitorResult, error)
}

// MonitorCircuit is shared by copied monitor definitions so a broken external
// classifier is not retried by every subsequent run in the same process.
type MonitorCircuit struct{ disabled atomic.Bool }

func (c *MonitorCircuit) disable()         { c.disabled.Store(true) }
func (c *MonitorCircuit) isDisabled() bool { return c != nil && c.disabled.Load() }

type MonitorDef struct {
	Spec       MonitorSpec
	Monitor    string
	Dispatcher MonitorDispatcher
	Circuit    *MonitorCircuit
}

type SupervisorOptions struct {
	BatchSize       int
	Debounce        time.Duration
	BudgetUSD       float64
	ConcurrencyCap  int
	DispatchTimeout time.Duration
	StatePath       string
	FindingsPath    string
	Resume          bool
}

func (o SupervisorOptions) normalized() SupervisorOptions {
	if o.BatchSize <= 0 {
		o.BatchSize = BatchSize
	}
	if o.Debounce <= 0 {
		o.Debounce = DebounceInterval
	}
	if o.DispatchTimeout <= 0 {
		o.DispatchTimeout = defaultDispatchTimeout
	}
	// A6 deliberately dispatches serially; ConcurrencyCap remains an upper
	// bound for a future worker pool and every valid value is therefore honored.
	return o
}

type executionKey struct {
	stepID string
	ExecutionCoordinates
}

type pendingWindow struct {
	count  int
	signal StepSignal
}

type Supervisor struct {
	runID          string
	signals        <-chan StepSignal
	sink           *Writer
	monitors       []MonitorDef
	transcriptPath func(string) string
	notify         func(Finding)
	options        SupervisorOptions

	mu              sync.Mutex
	state           monitorState
	pending         map[executionKey]pendingWindow
	lastFlush       map[executionKey]time.Time
	lastObserved    map[executionKey]int
	latestExecution map[string]ExecutionCoordinates
	seenFPs         map[string]bool
	disabled        map[string]bool
	health          map[string]bool
}

func NewSupervisor(runID string, signals <-chan StepSignal, sink *Writer, monitors []MonitorDef,
	transcriptPath func(string) string, notify func(Finding), options SupervisorOptions) *Supervisor {
	return &Supervisor{
		runID: runID, signals: signals, sink: sink, monitors: monitors,
		transcriptPath: transcriptPath, notify: notify, options: options.normalized(),
		state: newMonitorState(), pending: make(map[executionKey]pendingWindow),
		lastFlush: make(map[executionKey]time.Time), lastObserved: make(map[executionKey]int),
		latestExecution: make(map[string]ExecutionCoordinates),
		seenFPs:         make(map[string]bool), disabled: make(map[string]bool), health: make(map[string]bool),
	}
}

func (s *Supervisor) Run(ctx context.Context) {
	defer func() {
		if s.sink != nil {
			_ = s.sink.Close()
		}
	}()
	if len(s.monitors) == 0 {
		return
	}
	s.seedFingerprints()
	s.restoreAccounting()
	ticker := time.NewTicker(s.options.Debounce)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case sig, ok := <-s.signals:
			if !ok {
				return
			}
			if sig.RunID != s.runID || sig.StepID == "" {
				continue
			}
			key := executionKey{stepID: sig.StepID, ExecutionCoordinates: sig.ExecutionCoordinates}
			s.mu.Lock()
			if latest, exists := s.latestExecution[sig.StepID]; exists {
				order := compareExecution(sig.ExecutionCoordinates, latest)
				if order < 0 {
					s.mu.Unlock()
					continue
				}
				if order > 0 {
					for pendingKey := range s.pending {
						if pendingKey.stepID == sig.StepID {
							delete(s.pending, pendingKey)
						}
					}
				}
			}
			s.latestExecution[sig.StepID] = sig.ExecutionCoordinates
			p := s.pending[key]
			p.count++
			p.signal = sig
			s.pending[key] = p
			count := p.count
			s.mu.Unlock()
			if count >= s.options.BatchSize {
				s.flush(ctx, key)
			}
		case now := <-ticker.C:
			s.mu.Lock()
			var due []executionKey
			for key, p := range s.pending {
				if p.count > 0 && now.Sub(s.lastFlush[key]) >= s.options.Debounce {
					due = append(due, key)
				}
			}
			s.mu.Unlock()
			for _, key := range due {
				s.flush(ctx, key)
			}
		}
	}
}

func compareExecution(left, right ExecutionCoordinates) int {
	if left.Generation != right.Generation {
		if left.Generation < right.Generation {
			return -1
		}
		return 1
	}
	if left.Iteration != right.Iteration {
		if left.Iteration < right.Iteration {
			return -1
		}
		return 1
	}
	if left.Attempt != right.Attempt {
		if left.Attempt < right.Attempt {
			return -1
		}
		return 1
	}
	return 0
}

func (s *Supervisor) flush(ctx context.Context, key executionKey) {
	s.mu.Lock()
	p := s.pending[key]
	delete(s.pending, key)
	s.lastFlush[key] = time.Now()
	degraded := s.state.Degraded
	lastObserved := s.lastObserved[key]
	s.mu.Unlock()
	if p.count == 0 || degraded {
		return
	}
	path := s.transcriptPath(key.stepID)
	if path == "" {
		return
	}
	r, err := transcript.Open(path)
	if err != nil {
		s.healthFinding(key.stepID, "transcript-unavailable", "Tier-2 could not open the transcript")
		return
	}
	page, err := r.TailPage(entryCountCap)
	if err != nil {
		s.healthFinding(key.stepID, "transcript-unavailable", "Tier-2 could not read the transcript")
		return
	}
	entries := entriesForExecution(page.Entries, key.ExecutionCoordinates)
	if len(entries) == 0 {
		return
	}
	maxSeq := entries[len(entries)-1].Seq
	if p.signal.Seq <= lastObserved || maxSeq <= lastObserved {
		return
	}
	s.mu.Lock()
	s.lastObserved[key] = maxSeq
	s.mu.Unlock()
	base := renderWindow(entries)
	if base == "" {
		return
	}
	for i := range s.monitors {
		mon := &s.monitors[i]
		if ctx.Err() != nil {
			return
		}
		if s.monitorDisabled(mon) {
			s.healthFinding(key.stepID, "monitor-unavailable/"+mon.Monitor, "Tier-2 classifier is unavailable for this process")
			continue
		}
		if mon.Monitor == "stuck-loop" && !StuckLoopPrefilter(entries) {
			continue
		}
		if !s.withinBudget() {
			s.degradeBudget()
			return
		}
		input := combineRendered(trustedContext(mon.Monitor, p.signal.OutboundAllowlist), base)
		if !s.beginInvocation() {
			return
		}
		dispatchCtx, cancel := context.WithTimeout(ctx, s.options.DispatchTimeout)
		result, dispatchErr := mon.Dispatcher.Dispatch(dispatchCtx, mon.Spec, input)
		cancel()
		result.Detail = RedactText(result.Detail)
		continueFleet := s.finishInvocation(result)
		if dispatchErr != nil {
			if ctx.Err() != nil || errors.Is(dispatchErr, context.Canceled) && ctx.Err() != nil {
				return
			}
			s.disableMonitor(mon)
			s.healthFinding(key.stepID, "monitor-unavailable/"+mon.Monitor, monitorFailureDetail(dispatchErr))
			if !continueFleet {
				return
			}
			continue
		}
		if !continueFleet {
			return
		}
		if result.Flagged {
			s.recordFinding(Finding{
				Ts: time.Now().UTC(), RunID: s.runID, StepID: key.stepID,
				Generation: key.Generation, Iteration: key.Iteration, Attempt: key.Attempt,
				Tier: TierMonitor, Monitor: mon.Monitor,
				Severity: Severity(result.Severity), Action: ActionObserved, Detail: result.Detail,
				Evidence:    fmt.Sprintf("transcript seq %d-%d", entries[0].Seq, maxSeq),
				Fingerprint: NewFingerprint(key.stepID, mon.Monitor, result.Detail),
			})
		}
		if !s.withinBudget() {
			s.degradeBudget()
			return
		}
	}
}

func entriesForExecution(entries []transcript.Entry, coords ExecutionCoordinates) []transcript.Entry {
	out := make([]transcript.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Generation == coords.Generation && entry.Iteration == coords.Iteration && entry.Attempt == coords.Attempt {
			out = append(out, entry)
		}
	}
	return out
}

func trustedContext(monitor string, allowlist []string) string {
	var b strings.Builder
	b.WriteString("[trusted-monitor-context]\n")
	if monitor == "stuck-loop" {
		b.WriteString("stuck_loop_prefilter=true\n")
	}
	if len(allowlist) == 0 {
		b.WriteString("effective_outbound_allowlist=(none)\n")
	} else {
		copy := append([]string(nil), allowlist...)
		sort.Strings(copy)
		b.WriteString("effective_outbound_allowlist=")
		b.WriteString(RedactText(strings.Join(copy, ",")))
		b.WriteByte('\n')
	}
	b.WriteString("[/trusted-monitor-context]\n[untrusted-transcript-data]\n")
	return b.String()
}

func (s *Supervisor) monitorDisabled(mon *MonitorDef) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disabled[mon.Monitor] || mon.Circuit.isDisabled()
}

func (s *Supervisor) disableMonitor(mon *MonitorDef) {
	s.mu.Lock()
	s.disabled[mon.Monitor] = true
	s.mu.Unlock()
	if mon.Circuit != nil {
		mon.Circuit.disable()
	}
}

func (s *Supervisor) withinBudget() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.state.Degraded && (s.options.BudgetUSD <= 0 || s.state.SpentUSD < s.options.BudgetUSD)
}

func (s *Supervisor) beginInvocation() bool {
	s.mu.Lock()
	if s.state.Degraded {
		s.mu.Unlock()
		return false
	}
	s.state.InFlight = true
	state := s.state
	s.mu.Unlock()
	if err := writeMonitorState(s.options.StatePath, state); err != nil {
		s.accountingFailure("Tier-2 accounting state could not be persisted before dispatch")
		return s.options.BudgetUSD <= 0
	}
	return true
}

func (s *Supervisor) finishInvocation(result MonitorResult) bool {
	s.mu.Lock()
	if result.CostKnown {
		s.state.SpentUSD += result.CostUSD
	}
	s.state.InFlight = false
	if result.Launched && !result.CostKnown && s.options.BudgetUSD > 0 {
		s.state.Degraded = true
	}
	state := s.state
	s.mu.Unlock()
	if err := writeMonitorState(s.options.StatePath, state); err != nil {
		s.accountingFailure("Tier-2 accounting state could not be updated after dispatch")
		return s.options.BudgetUSD <= 0
	}
	if result.Launched && !result.CostKnown {
		s.healthFinding("", "monitor-accounting-unknown", "A classifier call returned no cost; finite-budget Tier-2 is disabled")
		return s.options.BudgetUSD <= 0
	}
	return true
}

func (s *Supervisor) accountingFailure(detail string) {
	if s.options.BudgetUSD > 0 {
		s.mu.Lock()
		s.state.Degraded = true
		s.mu.Unlock()
	}
	s.healthFinding("", "monitor-state-unavailable", detail)
}

func (s *Supervisor) degradeBudget() {
	s.mu.Lock()
	if s.state.Degraded {
		s.mu.Unlock()
		return
	}
	s.state.Degraded = true
	state := s.state
	s.mu.Unlock()
	if err := writeMonitorState(s.options.StatePath, state); err != nil {
		s.healthFinding("", "monitor-state-unavailable", "Tier-2 budget exhaustion could not be persisted")
	}
	s.healthFinding("", "budget-exhausted", "Tier-2 fleet budget exhausted; degraded to Tier-1 only")
}

func (s *Supervisor) recordFinding(f Finding) {
	if f.Severity != SeverityLow && f.Severity != SeverityMedium && f.Severity != SeverityHigh && f.Severity != SeverityCritical {
		f.Severity = SeverityMedium
	}
	f.Detail = RedactText(f.Detail)
	s.mu.Lock()
	if s.seenFPs[f.Fingerprint] {
		s.mu.Unlock()
		return
	}
	s.seenFPs[f.Fingerprint] = true
	s.mu.Unlock()
	if s.sink != nil {
		if err := s.sink.Append(f); err != nil {
			s.notifyPersistenceFailure()
			return
		}
	}
	if s.notify != nil {
		s.notify(f)
	}
}

func (s *Supervisor) healthFinding(stepID, monitor, detail string) {
	key := monitor + "\x00" + stepID
	s.mu.Lock()
	if s.health[key] {
		s.mu.Unlock()
		return
	}
	s.health[key] = true
	s.mu.Unlock()
	s.recordFinding(Finding{Ts: time.Now().UTC(), RunID: s.runID, StepID: stepID, Tier: TierMonitor,
		Monitor: monitor, Severity: SeverityLow, Action: ActionObserved, Detail: detail,
		Fingerprint: NewFingerprint(s.runID, monitor, stepID)})
}

func (s *Supervisor) notifyPersistenceFailure() {
	s.mu.Lock()
	if s.health["finding-persistence-failed"] {
		s.mu.Unlock()
		return
	}
	s.health["finding-persistence-failed"] = true
	s.mu.Unlock()
	if s.notify != nil {
		s.notify(Finding{Ts: time.Now().UTC(), RunID: s.runID, Tier: TierMonitor,
			Monitor: "finding-persistence-failed", Severity: SeverityLow, Action: ActionObserved,
			Detail: "Tier-2 finding persistence failed", Fingerprint: NewFingerprint(s.runID, "finding-persistence-failed", "singleton")})
	}
}

func (s *Supervisor) seedFingerprints() {
	findings, err := ReadAll(s.options.FindingsPath)
	if err != nil {
		s.healthFinding("", "findings-unavailable", "Existing security findings could not be read")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range findings {
		s.seenFPs[f.Fingerprint] = true
	}
}

func (s *Supervisor) restoreAccounting() {
	if !s.options.Resume {
		if err := writeMonitorState(s.options.StatePath, s.state); err != nil {
			s.accountingFailure("Tier-2 accounting state could not be initialized")
		}
		return
	}
	state, err := readMonitorState(s.options.StatePath)
	if err == nil && !state.InFlight {
		s.mu.Lock()
		s.state = state
		s.mu.Unlock()
		return
	}
	reason := "Stored Tier-2 cost is uncertain after reopen"
	if errors.Is(err, os.ErrNotExist) {
		reason = "Tier-2 accounting is missing for this reopened run"
	}
	if s.options.BudgetUSD > 0 {
		s.mu.Lock()
		s.state.Degraded = true
		state = s.state
		s.mu.Unlock()
		_ = writeMonitorState(s.options.StatePath, state)
		s.healthFinding("", "monitor-accounting-unknown", reason+"; finite-budget Tier-2 is disabled")
		return
	}
	s.healthFinding("", "monitor-accounting-unknown", reason+"; unlimited-budget Tier-2 will continue")
	s.mu.Lock()
	s.state = newMonitorState()
	state = s.state
	s.mu.Unlock()
	_ = writeMonitorState(s.options.StatePath, state)
}

func monitorFailureDetail(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "Tier-2 classifier timed out"
	}
	return "Tier-2 classifier failed to connect or return a valid verdict"
}

func renderWindow(entries []transcript.Entry) string {
	var b strings.Builder
	for _, entry := range entries {
		fmt.Fprintf(&b, "[%s]\n", entry.Role)
		fmt.Fprintf(&b, "[entry seq=%d role=%s gen=%d iter=%d attempt=%d]\n", entry.Seq, entry.Role, entry.Generation, entry.Iteration, entry.Attempt)
		for i, block := range entry.Blocks {
			fmt.Fprintf(&b, "[block=%d type=%s]\n", i, block.Type)
			switch block.Type {
			case transcript.BlockText, transcript.BlockThinking:
				b.WriteString(RedactText(block.Text))
			case transcript.BlockToolUse:
				if activity := block.Activity(); activity != nil {
					fmt.Fprintf(&b, "<tool_use name=%q>\nstatus=%q\n%s\n</tool_use>", RedactText(activity.Title), RedactText(activity.Status), RedactText(string(activity.Input)))
				}
			case transcript.BlockToolResult:
				if activity := block.Activity(); activity != nil {
					fmt.Fprintf(&b, "<tool_result>\nname=%q status=%q\n%s\n</tool_result>", RedactText(activity.Title), RedactText(activity.Status), RedactText(toolText(activity)))
				}
			}
			b.WriteByte('\n')
		}
	}
	b.WriteString("[/untrusted-transcript-data]\n")
	return clipRendered(b.String())
}

// boundWindow remains the entry-level half of the dual bound; renderWindow
// applies the exact final byte cap, including labels and truncation markers.
func boundWindow(entries []transcript.Entry) []transcript.Entry {
	if len(entries) == 0 {
		return nil
	}
	if len(entries) > entryCountCap {
		entries = entries[len(entries)-entryCountCap:]
	}
	total, start := 0, len(entries)-1
	for i := len(entries) - 1; i >= 0; i-- {
		size := len(renderWindow([]transcript.Entry{entries[i]}))
		if total > 0 && total+size > tokenCeiling {
			break
		}
		total += size
		start = i
	}
	return entries[start:]
}

func clipRendered(s string) string {
	if len(s) <= renderByteCap {
		return s
	}
	const marker = "[earlier content truncated]\n"
	start := len(s) - (renderByteCap - len(marker))
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return marker + s[start:]
}

func combineRendered(prefix, body string) string {
	if len(prefix)+len(body) <= renderByteCap {
		return prefix + body
	}
	const marker = "[earlier transcript content truncated]\n"
	available := renderByteCap - len(prefix) - len(marker)
	if available <= 0 {
		return clipRendered(prefix)
	}
	start := len(body) - available
	for start < len(body) && !utf8.RuneStart(body[start]) {
		start++
	}
	return prefix + marker + body[start:]
}
