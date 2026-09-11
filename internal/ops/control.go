package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/harness"
	"jig/internal/headless"
	"jig/internal/step"
	"jig/internal/workflow"
)

type ControlError struct {
	ExitCode int
	Message  string
}

func (e *ControlError) Error() string { return e.Message }

type ControlPreflight struct {
	RunDir   string
	Report   RunReport
	Workflow *workflow.Workflow
	Records  []engine.JournalRecord
}

func PreflightControl(root, runID string) (ControlPreflight, error) {
	runDir, err := datastore.ResolveRunDir(root, runID)
	if err != nil {
		return ControlPreflight{}, err
	}
	report, statusErr := InspectRun(root, runID)
	if statusErr != nil {
		return ControlPreflight{}, statusErr
	}
	switch report.State {
	case StateSucceeded, StateFailed:
		return ControlPreflight{}, fmt.Errorf("run %q is already settled", runID)
	case StateActive:
		return ControlPreflight{}, fmt.Errorf("run %q already has a live scheduler", runID)
	case StateOrphaned, StateCorrupt:
		return ControlPreflight{}, fmt.Errorf("run %q is %s", runID, report.State)
	}
	wf, err := engine.LoadWorkflowSnapshot(runDir)
	if err != nil {
		return ControlPreflight{}, fmt.Errorf("load captured workflow: %w", err)
	}
	records, err := engine.ReplayJournalRecords(runDir)
	if err != nil {
		return ControlPreflight{}, err
	}
	return ControlPreflight{RunDir: runDir, Report: report, Workflow: wf, Records: records}, nil
}

func ResumeRun(ctx context.Context, opts headless.Options, runID string) headless.Result {
	if opts.Manager == nil {
		err := fmt.Errorf("resume: manager is required")
		return headless.Result{ExitCode: headless.ExitUsage, Err: err}
	}
	preflight, err := PreflightControl(opts.Manager.Root(), runID)
	if err != nil {
		return headless.Result{ExitCode: headless.ExitFailed, Err: err}
	}
	if err := resumeEligibility(preflight, opts.OnRecovery); err != nil {
		code := headless.ExitFailed
		var controlErr *ControlError
		if errors.As(err, &controlErr) {
			code = controlErr.ExitCode
		}
		return headless.Result{ExitCode: code, Err: err}
	}
	live, ctrl := opts.Manager.Subscribe()
	bufferedCtrl, stopBuffer := bufferControlEvents(ctrl)
	defer stopBuffer()
	run, err := opts.Manager.Resume(runID)
	if err != nil {
		return headless.Result{ExitCode: headless.ExitFailed, Err: err}
	}
	if opts.OnRunStart != nil {
		opts.OnRunStart(run.ID, preflight.Workflow)
	}
	opts.Workflow = preflight.Workflow
	opts.WorkflowPath = ""
	return headless.Supervise(ctx, opts, preflight.Workflow, run, live, bufferedCtrl)
}

func resumeEligibility(preflight ControlPreflight, recoveryAction string) error {
	recoverable := false
	for _, s := range preflight.Report.Steps {
		switch s.Status {
		case step.StatusRunning, step.StatusValidating, step.StatusAwaitingRecovery, step.StatusAwaitingIntegration:
			recoverable = true
		case step.StatusAwaitingReview, step.StatusNeedsInput, step.StatusStopped:
			return &ControlError{ExitCode: headless.ExitGate, Message: fmt.Sprintf("step %q is parked at %s; open the TUI", s.ID, s.Status)}
		}
	}
	for _, wait := range preflight.Report.WaitingOn {
		switch wait.Kind {
		case "review", "prompt", "input", "question", "stopped", "final_merge":
			return &ControlError{ExitCode: headless.ExitGate, Message: fmt.Sprintf("run is waiting on %s; open the TUI", wait.Kind)}
		}
	}
	if !recoverable {
		return fmt.Errorf("run has no automation-safe recoverable park")
	}
	if recoveryAction == headless.RecoveryResume {
		latest := map[string]engine.RecoveryRequest{}
		for _, record := range preflight.Records {
			if request, ok := record.Event.(engine.RecoveryRequest); ok {
				latest[request.StepID] = request
			}
		}
		for _, s := range preflight.Report.Steps {
			if s.Status == step.StatusAwaitingRecovery {
				request, ok := latest[s.ID]
				if !ok || !request.CanResume {
					return &ControlError{ExitCode: headless.ExitGate, Message: fmt.Sprintf("step %q does not have a resumable recovery session", s.ID)}
				}
			} else if s.Status == step.StatusRunning || s.Status == step.StatusValidating {
				if !interruptedCanResume(preflight, s.ID) {
					return &ControlError{ExitCode: headless.ExitGate, Message: fmt.Sprintf("interrupted step %q does not have a resumable session", s.ID)}
				}
			}
		}
	}
	return nil
}

func interruptedCanResume(preflight ControlPreflight, stepID string) bool {
	info, err := datastore.ReadSession(preflight.RunDir, stepID)
	if err != nil || info.SessionID == "" {
		return false
	}
	var wfStep *workflow.Step
	for i := range preflight.Workflow.Steps {
		if preflight.Workflow.Steps[i].ID == stepID {
			wfStep = &preflight.Workflow.Steps[i]
			break
		}
	}
	if wfStep == nil || wfStep.Type != workflow.StepAgent {
		return false
	}
	h, err := harness.For(wfStep.Backend, wfStep.Transport)
	if err != nil || !h.Capabilities().Has(harness.CapSessionResume) {
		return false
	}
	if wfStep.Isolation == workflow.IsolationWorktree {
		path := filepath.Join(filepath.Dir(preflight.RunDir), "..", "worktrees", preflight.Report.RunID, stepID)
		stat, statErr := os.Stat(filepath.Clean(path))
		return statErr == nil && stat.IsDir()
	}
	return true
}

