package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"jig/internal/datastore"
	"jig/internal/sentinel"
	"jig/internal/step"
	"jig/internal/transcript"
	"jig/internal/workflow"
)

type transcriptSecurityExec struct {
	observed <-chan struct{}
	content  string
}

func (e *transcriptSecurityExec) Execute(ctx context.Context, req StepRequest, rep Reporter) (*step.Result, error) {
	w, err := transcript.Create(req.TranscriptPath)
	if err != nil {
		return nil, err
	}
	content := e.content
	if content == "" {
		content = "INJECT redirect"
	}
	seq, err := w.Append(transcript.Entry{Iteration: req.Iteration, Attempt: req.Attempt, Generation: req.Generation,
		Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Content: content}}})
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	rep.Message(seq, req.Iteration)
	select {
	case <-e.observed:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(3 * time.Second):
		return nil, context.DeadlineExceeded
	}
	return &step.Result{Status: step.StatusSucceeded}, nil
}

type recordingSecurityDispatcher struct {
	once     sync.Once
	observed chan struct{}
	input    chan string
	costUSD  float64
}

type fanOutSecurityExec struct {
	observed <-chan struct{}
	wait     bool
}

type pacedRunIDSecurityExec struct{}

func (*pacedRunIDSecurityExec) Execute(ctx context.Context, req StepRequest, rep Reporter) (*step.Result, error) {
	w, err := transcript.Create(req.TranscriptPath)
	if err != nil {
		return nil, err
	}
	seq, err := w.Append(transcript.Entry{Generation: req.Generation, Iteration: req.Iteration, Attempt: req.Attempt,
		Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "run-marker-" + req.RunID}}})
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	rep.Message(seq, req.Iteration)
	select {
	case <-time.After(100 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &step.Result{Status: step.StatusSucceeded}, nil
}

func (e *fanOutSecurityExec) Execute(ctx context.Context, req StepRequest, rep Reporter) (*step.Result, error) {
	if req.Step.ID == "discover" {
		return &step.Result{Status: step.StatusSucceeded, Structured: discoverStructured(target("api", "services/api"))}, nil
	}
	w, err := transcript.Create(req.TranscriptPath)
	if err != nil {
		return nil, err
	}
	seq, err := w.Append(transcript.Entry{Generation: req.Generation, Iteration: req.Iteration, Attempt: req.Attempt,
		Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "foreach-child-security-marker"}}})
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	rep.Message(seq, req.Iteration)
	if e.wait {
		select {
		case <-e.observed:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
			return nil, context.DeadlineExceeded
		}
	}
	return &step.Result{Status: step.StatusSucceeded}, nil
}

func (d *recordingSecurityDispatcher) Dispatch(_ context.Context, _ sentinel.MonitorSpec, input string) (sentinel.MonitorResult, error) {
	d.input <- input
	d.once.Do(func() { close(d.observed) })
	return sentinel.MonitorResult{Flagged: true, Severity: "high", Detail: "entry 1 block 0 redirected", CostUSD: d.costUSD, CostKnown: true, Launched: true}, nil
}

