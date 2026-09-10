package engine

import (
	"context"
	"sync"
	"time"

	"jig/internal/datastore"
	"jig/internal/sentinel"
	"jig/internal/workflow"
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

func tier2Eligible(st *workflow.Step, runDir string, monitors []sentinel.MonitorDef) bool {
	return len(monitors) > 0 && stepTier2Enabled(st, runDir)
}

func stepTier2Enabled(st *workflow.Step, runDir string) bool {
	if st == nil || st.Type != workflow.StepAgent || runDir == "" {
		return false
	}
	securityOn := st.Security.Enabled == nil || *st.Security.Enabled
	tier2On := st.Security.Tier2Enabled == nil || *st.Security.Tier2Enabled
	return securityOn && tier2On
}

func workflowUsesTier2(wf *workflow.Workflow, runDir string, monitors []sentinel.MonitorDef) bool {
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

func startRunSecurity(parent context.Context, wf *workflow.Workflow, runID, runDir string,
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
	findingsPath := datastore.FindingsPath(runDir)
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
		StatePath:      datastore.SecurityMonitorStatePath(runDir), FindingsPath: findingsPath, Resume: resume,
	}
	supervisor := sentinel.NewSupervisor(runID, runtime.signals, sink, monitors,
		func(stepID string) string { return datastore.TranscriptPath(runDir, stepID) }, notify, options)
	go func() { defer close(runtime.done); supervisor.Run(ctx) }()
	return runtime
}
