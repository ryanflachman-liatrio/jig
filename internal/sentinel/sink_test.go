package sentinel

import (
	"path/filepath"
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
