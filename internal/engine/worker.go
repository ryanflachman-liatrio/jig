package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ryanflachman-liatrio/jig/internal/sentinel"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// hasUserInputs reports whether st declares any from="user" inputs.
func hasUserInputs(st *workflow.Step) bool {
	for _, inp := range st.Inputs {
		if inp.From == "user" {
			return true
		}
	}
	return false
}

// failDispatch fails a step before it took any capacity and parks it for a
// recovery decision.
func (s *scheduler) failDispatch(stepID, format string, args ...any) {
	s.states[stepID].Result = &step.Result{Status: step.StatusFailed, Err: fmt.Sprintf(format, args...)}
	s.enterRecovery(stepID)
}

// dispatch starts one ready step through its stepKinds entry: worker-backed
// kinds launch a goroutine that reports back with a stepDoneMsg, and a human
// gate parks on operator input.
func (s *scheduler) dispatch(ctx context.Context, st *workflow.Step) {
	// A foreach family template never executes directly — dispatching it
	// expands its bounded list into runtime children instead (see fanout.go).
	// This check comes before the type-keyed strategy lookup because a
	// foreach template's Type is still "agent" or "command".
	if st.ForEach != nil {
		s.dispatchForEach(ctx, st)
		return
	}
	kind, ok := stepKinds[st.Type]
	if !ok {
		// Validation rejects unknown types, so this is a programming error; fail
		// the step rather than leaving it dispatched with no worker.
		s.failDispatch(st.ID, "step %q: unsupported step type %q", st.ID, st.Type)
		return
	}
	kind.dispatch(s, ctx, st)
}

