package headless

import (
	"context"
	"errors"
	"fmt"

	"jig/internal/engine"
	"jig/internal/workflow"
)

// Run loads (if needed), starts, drains events under Policy, and returns a
// settled Result with an exit code. It never hangs on a human gate: unexpected
// parks Cancel + wait for RunFinished; configured policies use native APIs.
func Run(ctx context.Context, opts Options) Result {
	w := newWriter(opts)

	wf, err := resolveWorkflow(opts)
	if err != nil {
		w.errf("%v", err)
		return Result{ExitCode: ExitFailed, Err: err, Envelope: Envelope{
			OK: false, Error: &ErrorInfo{Code: "load_error", Message: err.Error()},
		}}
	}
	if opts.Manager == nil {
		err := fmt.Errorf("headless: Manager is required")
		w.errf("%v", err)
		return Result{ExitCode: ExitUsage, Err: err}
	}

	policy := &Policy{
		ApproveMerge: opts.ApproveMerge,
		DiscardMerge: opts.DiscardMerge,
		CI:           opts.CI,
	}
	hasMerge := opts.ApproveMerge || opts.DiscardMerge || opts.CI
	w.emitGateWarnings(wf, opts.Manager.Root(), hasMerge)

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	live, ctrl := opts.Manager.Subscribe()
	go drainLive(live)

	run, err := opts.Manager.Start(wf)
	if err != nil {
		w.errf("%v", err)
		return Result{ExitCode: ExitFailed, Err: err, Envelope: Envelope{
			OK: false, Workflow: wf.Meta.Name,
			Error: &ErrorInfo{Code: "start_error", Message: err.Error()},
		}}
	}
	w.runID(run.ID)

	var terminal error
	cancelled := false
	cancelOnce := func(reason error) {
		if cancelled {
			return
		}
		cancelled = true
		if terminal == nil {
			terminal = reason
		}
		run.Cancel()
	}

	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				cancelOnce(&TimeoutError{Timeout: opts.Timeout})
			} else {
				cancelOnce(&InterruptedError{})
			}
			// Fall through: wait for RunFinished via ctrl (or Wait below).
			snap := waitFinished(ctrl, run)
			return finishResult(opts.Manager, wf, w, snap, terminal)

		case ev := <-ctrl:
			if id := eventRunID(ev); id != "" && id != run.ID {
				continue
			}
			w.event(ev)

			if err := policy.Handle(run, ev); err != nil {
				cancelOnce(err)
				// Do not return yet — wait for RunFinished (D15).
				continue
			}

			if fin, ok := ev.(engine.RunFinished); ok && fin.RunID == run.ID {
				snap := run.Snapshot()
				return finishResult(opts.Manager, wf, w, finishedSnap(snap, fin), terminal)
			}
		}
	}
}

func resolveWorkflow(opts Options) (*workflow.Workflow, error) {
	if opts.Workflow != nil {
		return opts.Workflow, nil
	}
	if opts.WorkflowPath == "" {
		return nil, fmt.Errorf("headless: workflow path required")
	}
	return workflow.Load(opts.WorkflowPath)
}

func drainLive(live <-chan engine.Event) {
	for range live {
		// Drop: file is truth; live is liveness only. Draining prevents the
		// live buffer from stalling the scheduler under drop-on-full pressure.
	}
}

func eventRunID(ev engine.Event) string {
	switch e := ev.(type) {
	case engine.RunStarted:
		return e.RunID
	case engine.RunFinished:
		return e.RunID
	case engine.StepStatus:
		return e.RunID
	case engine.ReviewRequest:
		return e.RunID
	case engine.PromptRequest:
		return e.RunID
	case engine.InputRequest:
		return e.RunID
	case engine.AgentQuestion:
		return e.RunID
	case engine.RecoveryRequest:
		return e.RunID
	case engine.IntegrationConflictRequest:
		return e.RunID
	case engine.FinalMergeRequest:
		return e.RunID
	case engine.RunError:
		return e.RunID
	case engine.GateResult:
		return e.RunID
	case engine.RouteSelected:
		return e.RunID
	case engine.RouteCapExceeded:
		return e.RunID
	case engine.SecurityFinding:
		return e.RunID
	case engine.StepsReset:
		return e.RunID
	case engine.ReviewSubmitted:
		return e.RunID
	case engine.AgentQuestionResolved:
		return e.RunID
	default:
		return ""
	}
}

// waitFinished drains ctrl until RunFinished for run, or falls back to Wait
// if the event was dropped. Always settles.
func waitFinished(ctrl <-chan engine.Event, run *engine.Run) finishedView {
	done := make(chan engine.RunSnapshot, 1)
	go func() { done <- run.Wait() }()

	for {
		select {
		case ev := <-ctrl:
			if fin, ok := ev.(engine.RunFinished); ok && fin.RunID == run.ID {
				snap := run.Wait() // ensure scheduler exited
				return finishedSnap(snap, fin)
			}
		case snap := <-done:
			return finishedView{snap: snap, fin: engine.RunFinished{RunID: snap.ID, Failed: snap.Failed}}
		}
	}
}

type finishedView struct {
	snap engine.RunSnapshot
	fin  engine.RunFinished
}

func finishedSnap(snap engine.RunSnapshot, fin engine.RunFinished) finishedView {
	return finishedView{snap: snap, fin: fin}
}

func finishResult(mgr *engine.Manager, wf *workflow.Workflow, w *writer, view finishedView, terminal error) Result {
	env := buildEnvelope(mgr, wf, view.snap, view.fin, terminal)
	w.finish(env)
	if terminal != nil {
		w.errf("%v", terminal)
	}
	return Result{
		ExitCode: exitCode(terminal, view.fin),
		Envelope: env,
		Err:      terminal,
	}
}

func exitCode(terminal error, fin engine.RunFinished) int {
	switch {
	case isTimeout(terminal):
		return ExitTimeout
	case isInterrupted(terminal):
		return ExitInterrupted
	case isGate(terminal):
		return ExitGate
	case terminal != nil:
		return ExitFailed
	case fin.Failed:
		return ExitFailed
	default:
		return ExitOK
	}
}
