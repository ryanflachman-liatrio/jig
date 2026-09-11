package sentinel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"jig/internal/transcript"
)

type errorDispatcher struct{ calls atomic.Int32 }

func (d *errorDispatcher) Dispatch(context.Context, MonitorSpec, string) (MonitorResult, error) {
	d.calls.Add(1)
	return MonitorResult{CostUSD: 0.01, CostKnown: true, Launched: true}, errors.New("raw vendor transcript must not persist")
}

type captureDispatcher struct {
	mu     sync.Mutex
	inputs []string
	result MonitorResult
	calls  chan struct{}
}

func (d *captureDispatcher) Dispatch(_ context.Context, _ MonitorSpec, input string) (MonitorResult, error) {
	d.mu.Lock()
	d.inputs = append(d.inputs, input)
	d.mu.Unlock()
	select {
	case d.calls <- struct{}{}:
	default:
	}
	return d.result, nil
}

func (d *captureDispatcher) captured() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.inputs...)
}

func TestSupervisorClassifierFailureIsIsolatedAndDeduplicated(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "transcript.jsonl")
	findingsPath := filepath.Join(dir, "findings.jsonl")
	statePath := filepath.Join(dir, "security-monitor-state.json")
	seedTranscript(t, transcriptPath, 1, 0, "FLAG")
	broken := &errorDispatcher{}
	working := newStub("FLAG", "high", 0.02)
	sink, err := NewWriter(findingsPath)
	if err != nil {
		t.Fatal(err)
	}
	signals := make(chan StepSignal, 4)
	supervisor := NewSupervisor("run", signals, sink, []MonitorDef{
		{Monitor: "broken", Dispatcher: broken, Circuit: &MonitorCircuit{}},
		{Monitor: "working", Dispatcher: working},
	}, func(string) string { return transcriptPath }, nil,
		SupervisorOptions{BatchSize: 1, BudgetUSD: 1, StatePath: statePath, FindingsPath: findingsPath})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { supervisor.Run(ctx); close(done) }()
	signals <- StepSignal{RunID: "run", StepID: "step", Seq: 1}
	waitFlagged(t, working, 2*time.Second)
	w, err := transcript.Create(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	seq, err := w.Append(transcript.Entry{Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "FLAG again"}}})
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	signals <- StepSignal{RunID: "run", StepID: "step", Seq: seq}
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	if broken.calls.Load() != 1 {
		t.Fatalf("broken classifier calls = %d", broken.calls.Load())
	}
	findings, err := ReadAll(findingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var unavailable, flagged int
	for _, finding := range findings {
		if finding.Monitor == "monitor-unavailable/broken" {
			unavailable++
			if strings.Contains(finding.Detail, "vendor") {
				t.Fatalf("raw error persisted: %q", finding.Detail)
			}
		}
		if finding.Monitor == "working" {
			flagged++
		}
	}
	if unavailable != 1 || flagged == 0 {
		t.Fatalf("findings = %+v", findings)
	}
	state, err := readMonitorState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.SpentUSD < 0.03 {
		t.Fatalf("known error cost was not retained: %+v", state)
	}
}

func TestExecutionOrderingAndFiltering(t *testing.T) {
	old := ExecutionCoordinates{Generation: 1, Iteration: 9, Attempt: 2}
	current := ExecutionCoordinates{Generation: 2, Iteration: 0, Attempt: 0}
	if compareExecution(old, current) >= 0 || compareExecution(current, old) <= 0 || compareExecution(current, current) != 0 {
		t.Fatal("execution ordering is not lexicographic by generation, iteration, attempt")
	}
	entries := []transcript.Entry{
		{Seq: 1, Generation: 1, Iteration: 9, Attempt: 2},
		{Seq: 2, Generation: 2, Iteration: 0, Attempt: 0},
	}
	got := entriesForExecution(entries, current)
	if len(got) != 1 || got[0].Seq != 2 {
		t.Fatalf("filtered entries = %+v", got)
	}
}