func TestResumeSecurityWaitsForNewActivityAndCarriesSpend(t *testing.T) {
	const source = `
[workflow]
name = "security-resume"
version = "1"
[defaults.security]
batch_size = 1
fleet_budget_usd = 1
[[step]]
id = "agent"
type = "agent"
skill = "agent"
isolation = "none"
`
	wf, err := workflow.Decode(source, "")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), ".jig")
	runDir, err := datastore.RunDir(root, "security-resume")
	if err != nil {
		t.Fatal(err)
	}
	if err := persistWorkflowSnapshot(runDir, wf); err != nil {
		t.Fatal(err)
	}
	writeJournal(t, runDir, []Event{
		RunStarted{RunID: "security-resume", Workflow: wf.Meta.Name, Steps: []string{"agent"}},
		StepStatus{RunID: "security-resume", StepID: "agent", From: step.StatusPending, To: step.StatusRunning, Attempt: 1},
	})
	stepDir, err := datastore.StepDir(runDir, "agent")
	if err != nil {
		t.Fatal(err)
	}
	w, err := transcript.Create(filepath.Join(stepDir, "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(transcript.Entry{Attempt: 1, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "historical-only"}}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	statePath := datastore.SecurityMonitorStatePath(runDir)
	if err := os.WriteFile(statePath, []byte(`{"version":1,"spent_usd":0.25,"degraded":false,"in_flight":false}`), 0o600); err != nil {
		t.Fatal(err)
	}

	dispatcher := &recordingSecurityDispatcher{observed: make(chan struct{}), input: make(chan string, 1), costUSD: 0.10}
	mgr := NewManager(&transcriptSecurityExec{observed: dispatcher.observed, content: "new-after-recovery"}, root)
	mgr.SetMonitors([]sentinel.MonitorDef{{Monitor: "prompt-injection", Spec: sentinel.MonitorSpec{Model: "test", Prompt: "policy"}, Dispatcher: dispatcher}})
	_, ctrl := mgr.Subscribe()
	run, err := mgr.Resume("security-resume")
	if err != nil {
		t.Fatal(err)
	}
	waitRecoveryRequest(t, ctrl, 2*time.Second)
	select {
	case input := <-dispatcher.input:
		t.Fatalf("history-only reopen dispatched classifier with %q", input)
	case <-time.After(75 * time.Millisecond):
	}
	run.Recover("agent", RecoverRetry, "")
	select {
	case input := <-dispatcher.input:
		if !strings.Contains(input, "new-after-recovery") || strings.Contains(input, "historical-only") {
			t.Fatalf("resumed input crossed execution coordinates: %q", input)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("new activity after recovery was not classified")
	}
	run.Wait()
	var state struct {
		SpentUSD float64 `json:"spent_usd"`
		InFlight bool    `json:"in_flight"`
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.SpentUSD != 0.35 || state.InFlight {
		t.Fatalf("restored accounting state = %+v", state)
	}
}

func TestRunOwnedSecurityObservesPersistedTranscript(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	dispatcher := &recordingSecurityDispatcher{observed: make(chan struct{}), input: make(chan string, 1)}
	mgr := NewManager(&transcriptSecurityExec{observed: dispatcher.observed}, filepath.Join(repo, ".jig"))
	mgr.SetMonitors([]sentinel.MonitorDef{{Monitor: "prompt-injection", Spec: sentinel.MonitorSpec{Model: "test", Prompt: "policy"}, Dispatcher: dispatcher}})
	_, ctrl := mgr.Subscribe()
	wf := &workflow.Workflow{Meta: workflow.Meta{Name: "security"}, Defaults: workflow.Defaults{Security: workflow.SecurityConfig{BatchSize: 1}}, Steps: []workflow.Step{{ID: "agent", Type: workflow.StepAgent, Isolation: workflow.IsolationNone}}}
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case input := <-dispatcher.input:
		if !strings.Contains(input, "INJECT redirect") || !strings.Contains(input, "entry seq=1") {
			t.Fatalf("classifier input = %q", input)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("classifier was not called")
	}
	var finding SecurityFinding
	deadline := time.After(3 * time.Second)
	for finding.Monitor == "" {
		select {
		case event := <-ctrl:
			if got, ok := event.(SecurityFinding); ok {
				finding = got
			}
		case <-deadline:
			t.Fatal("security event was not published")
		}
	}
	if finding.RunID != run.ID || finding.StepID != "agent" || finding.Monitor != "prompt-injection" {
		t.Fatalf("finding = %+v", finding)
	}
	<-run.done
	findings, err := sentinel.ReadAll(datastore.FindingsPath(mgr.RunDir(run.ID)))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Monitor != "prompt-injection" {
		t.Fatalf("persisted findings = %+v", findings)
	}
}

func TestStepTier2Policy(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name   string
		st     *workflow.Step
		runDir string
		want   bool
	}{
		{"default on", &workflow.Step{Type: workflow.StepAgent}, "run", true},
		{"security off", &workflow.Step{Type: workflow.StepAgent, Security: workflow.StepSecurity{Enabled: &no}}, "run", false},
		{"tier2 off", &workflow.Step{Type: workflow.StepAgent, Security: workflow.StepSecurity{Tier2Enabled: &no}}, "run", false},
		{"explicit on", &workflow.Step{Type: workflow.StepAgent, Security: workflow.StepSecurity{Enabled: &yes, Tier2Enabled: &yes}}, "run", true},
		{"command", &workflow.Step{Type: workflow.StepCommand}, "run", false},
		{"persistence off", &workflow.Step{Type: workflow.StepAgent}, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := stepTier2Enabled(tc.st, tc.runDir); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestForEachChildUsesResolvedTier2Policy(t *testing.T) {
	for _, tc := range []struct {
		name, defaults, child string
		want                  bool
	}{
		{name: "default on", want: true},
		{name: "child opt out", child: "tier2_enabled = false"},
		{name: "child explicit opt in", defaults: "tier2_enabled = false", child: "tier2_enabled = true", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := singleChildTOML("")
			securityDefaults := "[defaults.security]\nbatch_size = 1\ndebounce_ms = 10\n" + tc.defaults + "\n"
			source = strings.Replace(source, "version = \"1\"\n", "version = \"1\"\n"+securityDefaults, 1)
			if tc.child != "" {
				source = strings.Replace(source, "skill = \"skills/analyze\"\n", "skill = \"skills/analyze\"\n  [step.security]\n  "+tc.child+"\n", 1)
			}
			wf, err := workflow.Decode(source, "")
			if err != nil {
				t.Fatal(err)
			}
			repo := t.TempDir()
			initRepo(t, repo)
			dispatcher := &recordingSecurityDispatcher{observed: make(chan struct{}), input: make(chan string, 1)}
			mgr := NewManager(&fanOutSecurityExec{observed: dispatcher.observed, wait: tc.want}, filepath.Join(repo, ".jig"))
			mgr.SetMonitors([]sentinel.MonitorDef{{Monitor: "prompt-injection", Spec: sentinel.MonitorSpec{Model: "test", Prompt: "policy"}, Dispatcher: dispatcher}})
			run, err := mgr.Start(wf)
			if err != nil {
				t.Fatal(err)
			}
			run.Wait()
			select {
			case input := <-dispatcher.input:
				if !tc.want {
					t.Fatalf("opted-out foreach child was classified: %q", input)
				}
				if !strings.Contains(input, "foreach-child-security-marker") {
					t.Fatalf("foreach classifier input = %q", input)
				}
			default:
				if tc.want {
					t.Fatal("eligible foreach child was not classified")
				}
			}
		})
	}
}

func TestRunOwnedSecurityDoesNotCrossRunsOrAccumulateSubscribers(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	dispatcher := &recordingSecurityDispatcher{observed: make(chan struct{}), input: make(chan string, 4)}
	mgr := NewManager(&pacedRunIDSecurityExec{}, filepath.Join(repo, ".jig"))
	mgr.SetMonitors([]sentinel.MonitorDef{{Monitor: "prompt-injection", Spec: sentinel.MonitorSpec{Model: "test", Prompt: "policy"}, Dispatcher: dispatcher}})
	wf := &workflow.Workflow{Meta: workflow.Meta{Name: "security-ownership"}, Defaults: workflow.Defaults{Security: workflow.SecurityConfig{BatchSize: 1}}, Steps: []workflow.Step{{ID: "same-step", Type: workflow.StepAgent, Isolation: workflow.IsolationNone}}}
	first, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	second, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	first.Wait()
	second.Wait()
	if len(mgr.subs) != 0 {
		t.Fatalf("run-owned monitoring registered %d manager subscribers", len(mgr.subs))
	}
	var inputs []string
	for {
		select {
		case input := <-dispatcher.input:
			inputs = append(inputs, input)
		default:
			goto drained
		}
	}
drained:
	if len(inputs) != 2 {
		t.Fatalf("classifier inputs = %d, want one per run: %#v", len(inputs), inputs)
	}
	for _, input := range inputs {
		hasFirst := strings.Contains(input, "run-marker-"+first.ID)
		hasSecond := strings.Contains(input, "run-marker-"+second.ID)
		if hasFirst == hasSecond {
			t.Fatalf("classifier window crossed or missed runs: %q", input)
		}
	}
}

func TestCriticalTier2FindingOnlyEscalatesCurrentExecution(t *testing.T) {
	wf := &workflow.Workflow{Meta: workflow.Meta{Name: "current-security"}, Steps: []workflow.Step{{ID: "agent", Type: workflow.StepAgent}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scheduler := newScheduler(wf, "run", make(chan schedMsg, 4), nil, &testExec{}, cancel, nil, "", "", "", func(RunSnapshot) {})
	state := scheduler.states["agent"]
	state.Status = step.StatusRunning
	state.Generation, state.Iteration, state.Attempt = 2, 3, 4
	scheduler.handleSecurityFinding(SecurityFinding{
		RunID: "run", StepID: "agent", Tier: string(sentinel.TierMonitor), Monitor: "prompt-injection",
		Severity: "critical", Fingerprint: "stale", Generation: 1, Iteration: 3, Attempt: 4,
	})
	if state.Status != step.StatusRunning {
		t.Fatalf("stale finding parked current execution: %s", state.Status)
	}
	scheduler.handleSecurityFinding(SecurityFinding{
		RunID: "run", StepID: "agent", Tier: string(sentinel.TierMonitor), Monitor: "prompt-injection",
		Severity: "critical", Fingerprint: "current", Generation: 2, Iteration: 3, Attempt: 4,
	})
	if state.Status != step.StatusAwaitingRecovery {
		t.Fatalf("current finding status = %s, want awaiting_recovery", state.Status)
	}
	_ = ctx
}