// dispatchWorker starts an agent or command step's worker goroutine. It is
// the dispatch for every worker-backed step kind (see stepKinds).
func (s *scheduler) dispatchWorker(ctx context.Context, st *workflow.Step) {
	workspace, err := s.acquireExecutionWorkspace(st)
	if err != nil {
		// A reader view is the persisted execution contract. Falling back to the
		// caller's checkout here would let a failed snapshot silently read a
		// different repository state.
		s.failDispatch(st.ID, "acquire execution workspace for step %q: %v", st.ID, err)
		return
	}
	worktreePath := ""
	if workspace.Kind == executionWorkspaceMutation {
		worktreePath = workspace.Dir
	}
	releaseReaderView := func() {
		if workspace.Kind == executionWorkspaceReadOnlyView {
			s.releaseExecutionViewForStep(st.ID)
		}
	}

	// Ensure the per-step directory exists before the executor writes its
	// transcript there. The manifest writer also calls StepDir, but only on the
	// step's terminal event — too late for the runner's mid-execution transcript
	// writes, which would otherwise fail with "no such file or directory".
	if s.runDir.Enabled() {
		if err := s.runDir.Step(st.ID).Ensure(); err != nil {
			releaseReaderView()
			s.failDispatch(st.ID, "create step dir for %q: %v", st.ID, err)
			return
		}
	}
	// Clear a stale session before the durable running transition. If the process
	// dies after that transition, session.json can only belong to this dispatch.
	if hasAgentSession(st) && s.rt(st.ID).resumeSession == "" {
		if err := s.runDir.Step(st.ID).ClearSession(); err != nil {
			releaseReaderView()
			s.failDispatch(st.ID, "clear stale session for step %q: %v", st.ID, err)
			return
		}
	}

	from := s.states[st.ID].Status
	if !s.transition(st.ID, from, step.StatusRunning) {
		releaseReaderView()
		return
	}
	s.acquireSlots(st)

	runID, stepID := s.runID, st.ID
	subs := s.subs
	inbox := s.inbox
	executionCoords := sentinel.ExecutionCoordinates{
		Generation: s.states[st.ID].Generation,
		Iteration:  s.states[st.ID].Iteration,
		Attempt:    s.states[st.ID].Attempt,
	}
	rep := &reporter{
		subs:   subs,
		stepID: stepID,
		inbox:  inbox,
		ev: func(e Event) {
			// Tag the event with run/step IDs and fan-out without holding any lock.
			switch e := e.(type) {
			case StepOutput:
				e.RunID, e.StepID = runID, stepID
				fanOutLive(subs, e)
			case StepToolCall:
				e.RunID, e.StepID = runID, stepID
				fanOutLive(subs, e)
			case StepMessage:
				e.RunID, e.StepID = runID, stepID
				fanOutLive(subs, e)
			case SecurityFinding:
				// Must-not-drop: rides ctrl, not live.
				e.RunID, e.StepID = runID, stepID
				e.Generation, e.Iteration, e.Attempt = executionCoords.Generation, executionCoords.Iteration, executionCoords.Attempt
				fanOutCtrl(subs, e)
				// Also notify the scheduler so critical findings can escalate
				// to the recovery gate. Non-blocking: the scheduler's inbox is
				// buffered (64) and rarely full.
				select {
				case inbox <- securityFindingMsg{sf: e}:
				default:
				}
			}
		},
	}
	if stepTier2Enabled(st, s.runDir) && s.securitySignals != nil {
		allowlist := append([]string(nil), st.Security.OutboundAllowlist...)
		rep.security = func(seq int) {
			signal := sentinel.StepSignal{RunID: runID, StepID: stepID, Seq: seq, ExecutionCoordinates: executionCoords, OutboundAllowlist: allowlist}
			select {
			case s.securitySignals <- signal:
			default:
			}
		}
	}
	var artifactDir, transcriptPath string
	if s.runDir.Enabled() {
		artifactDir = s.runDir.Artifacts()
		transcriptPath = s.runDir.Step(st.ID).Transcript()
	}
	// failAcquired fails a step whose slots are already charged but whose
	// worker never started.
	failAcquired := func(err error) {
		releaseReaderView()
		s.releaseSlots(st)
		s.states[st.ID].Result = &step.Result{Status: step.StatusFailed, Err: err.Error()}
		s.applyFailurePolicy(st.ID, st)
	}
	secrets, err := s.resolveSecrets(st)
	if err != nil {
		failAcquired(err)
		return
	}
	if err := s.resolveAllInputs(st, workspace.Dir); err != nil {
		failAcquired(err)
		return
	}
	if err := s.snapshotInputs(st.ID, s.rt(st.ID).preResolvedInputs); err != nil {
		failAcquired(err)
		return
	}
	req := s.buildRequest(st, runID, worktreePath, workspace.Dir, artifactDir, transcriptPath)
	req.Secrets = secrets
	req.NetworkRequest = func() {
		select {
		case s.inbox <- networkRequestMsg{stepID: st.ID}:
		default:
		}
	}
	s.rt(st.ID).preResolvedInputs = nil
	if sess := s.rt(st.ID).resumeSession; sess != "" {
		req.ResumeSessionID = sess
		req.Message = s.rt(st.ID).message
		s.rt(st.ID).resumeSession = ""
		s.rt(st.ID).message = ""
		s.rt(st.ID).sessionResumeInFlight = true
	} else {
		s.rt(st.ID).sessionResumeInFlight = false
		// A fresh attempt may resume after a prior dead-session park; clear the
		// force-false flag so a newly captured SessionID can offer resume again.
		s.rt(st.ID).failedSessionResume = false
	}
	// Clear the structured-output cache so block_on and when-guards always
	// see fresh output after a re-run rather than a stale cached decode.
	s.rt(st.ID).structured = nil

	// Give this worker its own child context so Run.Stop can cancel it
	// independently of the run context and its siblings (spec 07 B1). The child is
	// cancelled either by handleStop (a deliberate stop) or when the worker's
	// stepDoneMsg is handled (normal completion) — the latter releases the context
	// resources. Cancelling the run context still cascades to every child.
	stepCtx, stepCancel := context.WithCancel(ctx)
	if st.Timeout.Duration > 0 {
		stepCtx, stepCancel = context.WithTimeout(ctx, st.Timeout.Duration)
	}
	s.stepCancels[st.ID] = stepCancel
	deadline, _ := stepCtx.Deadline()

	go func() {
		result, err := s.exec.Execute(stepCtx, req, rep)
		timedOut := stepCtx.Err() == context.DeadlineExceeded
		// Deliver the result, but never block forever: once the run context is
		// cancelled the scheduler has stopped draining the inbox, so an
		// unconditional send would strand this goroutine (the inbox is only
		// buffered to 64, far below max_parallel). Select on ctx (the *run*
		// context, not stepCtx) so a single-step Stop — which cancels only
		// stepCtx while the run keeps draining — still delivers its stepDoneMsg
		// and transitions the step to StatusStopped.
		select {
		case s.inbox <- stepDoneMsg{stepID: stepID, result: result, err: err, timedOut: timedOut, deadline: deadline}:
		case <-ctx.Done():
		}
	}()
}

