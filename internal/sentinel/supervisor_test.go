package sentinel

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"jig/internal/transcript"
)

// stubDispatcher is a test double for MonitorDispatcher. It flags windows
// containing marker and records every call for assertion.
type stubDispatcher struct {
	marker   string
	severity string
	costUSD  float64
	calls    atomic.Int32  // total calls received
	flagged  chan struct{} // closed or written on each flagging call
}

func newStub(marker, severity string, costUSD float64) *stubDispatcher {
	return &stubDispatcher{
		marker:   marker,
		severity: severity,
		costUSD:  costUSD,
		flagged:  make(chan struct{}, 16),
	}
}

func (s *stubDispatcher) Dispatch(_ context.Context, _, windowText string) (MonitorResult, error) {
	s.calls.Add(1)
	flagged := strings.Contains(windowText, s.marker)
	if flagged {
		select {
		case s.flagged <- struct{}{}:
		default:
		}
	}
	return MonitorResult{Flagged: flagged, Severity: s.severity, CostUSD: s.costUSD}, nil
}

// seedTranscript writes n entries to path. If markerAt >= 0, that index gets an
// entry whose tool_result content contains marker.
func seedTranscript(t *testing.T, path string, n int, markerAt int, marker string) {
	t.Helper()
	w, err := transcript.Create(path)
	if err != nil {
		t.Fatalf("create transcript: %v", err)
	}
	for i := 0; i < n; i++ {
		var blocks []transcript.Block
		if i == markerAt {
			blocks = []transcript.Block{{Type: transcript.BlockToolResult, Content: marker}}
		} else {
			blocks = []transcript.Block{{Type: transcript.BlockText, Text: "normal turn"}}
		}
		if _, err := w.Append(transcript.Entry{Role: transcript.RoleUser, Blocks: blocks}); err != nil {
			t.Fatalf("append entry %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close transcript: %v", err)
	}
}

// waitFlagged waits for the stub to flag at least once or fails the test.
func waitFlagged(t *testing.T, stub *stubDispatcher, timeout time.Duration) {
	t.Helper()
	select {
	case <-stub.flagged:
	case <-time.After(timeout):
		t.Fatal("timed out waiting for monitor dispatch to flag")
	}
	// Give the supervisor a moment to write the finding before we read it.
	time.Sleep(20 * time.Millisecond)
}

// TestSupervisorBatching proves:
//  1. BatchSize signals trigger an immediate flush.
//  2. A burst of signals collapses to one monitor call (cursor advances; second
//     flush reads no new entries → no dispatch).
//  3. Exactly one finding is produced and persisted to findings.jsonl.
//  4. A duplicate flush (same finding detail → same fingerprint) is deduplicated
//     so the second call doesn't produce a second finding on disk.
func TestSupervisorBatching(t *testing.T) {
	dir := t.TempDir()
	tPath := filepath.Join(dir, "transcript.jsonl")
	fPath := filepath.Join(dir, "findings.jsonl")

	const marker = "INJECT:do_something_bad"
	seedTranscript(t, tPath, 4, 3, marker) // 4 entries; entry 3 has marker

	stub := newStub(marker, "high", 0.001)
	sig := make(chan StepSignal, 20)
	fw, err := NewWriter(fPath)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer fw.Close()

	sup := NewSupervisor(
		"run1",
		sig,
		fw,
		[]MonitorDef{{File: "prompt-injection.md", Monitor: "prompt-injection", Dispatcher: stub}},
		10.0, // high budget, won't degrade
		func(stepID string) string {
			if stepID == "impl" {
				return tPath
			}
			return ""
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go sup.Run(ctx)

	// First burst: BatchSize signals → immediate flush → reads 4 entries → marker found.
	for i := 0; i < BatchSize; i++ {
		sig <- StepSignal{RunID: "run1", StepID: "impl", Seq: i + 1}
	}
	waitFlagged(t, stub, 3*time.Second)

	findings, err := ReadAll(fPath)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("after first burst: want 1 finding, got %d", len(findings))
	}
	if findings[0].Monitor != "prompt-injection" {
		t.Errorf("finding Monitor = %q, want prompt-injection", findings[0].Monitor)
	}
	if findings[0].Tier != TierMonitor {
		t.Errorf("finding Tier = %q, want monitor", findings[0].Tier)
	}

	// Second burst: cursor already at EOF → no new entries → dispatcher not called again.
	callsBefore := stub.calls.Load()
	for i := 0; i < BatchSize; i++ {
		sig <- StepSignal{RunID: "run1", StepID: "impl", Seq: BatchSize + i + 1}
	}
	time.Sleep(150 * time.Millisecond) // allow potential flush time
	callsAfter := stub.calls.Load()
	if callsAfter > callsBefore {
		t.Errorf("second burst triggered %d extra dispatcher calls; want 0 (cursor at EOF)", callsAfter-callsBefore)
	}

	// Still exactly 1 finding on disk.
	findings2, _ := ReadAll(fPath)
	if len(findings2) != 1 {
		t.Errorf("after second burst: want 1 finding, got %d", len(findings2))
	}
}

// TestSupervisorBudgetDegrade proves:
//  1. When the summed monitor cost reaches the per-run budget, dispatch stops.
//  2. Exactly one "degraded-to-tier1" finding is appended to findings.jsonl.
//  3. The observed run is not blocked (Run returns and the observed transcript
//     entries are not affected).
func TestSupervisorBudgetDegrade(t *testing.T) {
	dir := t.TempDir()
	tPath := filepath.Join(dir, "transcript.jsonl")
	fPath := filepath.Join(dir, "findings.jsonl")

	const marker = "INJECT:drain_budget"
	seedTranscript(t, tPath, 2, 1, marker)

	const budget = 0.0005 // very low budget
	// costUSD per call exceeds budget → degrade after first call.
	stub := newStub(marker, "medium", budget+0.0001)
	sig := make(chan StepSignal, 10)
	fw, err := NewWriter(fPath)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer fw.Close()

	sup := NewSupervisor(
		"run2",
		sig,
		fw,
		[]MonitorDef{{File: "prompt-injection.md", Monitor: "prompt-injection", Dispatcher: stub}},
		budget,
		func(stepID string) string {
			if stepID == "step1" {
				return tPath
			}
			return ""
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go sup.Run(ctx)

	// First batch: triggers flush → dispatch → cost > budget → degrade.
	for i := 0; i < BatchSize; i++ {
		sig <- StepSignal{RunID: "run2", StepID: "step1", Seq: i + 1}
	}
	waitFlagged(t, stub, 3*time.Second)
	time.Sleep(50 * time.Millisecond) // allow degraded finding to be written

	// Verify degraded finding.
	findings, err := ReadAll(fPath)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	var gotFinding, gotDegrade bool
	for _, f := range findings {
		if f.Monitor == "prompt-injection" {
			gotFinding = true
		}
		if f.Monitor == "budget-exhausted" {
			gotDegrade = true
			if f.Action != ActionObserved {
				t.Errorf("degrade finding Action = %q, want observed", f.Action)
			}
			if f.Severity != SeverityLow {
				t.Errorf("degrade finding Severity = %q, want low", f.Severity)
			}
		}
	}
	if !gotFinding {
		t.Errorf("want prompt-injection finding before degrade")
	}
	if !gotDegrade {
		t.Errorf("want budget-exhausted finding after degrade")
	}

	// Add new transcript entries and send another batch.
	// Supervisor is degraded: no additional dispatches should occur.
	w2, _ := transcript.Create(tPath) // appends to existing
	for i := 0; i < 3; i++ {
		_, _ = w2.Append(transcript.Entry{
			Role:   transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "new entry with " + marker}},
		})
	}
	_ = w2.Close()

	callsBefore := stub.calls.Load()
	for i := 0; i < BatchSize; i++ {
		sig <- StepSignal{RunID: "run2", StepID: "step1", Seq: BatchSize + i + 1}
	}
	time.Sleep(150 * time.Millisecond)
	if callsAfter := stub.calls.Load(); callsAfter > callsBefore {
		t.Errorf("degraded supervisor made %d extra calls; want 0", callsAfter-callsBefore)
	}

	// Still only the original findings (no second degrade finding).
	findings3, _ := ReadAll(fPath)
	var degradeCount int
	for _, f := range findings3 {
		if f.Monitor == "budget-exhausted" {
			degradeCount++
		}
	}
	if degradeCount != 1 {
		t.Errorf("want exactly 1 degrade finding, got %d", degradeCount)
	}
}

// TestBoundWindow verifies the dual-bound truncation logic:
//   - Count cap: more than entryCountCap entries → only the most-recent are kept.
//   - Token cap: total bytes > tokenCeiling → entries are dropped from the oldest end.
//   - At least one entry is always returned when input is non-empty.
func TestBoundWindow(t *testing.T) {
	// Helper: build an entry with approximately n bytes of text.
	bigEntry := func(n int) transcript.Entry {
		return transcript.Entry{
			Role:   transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: strings.Repeat("x", n)}},
		}
	}

	t.Run("count cap", func(t *testing.T) {
		entries := make([]transcript.Entry, entryCountCap+5)
		for i := range entries {
			entries[i] = bigEntry(10)
		}
		got := boundWindow(entries)
		if len(got) != entryCountCap {
			t.Errorf("len = %d, want %d", len(got), entryCountCap)
		}
	})

	t.Run("token cap trims oldest", func(t *testing.T) {
		// 10 entries × (tokenCeiling/5 + 1) bytes each → first few should be dropped.
		sizeEach := tokenCeiling/5 + 100
		entries := make([]transcript.Entry, 10)
		for i := range entries {
			entries[i] = bigEntry(sizeEach)
		}
		got := boundWindow(entries)
		// We should end up with fewer than 10 entries (token cap fires).
		if len(got) == 10 {
			t.Errorf("token cap did not trim: got all 10 entries")
		}
		if len(got) == 0 {
			t.Errorf("token cap trimmed all entries; want at least 1")
		}
	})

	t.Run("always at least one entry", func(t *testing.T) {
		// One entry that exceeds the token ceiling on its own.
		got := boundWindow([]transcript.Entry{bigEntry(tokenCeiling + 1)})
		if len(got) != 1 {
			t.Errorf("len = %d, want 1 (single oversized entry must not be dropped)", len(got))
		}
	})

	t.Run("empty input", func(t *testing.T) {
		if got := boundWindow(nil); len(got) != 0 {
			t.Errorf("len = %d, want 0", len(got))
		}
	})
}

// TestRenderWindow verifies the window text contains role markers and block content.
func TestRenderWindow(t *testing.T) {
	entries := []transcript.Entry{
		{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
			{Type: transcript.BlockText, Text: "let me help"},
			{Type: transcript.BlockToolUse, Name: "Read", Input: []byte(`{"file_path":"main.go"}`)},
		}},
		{Role: transcript.RoleUser, Blocks: []transcript.Block{
			{Type: transcript.BlockToolResult, Content: "package main"},
		}},
	}
	got := renderWindow(entries)
	checks := []string{
		"[assistant]",
		"let me help",
		"<tool_use name=\"Read\">",
		"main.go",
		"[user]",
		"<tool_result>",
		"package main",
	}
	for _, s := range checks {
		if !strings.Contains(got, s) {
			t.Errorf("renderWindow output missing %q:\n%s", s, got)
		}
	}
}
