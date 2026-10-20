package engine

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/sentinel"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

type runSecurity struct {
	signals chan sentinel.StepSignal
	cancel  context.CancelFunc
	done    chan struct{}
	once    sync.Once
}

func (r *runSecurity) stop() {
	if r == nil {
		return
	}
	r.once.Do(func() { r.cancel(); <-r.done })
}

func tier2Eligible(st *workflow.Step, runDir datastore.Run, monitors []sentinel.MonitorDef) bool {
	return len(monitors) > 0 && stepTier2Enabled(st, runDir)
}

func stepTier2Enabled(st *workflow.Step, runDir datastore.Run) bool {
	if !hasAgentSession(st) || !runDir.Enabled() {
		return false
	}
	securityOn := st.Security.Enabled == nil || *st.Security.Enabled
	tier2On := st.Security.Tier2Enabled == nil || *st.Security.Tier2Enabled
	return securityOn && tier2On
}

func workflowUsesTier2(wf *workflow.Workflow, runDir datastore.Run, monitors []sentinel.MonitorDef) bool {
	if wf == nil {
		return false
	}
	for i := range wf.Steps {
		if tier2Eligible(&wf.Steps[i], runDir, monitors) {
			return true
		}
	}
	return false
}

func startRunSecurity(parent context.Context, wf *workflow.Workflow, runID string, runDir datastore.Run,
	monitors []sentinel.MonitorDef, subs []sub, inbox chan<- schedMsg, resume bool) *runSecurity {
	if !workflowUsesTier2(wf, runDir, monitors) {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	runtime := &runSecurity{signals: make(chan sentinel.StepSignal, 128), cancel: cancel, done: make(chan struct{})}
	notify := func(f sentinel.Finding) {
		sf := SecurityFinding{
			RunID: f.RunID, StepID: f.StepID, Tier: string(f.Tier), Monitor: f.Monitor,
			Severity: string(f.Severity), Action: string(f.Action), Fingerprint: f.Fingerprint,
			Iteration: f.Iteration, Attempt: f.Attempt, Generation: f.Generation,
		}
		fanOutCtrl(subs, sf)
		select {
		case inbox <- securityFindingMsg{sf: sf}:
		default:
		}
	}
	findingsPath := runDir.Findings()
	sink, err := sentinel.NewWriter(findingsPath)
	if err != nil {
		notify(sentinel.Finding{RunID: runID, Tier: sentinel.TierMonitor, Monitor: "finding-persistence-failed",
			Severity: sentinel.SeverityLow, Action: sentinel.ActionObserved,
			Fingerprint: sentinel.NewFingerprint(runID, "finding-persistence-failed", "open")})
	}
	options := sentinel.SupervisorOptions{
		BatchSize:      wf.Defaults.Security.BatchSize,
		Debounce:       time.Duration(wf.Defaults.Security.DebounceMs) * time.Millisecond,
		BudgetUSD:      wf.Defaults.Security.FleetBudgetUSD,
		ConcurrencyCap: wf.Defaults.Security.ConcurrencyCap,
		StatePath:      runDir.SecurityMonitorState(), FindingsPath: findingsPath, Resume: resume,
	}
	supervisor := sentinel.NewSupervisor(runID, runtime.signals, sink, monitors,
		func(stepID string) string { return runDir.Step(stepID).Transcript() }, notify, options)
	go func() { defer close(runtime.done); supervisor.Run(ctx) }()
	return runtime
}

// writeStepSecurityMarkdown refreshes steps/<stepID>/security.md from the
// run's findings.jsonl (the source of truth) so the step's on-disk artifacts
// carry a human-readable security report alongside input.md/output.md,
// visible in the same file list. Best-effort: persistence-off runs and
// findings read errors are skipped since findings.jsonl is authoritative; a
// failed write is reported as a RunError so a stale report is not silent.
func (s *scheduler) writeStepSecurityMarkdown(stepID string) {
	if !s.runDir.Enabled() || stepID == "" {
		return
	}
	findings, err := sentinel.ReadAll(s.runDir.Findings())
	if err != nil {
		return
	}
	var stepFindings []sentinel.Finding
	for _, f := range findings {
		if f.StepID == stepID {
			stepFindings = append(stepFindings, f)
		}
	}
	if err := s.runDir.Step(stepID).Ensure(); err != nil {
		s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("write security report for %q: %v", stepID, err)})
		return
	}
	path := s.runDir.Step(stepID).Security()
	if err := os.WriteFile(path, []byte(sentinel.RenderMarkdown(stepID, stepFindings)), 0o644); err != nil {
		s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("write security report %s: %v", path, err)})
	}
}

// handleSecurityFinding escalates critical security findings to the recovery
// gate. Non-critical findings are silently recorded (seenEscalations) so
// duplicate fingerprints remain no-ops even if they later become critical.
// A fingerprint that already triggered enterRecovery is skipped (rate-limit).
// If the step is already terminal or the run is done, no recovery action is taken.
func (s *scheduler) handleSecurityFinding(sf SecurityFinding) {
	// Always record the fingerprint to prevent duplicate escalations.
	if s.seenEscalations[sf.Fingerprint] {
		return
	}
	s.seenEscalations[sf.Fingerprint] = true
	s.securityFindings++
	s.writeStepSecurityMarkdown(sf.StepID)

	if sf.Severity != "critical" {
		return
	}
	state, ok := s.states[sf.StepID]
	if !ok {
		return // unknown step — finding already recorded above
	}
	if sf.Generation != state.Generation || sf.Iteration != state.Iteration || sf.Attempt != state.Attempt {
		return
	}
	switch state.Status {
	case step.StatusRunning, step.StatusNeedsInput:
		// Blockable: set a descriptive error and park for human recovery decision.
		if state.Result == nil {
			state.Result = &step.Result{Status: step.StatusFailed}
		}
		if state.Result.Err == "" {
			state.Result.Err = "security escalation: critical finding from " + sf.Monitor
		}
		s.enterRecovery(sf.StepID)
		// Reap the still-live worker: cancel its context so a Question-blocked (or
		// otherwise in-flight) worker unwinds and its stepDoneMsg decrements
		// inFlight. The step is already parked at StatusAwaitingRecovery, so the
		// completing worker is recorded-and-kept-parked (see handle(stepDoneMsg))
		// rather than double-counted when the human later retries.
		// The registry entry goes with it, so a replacement dispatch registers
		// its own and the reaped completion cannot cancel that.
		if cancel, ok := s.stepCancels[sf.StepID]; ok {
			cancel()
			delete(s.stepCancels, sf.StepID)
			s.rt(sf.StepID).reaped = true
		}
		// StatusAwaitingRecovery: already parked; finding recorded above.
		// Terminal states (succeeded, failed, skipped, stopped): record only.
	}
}