type inputSnapshot struct {
	Label  string `json:"label"`
	SHA256 string `json:"sha256"`
	Path   string `json:"path,omitempty"`
}

func (s *scheduler) snapshotInputs(stepID string, inputs []ResolvedInput) error {
	if !s.runDir.Enabled() {
		return nil
	}
	state := s.states[stepID]
	stepDir := s.runDir.Step(stepID)
	if err := stepDir.Ensure(); err != nil {
		return fmt.Errorf("create input snapshot directory: %w", err)
	}
	dir := stepDir.Dir()
	snapshots := make([]inputSnapshot, 0, len(inputs))
	for i := range inputs {
		data := []byte(inputs[i].Value)
		path := ""
		if !inputs[i].Ref.Inline && inputs[i].Value != "" {
			if fileData, readErr := os.ReadFile(inputs[i].Value); readErr == nil {
				data = fileData
				path = filepath.Join(dir, fmt.Sprintf("input-generation-%03d-iteration-%03d-attempt-%03d-%02d", state.Generation, state.Iteration, state.Attempt, i+1))
				if err := os.WriteFile(path, data, 0o600); err != nil {
					return fmt.Errorf("snapshot input %d: %w", i+1, err)
				}
				inputs[i].SnapshotPath = path
				inputs[i].Value = path
			}
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(data))
		inputs[i].SHA256 = digest
		snapshots = append(snapshots, inputSnapshot{Label: inputs[i].Ref.String(), SHA256: digest, Path: path})
	}
	data, err := json.MarshalIndent(snapshots, "", "  ")
	if err != nil {
		return fmt.Errorf("encode input snapshots: %w", err)
	}
	manifestPath := filepath.Join(dir, fmt.Sprintf("inputs-generation-%03d-iteration-%03d-attempt-%03d.json", state.Generation, state.Iteration, state.Attempt))
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		return fmt.Errorf("write input snapshot manifest: %w", err)
	}
	return nil
}

func (s *scheduler) resolveSecrets(st *workflow.Step) (map[string]string, error) {
	if len(st.Secrets) == 0 {
		return nil, nil
	}
	if s.secretResolver == nil {
		return nil, fmt.Errorf("step %q requires named secrets but no secret resolver is configured", st.ID)
	}
	values := make(map[string]string, len(st.Secrets))
	for _, name := range st.Secrets {
		value, err := s.secretResolver(name)
		if err != nil {
			return nil, fmt.Errorf("resolve secret %q for step %q: %w", name, st.ID, err)
		}
		if value == "" {
			return nil, fmt.Errorf("resolve secret %q for step %q: empty value", name, st.ID)
		}
		values[name] = value
	}
	return values, nil
}