func TestSupervisorRetainsBoundedOverlapAcrossBatches(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "transcript.jsonl")
	w, err := transcript.Create(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	seq1, err := w.Append(transcript.Entry{Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Content: "sensitive read before boundary"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	dispatcher := &captureDispatcher{calls: make(chan struct{}, 2), result: MonitorResult{Severity: "low", CostKnown: true, Launched: true}}
	signals := make(chan StepSignal, 2)
	supervisor := NewSupervisor("run", signals, nil, []MonitorDef{{Monitor: "prompt-injection", Dispatcher: dispatcher}},
		func(string) string { return transcriptPath }, nil, SupervisorOptions{BatchSize: 1})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { supervisor.Run(ctx); close(done) }()
	signals <- StepSignal{RunID: "run", StepID: "step", Seq: seq1}
	select {
	case <-dispatcher.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("first batch was not classified")
	}
	w, err = transcript.Create(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	seq2, err := w.Append(transcript.Entry{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "outbound action after boundary"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	signals <- StepSignal{RunID: "run", StepID: "step", Seq: seq2}
	select {
	case <-dispatcher.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("second batch was not classified")
	}
	cancel()
	<-done
	inputs := dispatcher.captured()
	if len(inputs) != 2 || !strings.Contains(inputs[1], "sensitive read before boundary") || !strings.Contains(inputs[1], "outbound action after boundary") {
		t.Fatalf("overlapping inputs = %#v", inputs)
	}
}

func TestSupervisorResumeCarriesBudgetAndDeduplication(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "transcript.jsonl")
	findingsPath := filepath.Join(dir, "findings.jsonl")
	statePath := filepath.Join(dir, "security-monitor-state.json")
	seedTranscript(t, transcriptPath, 1, 0, "FLAG")
	if err := writeMonitorState(statePath, monitorState{Version: monitorStateVersion, SpentUSD: 0.25}); err != nil {
		t.Fatal(err)
	}
	runOnce := func(stub *stubDispatcher, seq int) {
		sink, err := NewWriter(findingsPath)
		if err != nil {
			t.Fatal(err)
		}
		signals := make(chan StepSignal, 1)
		supervisor := NewSupervisor("run", signals, sink, []MonitorDef{{Monitor: "prompt-injection", Dispatcher: stub}},
			func(string) string { return transcriptPath }, nil,
			SupervisorOptions{BatchSize: 1, BudgetUSD: 0.30, StatePath: statePath, FindingsPath: findingsPath, Resume: true})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { supervisor.Run(ctx); close(done) }()
		signals <- StepSignal{RunID: "run", StepID: "step", Seq: seq}
		if stub.costUSD > 0 {
			waitFlagged(t, stub, 2*time.Second)
			time.Sleep(25 * time.Millisecond)
		} else {
			time.Sleep(75 * time.Millisecond)
		}
		cancel()
		<-done
	}

	first := newStub("FLAG", "high", 0.10)
	runOnce(first, 1)
	state, err := readMonitorState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.SpentUSD != 0.35 || !state.Degraded || state.InFlight {
		t.Fatalf("state after budget overshoot = %+v", state)
	}
	w, err := transcript.Create(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	seq, err := w.Append(transcript.Entry{Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "FLAG again"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	second := newStub("FLAG", "high", 0)
	runOnce(second, seq)
	if second.calls.Load() != 0 {
		t.Fatalf("degraded resumed supervisor made %d calls", second.calls.Load())
	}
	findings, err := ReadAll(findingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var flagged, exhausted int
	for _, finding := range findings {
		switch finding.Monitor {
		case "prompt-injection":
			flagged++
		case "budget-exhausted":
			exhausted++
		}
	}
	if flagged != 1 || exhausted != 1 {
		t.Fatalf("resume did not preserve finding deduplication: %+v", findings)
	}
}

func TestSupervisorStateFailureIsVisibleAndFailClosedForFiniteBudget(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "transcript.jsonl")
	findingsPath := filepath.Join(dir, "findings.jsonl")
	seedTranscript(t, transcriptPath, 1, 0, "FLAG")
	stateDirectory := filepath.Join(dir, "state-is-a-directory")
	if err := os.Mkdir(stateDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := newStub("FLAG", "high", 0.1)
	sink, err := NewWriter(findingsPath)
	if err != nil {
		t.Fatal(err)
	}
	signals := make(chan StepSignal, 1)
	supervisor := NewSupervisor("run", signals, sink, []MonitorDef{{Monitor: "prompt-injection", Dispatcher: stub}},
		func(string) string { return transcriptPath }, nil,
		SupervisorOptions{BatchSize: 1, BudgetUSD: 1, StatePath: stateDirectory, FindingsPath: findingsPath})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { supervisor.Run(ctx); close(done) }()
	signals <- StepSignal{RunID: "run", StepID: "step", Seq: 1}
	time.Sleep(75 * time.Millisecond)
	cancel()
	<-done
	if stub.calls.Load() != 0 {
		t.Fatalf("classifier ran without durable accounting: %d calls", stub.calls.Load())
	}
	findings, err := ReadAll(findingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Monitor != "monitor-state-unavailable" {
		t.Fatalf("state failure findings = %+v", findings)
	}
}

func TestSupervisorRedactsReturnedDetailBeforePersistence(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "transcript.jsonl")
	findingsPath := filepath.Join(dir, "findings.jsonl")
	seedTranscript(t, transcriptPath, 1, 0, "FLAG")
	dispatcher := &captureDispatcher{calls: make(chan struct{}, 1), result: MonitorResult{
		Flagged: true, Severity: "high", Detail: "evidence AKIA" + "ABCDEFGHIJKLMNOP", CostKnown: true, Launched: true,
	}}
	sink, err := NewWriter(findingsPath)
	if err != nil {
		t.Fatal(err)
	}
	signals := make(chan StepSignal, 1)
	supervisor := NewSupervisor("run", signals, sink, []MonitorDef{{Monitor: "prompt-injection", Dispatcher: dispatcher}},
		func(string) string { return transcriptPath }, nil,
		SupervisorOptions{BatchSize: 1, FindingsPath: findingsPath})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { supervisor.Run(ctx); close(done) }()
	signals <- StepSignal{RunID: "run", StepID: "step", Seq: 1}
	select {
	case <-dispatcher.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("classifier was not called")
	}
	time.Sleep(25 * time.Millisecond)
	cancel()
	<-done
	findings, err := ReadAll(findingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || strings.Contains(findings[0].Detail, "AKIA"+"ABCDEFGHIJKLMNOP") || !strings.Contains(findings[0].Detail, "[aws-key:") {
		t.Fatalf("persisted detail = %+v", findings)
	}
}
