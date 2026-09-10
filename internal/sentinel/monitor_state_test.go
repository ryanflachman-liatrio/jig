package sentinel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"jig/internal/transcript"
)

func TestMonitorStateRoundTripAndPersistenceOff(t *testing.T) {
	want := monitorState{Version: monitorStateVersion, SpentUSD: 1.25, Degraded: true, InFlight: true}
	path := filepath.Join(t.TempDir(), "security-monitor-state.json")
	if err := writeMonitorState(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := readMonitorState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("state = %+v, want %+v", got, want)
	}
	if err := writeMonitorState("", want); err != nil {
		t.Fatal(err)
	}
	if got, err := readMonitorState(""); err != nil || got.Version != monitorStateVersion {
		t.Fatalf("persistence-off state=%+v err=%v", got, err)
	}
}

func TestResumeWithInFlightFiniteBudgetDisablesDispatch(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "security-monitor-state.json")
	findingsPath := filepath.Join(dir, "findings.jsonl")
	transcriptPath := filepath.Join(dir, "transcript.jsonl")
	if err := writeMonitorState(statePath, monitorState{Version: monitorStateVersion, InFlight: true}); err != nil {
		t.Fatal(err)
	}
	seedTranscript(t, transcriptPath, 1, 0, "new activity")
	stub := newStub("new activity", "high", 0)
	signals := make(chan StepSignal, 1)
	sink, err := NewWriter(findingsPath)
	if err != nil {
		t.Fatal(err)
	}
	supervisor := NewSupervisor("run", signals, sink, []MonitorDef{{Monitor: "prompt-injection", Dispatcher: stub}},
		func(string) string { return transcriptPath }, nil,
		SupervisorOptions{BatchSize: 1, BudgetUSD: 1, StatePath: statePath, FindingsPath: findingsPath, Resume: true})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { supervisor.Run(ctx); close(done) }()
	signals <- StepSignal{RunID: "run", StepID: "step", Seq: 1}
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	if stub.calls.Load() != 0 {
		t.Fatalf("dispatcher calls = %d", stub.calls.Load())
	}
	findings, err := ReadAll(findingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Monitor != "monitor-accounting-unknown" {
		t.Fatalf("findings = %+v", findings)
	}
	state, err := readMonitorState(statePath)
	if err != nil || !state.Degraded || state.InFlight {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}

func TestRenderedMonitorInputIsRedactedAndBounded(t *testing.T) {
	const secret = "AKIA" + "ABCDEFGHIJKLMNOP"
	entry := transcript.Entry{Seq: 4, Role: transcript.RoleAssistant, Blocks: []transcript.Block{
		{Type: transcript.BlockText, Text: secret},
		{Type: transcript.BlockToolUse, Name: "Bash", Input: []byte(`{"command":"echo ` + secret + `"}`)},
		{Type: transcript.BlockToolResult, Content: secret},
	}}
	got := renderWindow([]transcript.Entry{entry})
	if len(got) > renderByteCap {
		t.Fatalf("rendered bytes = %d", len(got))
	}
	if containsSecret(got) || !strings.Contains(got, "[aws-key:") {
		t.Fatalf("redaction failed: %q", got)
	}
	huge := renderWindow([]transcript.Entry{{Seq: 5, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: strings.Repeat("界", renderByteCap)}}}})
	if len(huge) > renderByteCap || !utf8.ValidString(huge) {
		t.Fatalf("invalid bound: bytes=%d utf8=%v", len(huge), utf8.ValidString(huge))
	}
}

func TestMonitorStateRejectsCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security-monitor-state.json")
	if err := os.WriteFile(path, []byte(`{"version":99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readMonitorState(path); err == nil {
		t.Fatal("expected corrupt state error")
	}
}
