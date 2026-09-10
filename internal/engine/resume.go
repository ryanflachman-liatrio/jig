package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"jig/internal/datastore"
	"jig/internal/interaction"
	"jig/internal/manifest"
	"jig/internal/review"
	"jig/internal/step"
	"jig/internal/workflow"
)

type workflowSnapshot struct {
	SourcePath    string                  `json:"source_path,omitempty"`
	BaseDir       string                  `json:"base_dir,omitempty"`
	SHA256        string                  `json:"sha256"`
	TOML          string                  `json:"toml"`
	ModuleSources []workflow.ModuleSource `json:"module_sources,omitempty"`
	Meta          workflow.Meta           `json:"meta"`
	Defaults      workflow.Defaults       `json:"defaults"`
	PublicSteps   []workflow.Step         `json:"public_steps,omitempty"`
	ExpandedSteps []workflow.Step         `json:"expanded_steps,omitempty"`
}

func persistWorkflowSnapshot(runDir string, wf *workflow.Workflow) error {
	if runDir == "" || wf == nil {
		return nil
	}
	path, source := wf.Source()
	if source == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(source))
	snap := workflowSnapshot{
		SourcePath:    path,
		BaseDir:       filepath.Dir(path),
		SHA256:        hex.EncodeToString(sum[:]),
		TOML:          source,
		ModuleSources: wf.ModuleSources(),
		Meta:          wf.Meta,
		Defaults:      wf.Defaults,
		PublicSteps:   wf.PublicSteps(),
		ExpandedSteps: wf.Steps,
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(datastore.WorkflowSnapshotPath(runDir), data, 0o644)
}

func loadWorkflowSnapshot(runDir string) (*workflow.Workflow, error) {
	data, err := os.ReadFile(datastore.WorkflowSnapshotPath(runDir))
	if err != nil {
		return nil, err
	}
	var snap workflowSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("decode workflow snapshot: %w", err)
	}
	sum := sha256.Sum256([]byte(snap.TOML))
	if hex.EncodeToString(sum[:]) != snap.SHA256 {
		return nil, fmt.Errorf("workflow snapshot checksum mismatch")
	}
	for _, source := range snap.ModuleSources {
		sum := sha256.Sum256([]byte(source.TOML))
		if source.Path == "" || source.SHA256 == "" || hex.EncodeToString(sum[:]) != source.SHA256 {
			return nil, fmt.Errorf("workflow snapshot module checksum mismatch")
		}
	}
	if len(snap.ExpandedSteps) > 0 {
		return workflow.RestoreExpanded(snap.Meta, snap.Defaults, snap.PublicSteps, snap.ExpandedSteps, snap.ModuleSources), nil
	}
	return workflow.DecodeLocked(snap.TOML, snap.BaseDir, snap.SourcePath, snap.ModuleSources)
}

func acquireRunLock(runDir string) (*os.File, error) {
	if runDir == "" {
		return nil, nil
	}
	f, err := os.OpenFile(datastore.SchedulerLockPath(runDir), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("run already has a live scheduler")
	}
	return f, nil
}

func releaseRunLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}