// resolveAllInputs appends ResolvedInput entries for every non-user input
// into s.rt(st.ID).preResolvedInputs. User inputs are already present from the
// prompt-collection flow (handle(userInputMsg)); this fills the remaining refs.
// Literal paths are anchored to the dispatch snapshot before snapshotInputs
// copies them, so the worker receives the same immutable bytes the scheduler
// observed rather than a path resolved later from an unrelated checkout.
func (s *scheduler) resolveAllInputs(st *workflow.Step, executionDir string) error {
	for _, inp := range st.Inputs {
		if inp.From == "user" {
			continue // already collected via prompt flow
		}

		var value string

		switch {
		case inp.Ref != "" && inp.Artifact != "":
			if depState := s.states[inp.Ref]; depState != nil && depState.Result != nil {
				value = depState.Result.Artifacts[inp.Artifact]
			}

		case inp.Ref != "" && len(inp.RefField) > 0:
			// @step.field — extract from the dependency's structured output.
			if cur, err := s.structuredField(inp.Ref, inp.RefField); err == nil {
				switch v := cur.(type) {
				case string:
					value = v
				case bool:
					if v {
						value = "true"
					} else {
						value = "false"
					}
				case nil:
					// missing field — validation already caught dangling refs; pass empty
				default:
					if b, err := json.Marshal(v); err == nil {
						value = string(b)
					}
				}
			}

		case inp.Ref != "":
			// bare @step — use the dependency's artifact output file path.
			if depState := s.states[inp.Ref]; depState != nil && depState.Result != nil {
				value = depState.Result.OutputPath
			}

		case inp.Path != "":
			var err error
			value, err = workflow.ExecutionPath(executionDir, inp.Path)
			if err != nil {
				return fmt.Errorf("resolve input path %q: %w", inp.Path, err)
			}
		}

		s.rt(st.ID).preResolvedInputs = append(s.rt(st.ID).preResolvedInputs, ResolvedInput{
			Ref:   inp,
			Value: value,
		})
	}
	return nil
}

// buildRequest constructs the StepRequest for a dispatch. It must be called
// after resolveAllInputs so that preResolvedInputs contains all inputs
// (both user and non-user).
func (s *scheduler) buildRequest(
	st *workflow.Step,
	runID, worktreePath, executionDir, artifactDir, transcriptPath string,
) StepRequest {
	state := s.states[st.ID]
	// Agent steps get the engine-assembled "Workflow context" preamble; command
	// and review steps carry none, leaving their prompt untouched. An agent step
	// with inject_context off (explicitly, or inherited from [defaults]) also
	// opts out, dispatching with a byte-identical no-context prompt.
	var workflowContext string
	if hasAgentSession(st) && st.InjectContextEnabled() {
		workflowContext = s.buildStepContext(st).Render()
	}
	// Activate the Tier-1 guard for agent steps unless explicitly disabled.
	// Security is on by default (nil Enabled = on); resolved after applyDefaults.
	var guard *sentinel.Guard
	var findingsPath string
	if st.Tier1Enabled() {
		guard = sentinel.NewGuard(st.Security.OutboundAllowlist)
		if s.runDir.Enabled() {
			findingsPath = s.runDir.Findings()
		}
	}

	req := StepRequest{
		RunID:           runID,
		Step:            st,
		Inputs:          s.rt(st.ID).preResolvedInputs,
		Feedback:        s.rt(st.ID).feedback,
		WorkflowContext: workflowContext,
		ArtifactDir:     artifactDir,
		Worktree:        worktreePath,
		ExecutionDir:    executionDir,
		RepoRoot:        s.repoRoot,
		TranscriptPath:  transcriptPath,
		StepDir:         s.runDir.Step(st.ID),
		Iteration:       state.Iteration,
		Attempt:         state.Attempt,
		Generation:      state.Generation,
		Guard:           guard,
		FindingsPath:    findingsPath,
	}
	if item, ok := s.foreach.items[st.ID]; ok {
		req.FanOutItem = &item
	}
	return req
}
