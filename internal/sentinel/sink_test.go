package sentinel

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestFindingsSinkRoundtrip verifies write → read order preservation and that
// a "" path is a silent no-op (persistence-off path).
func TestFindingsSinkRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "findings.jsonl")

	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	now := time.Now().UTC().Truncate(0)
	findings := []Finding{
		{Ts: now, RunID: "r1", StepID: "a", Tier: TierGuard, Monitor: "secret-leak", Severity: SeverityHigh, Action: ActionBlocked, Detail: "[aws-key:…MPLE]", Fingerprint: "fp1"},
		{Ts: now, RunID: "r1", StepID: "b", Tier: TierMonitor, Monitor: "prompt-injection", Severity: SeverityMedium, Action: ActionObserved, Detail: "injection in tool_result", Fingerprint: "fp2"},
		{Ts: now, RunID: "r1", StepID: "a", Tier: TierGuard, Monitor: "denied-shell", Severity: SeverityCritical, Action: ActionEscalated, Detail: "rm -rf", Fingerprint: "fp3"},
	}

	for _, f := range findings {
		if err := w.Append(f); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != len(findings) {
		t.Fatalf("read %d findings, want %d", len(got), len(findings))
	}
	for i, want := range findings {
		if got[i].Fingerprint != want.Fingerprint {
			t.Errorf("[%d] Fingerprint = %q, want %q", i, got[i].Fingerprint, want.Fingerprint)
		}
		if got[i].Monitor != want.Monitor {
			t.Errorf("[%d] Monitor = %q, want %q", i, got[i].Monitor, want.Monitor)
		}
		if got[i].Severity != want.Severity {
			t.Errorf("[%d] Severity = %q, want %q", i, got[i].Severity, want.Severity)
		}
	}

	// Persistence-off: "" path is a silent no-op.
	wNoop, err := NewWriter("")
	if err != nil {
		t.Fatalf("NewWriter(\"\") error: %v", err)
	}
	if err := wNoop.Append(findings[0]); err != nil {
		t.Errorf("Append on noop writer: %v", err)
	}
	if err := wNoop.Close(); err != nil {
		t.Errorf("Close on noop writer: %v", err)
	}
	noopFindings, err := ReadAll("")
	if err != nil {
		t.Fatalf("ReadAll(\"\") error: %v", err)
	}
	if len(noopFindings) != 0 {
		t.Errorf("ReadAll(\"\") returned %d findings, want 0", len(noopFindings))
	}
}

// TestWriterConcurrentAppendClose reproduces the data race between a producer
// goroutine appending findings and the owner closing the writer — the exact
// pattern in production, where the Tier-2 supervisor appends from its own
// goroutine while the run tears the writer down. Run with `go test -race`.
//
// It also asserts Close is idempotent and that appends after Close are a safe
// no-op rather than a write to a closed descriptor.
func TestWriterConcurrentAppendClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "findings.jsonl")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	f := Finding{RunID: "r", StepID: "s", Tier: TierMonitor, Monitor: "m", Severity: SeverityLow, Action: ActionObserved, Fingerprint: "fp"}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = w.Append(f)
		}
	}()

	// Close concurrently with the in-flight appends.
	_ = w.Close()
	// Close is idempotent.
	if err := w.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	wg.Wait()

	// Appends after Close are a safe no-op, never a panic or write to a closed fd.
	if err := w.Append(f); err != nil {
		t.Errorf("Append after Close: %v", err)
	}
}