// RunLockState reports whether another process currently owns the scheduler
// lock. A missing lock file is free; probing never creates it or keeps a lock.
func RunLockState(runDir string) (bool, error) {
	if runDir == "" {
		return false, fmt.Errorf("engine: persistence required to inspect a run lock")
	}
	info, err := os.Stat(runDir)
	if err != nil {
		return false, fmt.Errorf("engine: inspect run directory: %w", err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("engine: run path is not a directory")
	}
	path := datastore.SchedulerLockPath(runDir)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("engine: open scheduler lock: %w", err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return true, nil
		}
		return false, fmt.Errorf("engine: probe scheduler lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		return false, fmt.Errorf("engine: release scheduler lock probe: %w", err)
	}
	return false, nil
}

// LoadWorkflowSnapshot reads and verifies the immutable workflow captured at
// run start. Historical commands must use this instead of current author TOML.
func LoadWorkflowSnapshot(runDir string) (*workflow.Workflow, error) {
	return loadWorkflowSnapshot(runDir)
}

// Resume restores every durable unfinished park under one scheduler. Workers
// interrupted mid-flight are moved to recovery; parks that already existed
// before process death are rehydrated in place.
func (m *Manager) Resume(runID string) (*Run, error) {
	if runID == "" || filepath.Base(runID) != runID || strings.ContainsAny(runID, `/\\`) {
		return nil, fmt.Errorf("engine: invalid run id %q", runID)
	}
	m.mu.Lock()
	if _, exists := m.runs[runID]; exists {
		m.mu.Unlock()
		return nil, fmt.Errorf("run %s already has a scheduler", runID)
	}
	m.mu.Unlock()

	runDir, err := datastore.ResolveRunDir(m.root, runID)
	if err != nil {
		return nil, fmt.Errorf("engine: resolve run: %w", err)
	}
	lock, err := acquireRunLock(runDir)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Run, error) {
		releaseRunLock(lock)
		return nil, err
	}

	seq, events, err := readJournal(runDir)
	if err != nil {
		return fail(err)
	}
	wf, err := loadWorkflowSnapshot(runDir)
	if err != nil {
		return fail(fmt.Errorf("resume workflow: %w", err))
	}
	checkpoint, err := restoreUnfinishedCheckpoint(runDir, wf, events)
	if err != nil {
		return fail(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	inbox := make(chan schedMsg, 64)
	run := &Run{ID: runID, cancel: cancel, inbox: inbox, done: make(chan struct{}), runLock: lock}

	m.mu.Lock()
	if _, exists := m.runs[runID]; exists {
		m.mu.Unlock()
		cancel()
		return fail(fmt.Errorf("run %s already has a scheduler", runID))
	}
	subs := append([]sub(nil), m.subs...)
	secrets := m.secrets
	m.runs[runID] = run
	m.mu.Unlock()

	w, err := manifest.NewWriter(runDir)
	if err != nil {
		m.mu.Lock()
		delete(m.runs, runID)
		m.mu.Unlock()
		cancel()
		return fail(err)
	}
	repoRoot := filepath.Dir(filepath.Clean(m.root))
	var security *runSecurity
	onDone := func(snap RunSnapshot) {
		run.finalSnap = snap
		security.stop()
		releaseRunLock(run.runLock)
		run.runLock = nil
		close(run.done)
	}
	s := newScheduler(wf, runID, inbox, subs, m.exec, cancel, w, runDir, m.root, repoRoot, onDone)
	s.resolver = m.resolver
	s.secretResolver = secrets
	s.seq = seq
	s.states = checkpoint.states
	s.reviewSessions = checkpoint.reviewSessions
	s.restoredHold = checkpoint.hasParks()
	for stepID := range checkpoint.recoverySkipped {
		s.skippedByOperator[stepID] = true
	}
	for i := range wf.Steps {
		if checkpoint.states[wf.Steps[i].ID].Status == step.StatusSkipped && wf.Steps[i].When != "" {
			s.skippedByGuard[wf.Steps[i].ID] = true
		}
	}
	// Rebuild runtime fan-out registries from the FanOutExpanded manifests
	// folded during restoreUnfinishedCheckpoint — the manifest is the
	// authoritative creation record, never a fresh producer read (see
	// rebuildFanOutFamily). These must be non-nil maps (dispatchForEach writes
	// into them directly) even when the run has no foreach family at all.
	s.fanOutFamilies = checkpoint.fanOutFamilies
	s.fanOutFamilyOrder = checkpoint.fanOutFamilyOrder
	s.fanOutChildren = checkpoint.fanOutChildren
	s.fanOutItemByChild = checkpoint.fanOutItemByChild
	// A family left Running settles by ordinary child transitions during live
	// execution (transitionRecovery's trySettleFamilyIfChild hook), which is
	// edge-triggered on a child reaching a terminal status. If every child was
	// already terminal before the crash, no such transition will ever occur
	// again — catch that up once here so the barrier still closes and its
	// aggregate still gets written. This is a no-op for a family with any
	// non-terminal child (trySettleFamily itself guards on that) and for a
	// family that already settled before the crash (guarded by fam.Settled /
	// state.Status != Running).
	for _, familyID := range s.fanOutFamilyOrder {
		s.trySettleFamily(familyID)
	}
	if err := s.restoreRunBranch(len(checkpoint.integrations) > 0); err != nil {
		m.mu.Lock()
		delete(m.runs, runID)
		m.mu.Unlock()
		_ = w.Close()
		cancel()
		return fail(fmt.Errorf("restore run branch: %w", err))
	}
	// Journal the entire recovery transaction before runLoop so a crash during
	// reopen cannot lose the decision surface (Spec 20 D18). The events are
	// fanned out only after the batch is durable.
	reopenEvents, err := s.rehydrateParks(checkpoint)
	if err != nil {
		m.mu.Lock()
		delete(m.runs, runID)
		m.mu.Unlock()
		_ = w.Close()
		cancel()
		return fail(err)
	}
	if err := s.emitBatch(reopenEvents); err != nil {
		m.mu.Lock()
		delete(m.runs, runID)
		m.mu.Unlock()
		if s.runWorktree != "" && len(checkpoint.integrations) == 0 {
			_ = removeWorktree(s.repoRoot, s.runWorktree)
			s.runWorktree = ""
		}
		_ = w.Close()
		cancel()
		return fail(fmt.Errorf("journal reopened parks: %w", err))
	}
	security = startRunSecurity(ctx, wf, runID, runDir, m.monitors, subs, inbox, true)
	if security != nil {
		s.securitySignals = security.signals
	}
	go s.runLoop(ctx)
	return run, nil
}

// processInterruptedErr is the durable sentinel written when Resume parks a
// worker that was mid-flight when the owning jig process exited (Spec 20 D14).
const processInterruptedErr = "process exited while step was running"

// processInterruptedRecoveryAction makes a recovery park idempotently
// repairable if a process exits between the batch's newline record boundaries.
// It is internal journal provenance, not an operator-selectable action.
const processInterruptedRecoveryAction = "process_interrupted"

const lostInputRecoveryAction = "lost_input"

type unfinishedCheckpoint struct {
	states              map[string]*step.State
	reviewSessions      map[string]review.Session
	interrupted         map[string]bool
	recoveries          map[string]RecoveryRequest
	inputs              map[string]InputRequest
	questions           map[string][]AgentQuestion
	integrations        map[string]IntegrationConflictRequest
	stopped             map[string]bool
	missingInputPayload map[string]string
	recoverySkipped     map[string]bool

	// Runtime fan-out registries rebuilt from FanOutExpanded events (see
	// rebuildFanOutFamily below). Mirrors scheduler.fanOutFamilies/
	// fanOutFamilyOrder/fanOutChildren/fanOutItemByChild exactly — Resume
	// copies these straight into the new scheduler.
	fanOutFamilies    map[string]*fanOutFamily
	fanOutFamilyOrder []string
	fanOutChildren    map[string]*workflow.Step
	fanOutItemByChild map[string]FanOutItem
}

// hasParks reports whether the run has an operator-visible gate to reopen. It
// deliberately excludes an unsettled foreach family (see hasOutstandingWork
// below): a family barrier is not a park a human or Recover() call resolves,
// so it must never hold scheduler dispatch via s.restoredHold the way a real
// park does.
func (c *unfinishedCheckpoint) hasParks() bool {
	return len(c.reviewSessions) > 0 || len(c.interrupted) > 0 || len(c.recoveries) > 0 ||
		len(c.inputs) > 0 || len(c.questions) > 0 || len(c.integrations) > 0 ||
		len(c.stopped) > 0 || len(c.missingInputPayload) > 0
}

// hasOutstandingWork reports whether Resume has anything at all to do: either
// a real operator park (hasParks) or a foreach family left Running. The latter
// is not a park — it is a barrier waiting on children that either still need
// (re)dispatch or are already terminal and only need their settlement
// transition, which Resume performs itself (see the trySettleFamily catch-up
// call in Manager.Resume) — but it does mean the run is not actually
// quiescent, so Resume must not fail closed with "no reopenable park".
func (c *unfinishedCheckpoint) hasOutstandingWork() bool {
	if c.hasParks() {
		return true
	}
	for _, familyID := range c.fanOutFamilyOrder {
		if st := c.states[familyID]; st != nil && st.Status == step.StatusRunning {
			return true
		}
	}
	return false
}

func restoreUnfinishedCheckpoint(runDir string, wf *workflow.Workflow, events []Event) (*unfinishedCheckpoint, error) {
	if wf == nil {
		return nil, fmt.Errorf("resume: workflow is unavailable")
	}
	states := make(map[string]*step.State, len(wf.Steps))
	for i := range wf.Steps {
		states[wf.Steps[i].ID] = &step.State{ID: wf.Steps[i].ID, Status: step.StatusPending}
	}
	reviewRequests := make(map[string]ReviewRequest)
	recoveryRequests := make(map[string]RecoveryRequest)
	inputRequests := make(map[string]InputRequest)
	questions := make(map[string][]AgentQuestion)
	integrationRequests := make(map[string]IntegrationConflictRequest)
	verdicts := make(map[string]string)
	recoverySkipped := make(map[string]bool)
	interruptedRecovery := make(map[string]bool)
	lostInputRecovery := make(map[string]bool)
	fanOutFamilies := make(map[string]*fanOutFamily)
	var fanOutFamilyOrder []string
	fanOutChildren := make(map[string]*workflow.Step)
	fanOutItemByChild := make(map[string]FanOutItem)
	var started *RunStarted
	for _, event := range events {
		switch event := event.(type) {
		case RunStarted:
			copy := event
			started = &copy
		case RunFinished:
			return nil, fmt.Errorf("resume: run is already finished")
		case FanOutExpanded:
			// FanOutExpanded is the authoritative creation record for runtime
			// children (docs/plans/a8-dynamic-foreach-fan-out.md, "Durability,
			// replay, and crash reopen"): rebuild the family and its children
			// from the matching on-disk manifest — never by re-reading the
			// producer — and register their step.State entries before any
			// later StepStatus event for a child instance id is folded below.
			rebuilt, err := rebuildFanOutFamily(runDir, wf, event, states)
			if err != nil {
				return nil, err
			}
			if _, exists := fanOutFamilies[event.FamilyID]; !exists {
				fanOutFamilyOrder = append(fanOutFamilyOrder, event.FamilyID)
			}
			fanOutFamilies[event.FamilyID] = rebuilt.family
			for id, child := range rebuilt.children {
				fanOutChildren[id] = child
			}
			for id, item := range rebuilt.items {
				fanOutItemByChild[id] = item
			}
		case StepStatus:
			state := states[event.StepID]
			if state == nil {
				return nil, fmt.Errorf("resume: workflow no longer contains step %q", event.StepID)
			}
			state.Status = event.To
			state.Attempt = event.Attempt
			state.Iteration = event.Iteration
			state.Generation = event.Generation
			if event.Cost != nil {
				state.SpentUSD = *event.Cost
			}
			if event.Tokens > state.SpentTokens {
				state.SpentTokens = event.Tokens
			}
			if terminalStatus(event.To) {
				state.Result = loadPersistedResult(runDir, event.StepID, event.To, event.Err, event.Subtype)
			}
			if event.To == step.StatusFailed && event.RecoveryAction == RecoverSkip {
				recoverySkipped[event.StepID] = true
			} else if event.To != step.StatusFailed {
				delete(recoverySkipped, event.StepID)
			}
			if event.To == step.StatusAwaitingRecovery && event.RecoveryAction == processInterruptedRecoveryAction {
				interruptedRecovery[event.StepID] = true
			} else {
				delete(interruptedRecovery, event.StepID)
			}
			if event.To == step.StatusAwaitingRecovery && event.RecoveryAction == lostInputRecoveryAction {
				lostInputRecovery[event.StepID] = true
			} else {
				delete(lostInputRecovery, event.StepID)
			}
			if event.To != step.StatusAwaitingReview {
				delete(reviewRequests, event.StepID)
			}
			if event.To != step.StatusAwaitingRecovery {
				delete(recoveryRequests, event.StepID)
			}
			if event.To != step.StatusNeedsInput {
				delete(inputRequests, event.StepID)
				delete(questions, event.StepID)
			}
			if event.To != step.StatusAwaitingIntegration {
				delete(integrationRequests, event.StepID)
			}
		case ReviewRequest:
			reviewRequests[event.StepID] = event
		case ReviewSubmitted:
			verdicts[event.StepID] = event.Verdict
			delete(reviewRequests, event.StepID)
		case RecoveryRequest:
			recoveryRequests[event.StepID] = event
		case InputRequest:
			inputRequests[event.StepID] = event
		case AgentQuestion:
			questions[event.StepID] = upsertQuestion(questions[event.StepID], event)
		case AgentQuestionResolved:
			questions[event.StepID] = removeQuestion(questions[event.StepID], event.RequestID)
		case IntegrationConflictRequest:
			integrationRequests[event.StepID] = event
		}
	}
	if started == nil || started.Workflow != wf.Meta.Name || len(started.Steps) != len(wf.Steps) {
		return nil, fmt.Errorf("resume: workflow does not match the historical run")
	}
	for i, id := range started.Steps {
		if wf.Steps[i].ID != id {
			return nil, fmt.Errorf("resume: workflow step order changed at %q", id)
		}
	}
	for id, verdict := range verdicts {
		if state := states[id]; state != nil {
			if state.Result == nil {
				state.Result = &step.Result{Status: state.Status}
			}
			state.Result.Verdict = verdict
		}
	}

	checkpoint := &unfinishedCheckpoint{
		states:              states,
		reviewSessions:      make(map[string]review.Session),
		interrupted:         make(map[string]bool),
		recoveries:          make(map[string]RecoveryRequest),
		inputs:              make(map[string]InputRequest),
		questions:           make(map[string][]AgentQuestion),
		integrations:        make(map[string]IntegrationConflictRequest),
		stopped:             make(map[string]bool),
		missingInputPayload: make(map[string]string),
		recoverySkipped:     recoverySkipped,
		fanOutFamilies:      fanOutFamilies,
		fanOutFamilyOrder:   fanOutFamilyOrder,
		fanOutChildren:      fanOutChildren,
		fanOutItemByChild:   fanOutItemByChild,
	}

	// classifyPark applies the same durable-status classification to any step
	// id — static or a runtime fan-out child — so a child parked mid-flight
	// reopens through exactly the same Specs 20/21 path as a static step (the
	// plan's "Durability, replay, and crash reopen").
	classifyPark := func(id string) error {
		state := states[id]
		switch state.Status {
		case step.StatusPending, step.StatusSucceeded, step.StatusSkipped, step.StatusFailed:
		case step.StatusAwaitingReview:
			req, ok := reviewRequests[id]
			if !ok {
				return fmt.Errorf("resume: step %q has no durable review request", id)
			}
			sess, err := restoreReviewSession(runDir, req)
			if err != nil {
				return err
			}
			checkpoint.reviewSessions[id] = sess
		case step.StatusRunning, step.StatusValidating:
			checkpoint.interrupted[id] = true
		case step.StatusAwaitingRecovery:
			req, ok := recoveryRequests[id]
			if !ok {
				errText := "step was awaiting recovery when the jig process exited"
				if interruptedRecovery[id] {
					errText = processInterruptedErr
				} else if lostInputRecovery[id] {
					errText = "process exited while step needed input, but its durable input could not be restored"
				}
				req = RecoveryRequest{RunID: started.RunID, StepID: id, Err: errText}
			}
			checkpoint.recoveries[id] = req
		case step.StatusNeedsInput:
			switch {
			case len(questions[id]) > 0:
				checkpoint.questions[id] = questions[id]
			case inputRequests[id].StepID != "":
				checkpoint.inputs[id] = inputRequests[id]
			default:
				checkpoint.missingInputPayload[id] = "process exited while step needed input, but no durable input request was found"
			}
		case step.StatusAwaitingIntegration:
			checkpoint.integrations[id] = integrationRequests[id]
		case step.StatusStopped:
			checkpoint.stopped[id] = true
		default:
			return fmt.Errorf("resume: step %q has unsupported durable status %s", id, state.Status)
		}
		return nil
	}

	for i := range wf.Steps {
		id := wf.Steps[i].ID
		// A foreach family left Running is a barrier waiting on its children,
		// never a worker that was mid-flight — it must not be misclassified as
		// interrupted. Its children (folded in below, independently) carry
		// their own real statuses and reopen normally.
		if wf.Steps[i].ForEach != nil && states[id].Status == step.StatusRunning {
			continue
		}
		if err := classifyPark(id); err != nil {
			return nil, err
		}
	}
	for _, familyID := range fanOutFamilyOrder {
		fam := fanOutFamilies[familyID]
		if fam == nil {
			continue
		}
		for _, childID := range fam.Order {
			if err := classifyPark(childID); err != nil {
				return nil, err
			}
		}
	}
	if !checkpoint.hasOutstandingWork() && len(recoverySkipped) == 0 {
		return nil, fmt.Errorf("resume: run has no reopenable park")
	}
	return checkpoint, nil
}

// rebuiltFanOutFamily is rebuildFanOutFamily's output: everything
// restoreUnfinishedCheckpoint needs to fold one FanOutExpanded event into the
// checkpoint's runtime registries.
type rebuiltFanOutFamily struct {
	family   *fanOutFamily
	children map[string]*workflow.Step
	items    map[string]FanOutItem
}

// rebuildFanOutFamily recreates one foreach family's runtime children from its
// durable, versioned manifest — the authoritative creation record — rather
// than by re-reading the producer's output. It fails closed (per the plan's
// "Durability, replay, and crash reopen") when the manifest is missing,
// corrupt, out of order, or its digest does not match the journaled
// FanOutExpanded event: a mismatch means the manifest was rewritten (or never
// durably renamed) after the event was journaled, and Resume must not trust
// it. It also registers a step.State for each child directly into states so
// later StepStatus events for that instance id (processed in the same journal
// pass, in order) find an existing entry exactly like a static step would.
func rebuildFanOutFamily(runDir string, wf *workflow.Workflow, event FanOutExpanded, states map[string]*step.State) (*rebuiltFanOutFamily, error) {
	tmpl := wfStepByID(wf, event.FamilyID)
	if tmpl == nil || tmpl.ForEach == nil {
		return nil, fmt.Errorf("resume: fan-out family %q is not a foreach step in the current workflow", event.FamilyID)
	}
	m, err := datastore.ReadFanOutManifest(runDir, event.FamilyID, event.Generation, event.Iteration)
	if err != nil {
		return nil, fmt.Errorf("resume: fan-out family %q generation %d iteration %d: %w", event.FamilyID, event.Generation, event.Iteration, err)
	}
	if err := datastore.ValidateFanOutItems(m); err != nil {
		return nil, fmt.Errorf("resume: fan-out family %q: %w", event.FamilyID, err)
	}
	digest, err := datastore.FanOutManifestDigest(m)
	if err != nil {
		return nil, fmt.Errorf("resume: fan-out family %q: digest manifest: %w", event.FamilyID, err)
	}
	if digest != event.ManifestDigest {
		return nil, fmt.Errorf("resume: fan-out family %q generation %d iteration %d: on-disk manifest digest %s does not match the journaled digest %s — refusing to trust a manifest that was rewritten (or never durably renamed) after the event was journaled",
			event.FamilyID, event.Generation, event.Iteration, digest, event.ManifestDigest)
	}
	if len(m.Items) != len(event.Instances) {
		return nil, fmt.Errorf("resume: fan-out family %q generation %d iteration %d: manifest has %d items, journaled event has %d instances",
			event.FamilyID, event.Generation, event.Iteration, len(m.Items), len(event.Instances))
	}

	fam := &fanOutFamily{
		FamilyID:    event.FamilyID,
		Generation:  event.Generation,
		Iteration:   event.Iteration,
		MaxParallel: tmpl.ForEach.MaxParallel,
	}
	out := &rebuiltFanOutFamily{
		family:   fam,
		children: make(map[string]*workflow.Step, len(m.Items)),
		items:    make(map[string]FanOutItem, len(m.Items)),
	}
	for _, it := range m.Items {
		child := cloneForEachTemplate(tmpl, it.InstanceID)
		states[it.InstanceID] = &step.State{
			ID:          it.InstanceID,
			Status:      step.StatusPending,
			ParentID:    event.FamilyID,
			FanOutIndex: it.Index,
			FanOutTotal: len(m.Items),
		}
		out.children[it.InstanceID] = child
		out.items[it.InstanceID] = FanOutItem{
			TemplateID: event.FamilyID,
			InstanceID: it.InstanceID,
			Index:      it.Index,
			Total:      len(m.Items),
			As:         tmpl.ForEach.As,
			Item:       it.Item,
			Digest:     it.ItemSHA256,
		}
		fam.Order = append(fam.Order, it.InstanceID)
	}
	return out, nil
}

// wfStepByID returns the static step template with the given id, or nil.
// restoreUnfinishedCheckpoint runs before any scheduler exists, so it cannot
// use scheduler.stepByID (which also consults the runtime fan-out registry —
// irrelevant here, since a foreach family's template is always a static step).
func wfStepByID(wf *workflow.Workflow, id string) *workflow.Step {
	for i := range wf.Steps {
		if wf.Steps[i].ID == id {
			return &wf.Steps[i]
		}
	}
	return nil
}

func upsertQuestion(existing []AgentQuestion, question AgentQuestion) []AgentQuestion {
	for i := range existing {
		if existing[i].Request.ID == question.Request.ID {
			existing[i] = question
			return existing
		}
	}
	return append(existing, question)
}

func removeQuestion(existing []AgentQuestion, requestID string) []AgentQuestion {
	for i := range existing {
		if existing[i].Request.ID == requestID {
			return append(existing[:i], existing[i+1:]...)
		}
	}
	return existing
}

func (s *scheduler) rehydrateParks(checkpoint *unfinishedCheckpoint) ([]Event, error) {
	var events []Event
	// reopen produces the durable reopen events for one parked step id — static
	// or a runtime fan-out child alike, reusing the exact Specs 20/21 park-reopen
	// path (docs/plans/a8-dynamic-foreach-fan-out.md, "Durability, replay, and
	// crash reopen": "A child ... reopens through the same Specs 20/21 path as
	// a static step").
	reopen := func(stepID string) error {
		state := s.states[stepID]
		switch state.Status {
		case step.StatusRunning, step.StatusValidating:
			events = append(events, s.parkInterruptedWorker(stepID)...)
		case step.StatusAwaitingRecovery:
			s.restoreParkSession(stepID)
			s.reattachStepWorktree(stepID)
			req := checkpoint.recoveries[stepID]
			if req.Err == "" {
				req.Err = "step was awaiting recovery when the jig process exited"
			}
			if state.Result != nil {
				state.Result.Err = req.Err
				state.Result.Status = step.StatusFailed
				req.CanResume = s.stepCanResume(stepID, state.Result.SessionID)
			} else {
				req.CanResume = false
			}
			req.RunID = s.runID
			req.StepID = stepID
			events = append(events, req)
		case step.StatusStopped:
			s.restoreParkSession(stepID)
			s.reattachStepWorktree(stepID)
		case step.StatusNeedsInput:
			s.restoreParkSession(stepID)
			s.reattachStepWorktree(stepID)
			errText := checkpoint.missingInputPayload[stepID]
			if errText == "" && (state.Result == nil || !s.stepCanResume(stepID, state.Result.SessionID)) {
				errText = "process exited while step needed input, but its agent session cannot be resumed"
			}
			if errText != "" {
				events = append(events, s.parkLostInput(stepID, errText)...)
				return nil
			}
			if pending := checkpoint.questions[stepID]; len(pending) > 0 {
				s.pendingQuestions[stepID] = make(map[string]pendingQuestion, len(pending))
				for _, question := range pending {
					question.RunID = s.runID
					s.pendingQuestions[stepID][question.Request.ID] = pendingQuestion{request: question.Request, restored: true}
					events = append(events, question)
				}
			} else {
				request := checkpoint.inputs[stepID]
				request.RunID = s.runID
				events = append(events, request)
			}
		case step.StatusAwaitingIntegration:
			paths := mergeConflictPaths(s.runWorktree)
			if len(paths) == 0 {
				return fmt.Errorf("resume: step %q is awaiting integration but the run worktree has no unresolved conflict markers", stepID)
			}
			request := checkpoint.integrations[stepID]
			events = append(events, s.integrationConflictRequest(stepID, paths, request.Resolution))
		}
		return nil
	}

	for i := range s.wf.Steps {
		stepID := s.wf.Steps[i].ID
		// A foreach family left Running is a barrier, not a worker — see the
		// matching special case in restoreUnfinishedCheckpoint. Its children
		// are reopened independently below.
		if s.wf.Steps[i].ForEach != nil && s.states[stepID].Status == step.StatusRunning {
			continue
		}
		if err := reopen(stepID); err != nil {
			return nil, err
		}
	}
	for _, familyID := range checkpoint.fanOutFamilyOrder {
		fam := checkpoint.fanOutFamilies[familyID]
		if fam == nil {
			continue
		}
		for _, childID := range fam.Order {
			if err := reopen(childID); err != nil {
				return nil, err
			}
		}
	}
	return events, nil
}

func (s *scheduler) restoreParkSession(stepID string) {
	state := s.states[stepID]
	if state == nil {
		return
	}
	if state.Result == nil {
		state.Result = loadPersistedResult(s.runDir, stepID, state.Status, "", "")
	}
	if info, err := datastore.ReadSession(s.runDir, stepID); err == nil && info.SessionID != "" {
		state.Result.SessionID = info.SessionID
	}
}

func (s *scheduler) reattachStepWorktree(stepID string) {
	st := s.stepByID(stepID)
	if st == nil || st.Isolation != workflow.IsolationWorktree || s.jigRoot == "" {
		return
	}
	path := filepath.Join(s.jigRoot, "worktrees", s.runID, stepID)
	if !directoryExists(path) {
		return
	}
	s.worktrees[stepID] = path
	if !registeredWorktree(s.repoRoot, path, s.stepBranchName(stepID)) {
		return
	}
	if base, err := gitCmd(path, "merge-base", "HEAD", s.runBranch); err == nil {
		if base = strings.TrimSpace(base); base != "" {
			s.wtBaseSHAs[stepID] = base
		}
	}
}

func (s *scheduler) parkLostInput(stepID, errText string) []Event {
	state := s.states[stepID]
	if state == nil || state.Status != step.StatusNeedsInput {
		return nil
	}
	if state.Result == nil {
		state.Result = &step.Result{}
	}
	state.Result.Status = step.StatusFailed
	state.Result.Err = errText
	status := s.transitionEvent(stepID, step.StatusNeedsInput, step.StatusAwaitingRecovery, lostInputRecoveryAction)
	return []Event{status, RecoveryRequest{RunID: s.runID, StepID: stepID, Err: errText}}
}

func restoredQuestionMessage(responses []interaction.QuestionResponse) string {
	data, err := json.Marshal(responses)
	if err != nil {
		return "The jig process exited while you were waiting for AskUserQuestion input. Continue using the operator's submitted answers."
	}
	return "The jig process exited while you were waiting for AskUserQuestion input. Continue using these operator responses: " + string(data)
}

// parkInterruptedWorker prepares the durable events that move an interrupted
// running/validating step onto the recovery gate. The caller persists all such
// events as one batch before starting the scheduler. Attempt is not bumped here
// (Spec 20 D15) — that waits for Recover(retry|resume).
func (s *scheduler) parkInterruptedWorker(stepID string) []Event {
	state := s.states[stepID]
	if state == nil {
		return nil
	}
	from := state.Status
	if from != step.StatusRunning && from != step.StatusValidating && from != step.StatusAwaitingRecovery {
		return nil
	}
	if state.Result == nil {
		state.Result = &step.Result{Status: step.StatusFailed}
	}
	if info, err := datastore.ReadSession(s.runDir, stepID); err == nil && info.SessionID != "" {
		state.Result.SessionID = info.SessionID
	}
	state.Result.Err = processInterruptedErr
	state.Result.Status = step.StatusFailed

	s.reattachStepWorktree(stepID)

	status := s.transitionEvent(stepID, from, step.StatusAwaitingRecovery, processInterruptedRecoveryAction)
	return []Event{status, RecoveryRequest{
		RunID:     s.runID,
		StepID:    stepID,
		Err:       processInterruptedErr,
		CanResume: s.stepCanResume(stepID, state.Result.SessionID),
	}}
}

// stepCanResume reports whether Recover(resume) should be offered: durable
// SessionID plus CapSessionResume when the executor can answer, and a present
// mutator worktree when the step requires one (D5 / D10).
func (s *scheduler) stepCanResume(stepID, sessionID string) bool {
	if sessionID == "" {
		return false
	}
	if s.failedSessionResume[stepID] {
		return false
	}
	st := s.stepByID(stepID)
	if st == nil || st.Type != workflow.StepAgent {
		return false
	}
	support, ok := s.exec.(SessionResumeSupport)
	if !ok || !support.SupportsSessionResume(st.Backend, st.Transport) {
		return false
	}
	if st.Isolation == workflow.IsolationWorktree {
		path := s.worktrees[stepID]
		if path == "" && s.jigRoot != "" {
			path = filepath.Join(s.jigRoot, "worktrees", s.runID, stepID)
		}
		if path == "" || !directoryExists(path) || s.wtBaseSHAs[stepID] == "" {
			return false
		}
	}
	return true
}

func terminalStatus(status step.Status) bool {
	switch status {
	case step.StatusSucceeded, step.StatusFailed, step.StatusSkipped:
		return true
	default:
		return false
	}
}

func loadPersistedResult(runDir, stepID string, status step.Status, resultErr, subtype string) *step.Result {
	result := &step.Result{Status: status, Err: resultErr, Subtype: subtype}
	if data, err := os.ReadFile(datastore.OutputJSONPath(runDir, stepID)); err == nil {
		result.Structured = data
	}
	if path := datastore.OutputPath(runDir, stepID); fileExists(path) {
		result.OutputPath = path
	}
	return result
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func restoreReviewSession(runDir string, req ReviewRequest) (review.Session, error) {
	docs := append([]review.Document(nil), req.Documents...)
	for i := range docs {
		path := docs[i].SnapshotPath
		if path == "" || !fileExists(path) {
			path = filepath.Join(datastore.ReviewDocumentsDir(runDir, req.StepID, req.RoundID), docs[i].ID+reviewExtension(docs[i].Format))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return review.Session{}, fmt.Errorf("resume review %q: load document %q: %w", req.StepID, docs[i].ID, err)
		}
		docs[i].SnapshotPath = path
		docs[i].Content = string(data)
		if review.Digest(docs[i].Content) != docs[i].SHA256 {
			return review.Session{}, fmt.Errorf("resume review %q: document %q checksum mismatch", req.StepID, docs[i].ID)
		}
	}
	return review.Session{StepID: req.StepID, RoundID: req.RoundID, Documents: docs}, nil
}

func (s *scheduler) restoreRunBranch(preserveConflict bool) error {
	if s.repoRoot == "" {
		return nil
	}
	if _, err := currentHEAD(s.repoRoot); err != nil {
		return nil
	}
	branch := runBranchName(s.wf.Meta.Name, s.runID)
	if _, err := gitCmd(s.repoRoot, "rev-parse", "--verify", branch); err != nil {
		return fmt.Errorf("integration branch %s is missing", branch)
	}
	wtPath := filepath.Join(s.jigRoot, "worktrees", s.runID, "_run")
	var base string
	if preserveConflict {
		// Recreating the run worktree would discard the index/conflict index
		// state that is the integration gate's durable payload.
		if !registeredWorktree(s.repoRoot, wtPath, branch) {
			return fmt.Errorf("integration worktree for %s is missing or is not registered to %s", s.runID, branch)
		}
		if len(mergeConflictPaths(wtPath)) == 0 {
			return fmt.Errorf("integration worktree for %s has no unresolved conflict markers", s.runID)
		}
		var err error
		base, err = currentHEAD(wtPath)
		if err != nil {
			return fmt.Errorf("read integration worktree HEAD: %w", err)
		}
	} else {
		// A crashed process can leave the run branch registered to its old worktree.
		// The scheduler lock establishes single ownership among resume-capable jig processes.
		_ = removeWorktree(s.repoRoot, wtPath)
		_, _ = gitCmd(s.repoRoot, "worktree", "prune")
		var err error
		base, err = createWorktreeAt(s.repoRoot, wtPath, branch, branch)
		if err != nil {
			return err
		}
	}
	s.runBranch = branch
	s.runWorktree = wtPath
	s.runBaseSHA = base
	if out, err := gitCmd(s.repoRoot, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		s.baseBranch = strings.TrimSpace(out)
	}
	if s.baseBranch != "" {
		if out, err := gitCmd(s.repoRoot, "merge-base", branch, s.baseBranch); err == nil {
			s.runBaseSHA = strings.TrimSpace(out)
		}
	}
	commits, err := stepCommitsFromLog(s.repoRoot, branch)
	if err != nil {
		_ = removeWorktree(s.repoRoot, wtPath)
		s.runWorktree = ""
		return err
	}
	s.stepCommits = commits
	return nil
}