type ResetStep struct {
	ID     string      `json:"id"`
	Status step.Status `json:"status"`
}

type ResetPreview struct {
	RunID        string       `json:"run_id"`
	Target       string       `json:"target"`
	Steps        []ResetStep  `json:"steps"`
	OutsideParks []WaitReport `json:"outside_parks"`
}

func PreviewReset(root, runID, target string) (ResetPreview, error) {
	runDir, err := datastore.ResolveRunDir(root, runID)
	if err != nil {
		return ResetPreview{}, err
	}
	records, err := engine.ReplayJournalRecords(runDir)
	if err != nil {
		return ResetPreview{}, err
	}
	if len(records) == 0 {
		return ResetPreview{}, fmt.Errorf("run %q has no readable journal records", runID)
	}
	report := FoldStatus(runID, records, false)
	if report.State == StateSucceeded || report.State == StateFailed {
		return ResetPreview{}, fmt.Errorf("run %q is already settled", runID)
	}
	wf, err := engine.LoadWorkflowSnapshot(runDir)
	if err != nil {
		return ResetPreview{}, fmt.Errorf("load captured workflow: %w", err)
	}
	closure := engine.ResetClosure(wf, target)
	if len(closure) == 0 {
		return ResetPreview{}, fmt.Errorf("unknown reset target %q", target)
	}
	byID := make(map[string]step.Status, len(report.Steps))
	for _, s := range report.Steps {
		byID[s.ID] = s.Status
	}
	preview := ResetPreview{RunID: runID, Target: target, Steps: make([]ResetStep, 0, len(closure)), OutsideParks: []WaitReport{}}
	closureSet := make(map[string]bool, len(closure))
	for _, id := range closure {
		closureSet[id] = true
		preview.Steps = append(preview.Steps, ResetStep{ID: id, Status: byID[id]})
	}
	for _, wait := range report.WaitingOn {
		if wait.StepID != "" && !closureSet[wait.StepID] {
			preview.OutsideParks = append(preview.OutsideParks, wait)
		}
	}
	return preview, nil
}

func ApplyReset(ctx context.Context, opts headless.Options, runID, target string) (engine.ResetResult, headless.Result) {
	if opts.Manager == nil {
		err := fmt.Errorf("reset: manager is required")
		return engine.ResetResult{}, headless.Result{ExitCode: headless.ExitUsage, Err: err}
	}
	preflight, err := PreflightControl(opts.Manager.Root(), runID)
	if err != nil {
		return engine.ResetResult{}, headless.Result{ExitCode: headless.ExitFailed, Err: err}
	}
	closure := engine.ResetClosure(preflight.Workflow, target)
	if len(closure) == 0 {
		err := fmt.Errorf("unknown reset target %q", target)
		return engine.ResetResult{}, headless.Result{ExitCode: headless.ExitFailed, Err: err}
	}
	closureSet := make(map[string]bool, len(closure))
	for _, id := range closure {
		closureSet[id] = true
	}
	for _, s := range preflight.Report.Steps {
		if !closureSet[s.ID] && !terminalStepStatus(s.Status) {
			err := fmt.Errorf("unfinished step %q at %s lies outside reset closure", s.ID, s.Status)
			return engine.ResetResult{}, headless.Result{ExitCode: headless.ExitGate, Err: err}
		}
	}
	doctor := Doctor(DoctorOptions{Root: opts.Manager.Root(), Workflow: preflight.Workflow, CI: true})
	if !doctor.OK {
		for _, check := range doctor.Checks {
			if check.Status == CheckFail {
				err := fmt.Errorf("doctor %s failed (%s): %s", check.ID, check.Scope, check.Message)
				return engine.ResetResult{}, headless.Result{ExitCode: headless.ExitGate, Err: err}
			}
		}
	}
	live, ctrl := opts.Manager.Subscribe()
	bufferedCtrl, stopBuffer := bufferControlEvents(ctrl)
	defer stopBuffer()
	run, err := opts.Manager.Resume(runID)
	if err != nil {
		return engine.ResetResult{}, headless.Result{ExitCode: headless.ExitFailed, Err: err}
	}
	if opts.OnRunStart != nil {
		opts.OnRunStart(run.ID, preflight.Workflow)
	}
	reset, err := run.Reset(target)
	if err != nil {
		run.Cancel()
		run.Wait()
		return engine.ResetResult{}, headless.Result{ExitCode: headless.ExitFailed, Err: err}
	}
	opts.Workflow = preflight.Workflow
	opts.WorkflowPath = ""
	return reset, headless.Supervise(ctx, opts, preflight.Workflow, run, live, bufferedCtrl)
}

func bufferControlEvents(input <-chan engine.Event) (<-chan engine.Event, func()) {
	output := make(chan engine.Event)
	done := make(chan struct{})
	go func() {
		defer close(output)
		var queue []engine.Event
		for {
			var send chan engine.Event
			var next engine.Event
			if len(queue) > 0 {
				send, next = output, queue[0]
			}
			select {
			case <-done:
				return
			case event := <-input:
				queue = append(queue, event)
			case send <- next:
				queue = queue[1:]
			}
		}
	}()
	return output, func() { close(done) }
}

func terminalStepStatus(status step.Status) bool {
	switch status {
	case step.StatusSucceeded, step.StatusFailed, step.StatusSkipped:
		return true
	default:
		return false
	}
}
