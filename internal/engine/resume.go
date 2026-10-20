package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/manifest"
	"github.com/ryanflachman-liatrio/jig/internal/review"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// Resume restores every durable unfinished park under one scheduler. Workers
// interrupted mid-flight are moved to recovery; parks that already existed
// before process death are rehydrated in place.
func (m *Manager) Resume(runID string) (_ *Run, err error) {
	if runID == "" || filepath.Base(runID) != runID || strings.ContainsAny(runID, `/\\`) {
		return nil, fmt.Errorf("engine: invalid run id %q", runID)
	}
	if m.hasRun(runID) {
		return nil, fmt.Errorf("run %s: %w", runID, ErrRunLocked)
	}
	runDir, err := m.store.Resolve(runID)
	if err != nil {
		return nil, fmt.Errorf("engine: resolve run: %w", err)
	}
	lock, err := AcquireRunLease(runDir)
	if err != nil {
		return nil, err
	}

	// Each stage below records what it acquired; on failure this undoes them
	// in reverse.
	var (
		cancel     context.CancelFunc
		registered bool
		w          *manifest.Writer
		s          *scheduler
		dropRunWT  bool // remove a run worktree restoreRunBranch created
	)
	defer func() {
		if err == nil {
			return
		}
		if dropRunWT && s != nil && s.branch.worktree != "" {
			_ = removeWorktree(s.repoRoot, s.branch.worktree)
			s.branch.worktree = ""
		}
		if w != nil {
			_ = w.Close()
		}
		if registered {
			m.mu.Lock()
			delete(m.runs, runID)
			m.mu.Unlock()
		}
		if cancel != nil {
			cancel()
		}
		_ = lock.Close()
	}()

	seq, events, err := readJournalStrict(runDir)
	if err != nil {
		return nil, err
	}
	wf, err := loadWorkflowSnapshot(runDir)
	if err != nil {
		return nil, fmt.Errorf("resume workflow: %w", err)
	}
	checkpoint, err := restoreUnfinishedCheckpoint(runDir, wf, events)
	if err != nil {
		return nil, err
	}

	// The run outlives this call, so its context is rooted in Background.
	ctx, runCancel := context.WithCancel(context.Background())
	cancel = runCancel
	inbox := make(chan schedMsg, 64)
	run := &Run{ID: runID, cancel: runCancel, inbox: inbox, done: make(chan struct{}), runLock: lock}
	subs, secrets, ok := m.register(run)
	if !ok {
		return nil, fmt.Errorf("run %s: %w", runID, ErrRunLocked)
	}
	registered = true

	if w, err = manifest.NewWriter(runDir); err != nil {
		return nil, err
	}
	var security *runSecurity
	onDone := func(snap RunSnapshot) {
		run.finalSnap = snap
		security.stop()
		_ = run.runLock.Close()
		run.runLock = nil
		close(run.done)
	}
	s = newScheduler(schedulerDeps{
		wf: wf, runID: runID, inbox: inbox, subs: subs, exec: m.exec, cancel: runCancel,
		writer: w, runDir: runDir, jigRoot: m.store.Root(),
		repoRoot: filepath.Dir(filepath.Clean(m.store.Root())), onDone: onDone,
	})
	s.resolver = m.resolver
	s.secretResolver = secrets
	s.getCauseHint = run.causeSnapshot
	s.seq = seq
	s.applyCheckpoint(checkpoint)
	if err := s.restoreRunBranch(len(checkpoint.integrations) > 0); err != nil {
		return nil, fmt.Errorf("restore run branch: %w", err)
	}
	// Journal the entire recovery transaction before runLoop so a crash during
	// reopen cannot lose the decision surface (Spec 20 D18). The events are
	// fanned out only after the batch is durable.
	reopenEvents, err := s.rehydrateParks(checkpoint)
	if err != nil {
		return nil, err
	}
	s.observers = &m.observers

	// Register the reopened run before any live event is fanned out. The
	// notification lifecycle uses this seed to emit one filtered restored
	// summary rather than replaying historical alerts.
	m.observers.dispatchRegistered(RunRegistration{
		RunID:           runID,
		Workflow:        wf.Meta.Name,
		Epoch:           m.nextEpoch(),
		Reopen:          true,
		Snapshot:        run.Snapshot,
		Policy:          wf.NotificationPolicy(),
		UnresolvedWaits: unresolvedWaitsFromCheckpoint(checkpoint),
	})

	if err := s.emitBatch(reopenEvents); err != nil {
		// An integration park keeps its conflicted run worktree for the
		// operator; otherwise the worktree restoreRunBranch made is discarded.
		dropRunWT = len(checkpoint.integrations) == 0
		return nil, fmt.Errorf("journal reopened parks: %w", err)
	}
	security = startRunSecurity(ctx, wf, runID, runDir, m.monitors, subs, inbox, true)
	if security != nil {
		s.securitySignals = security.signals
	}
	go func() {
		defer m.observers.dispatchStopped(runID)
		s.runLoop(ctx)
	}()
	return run, nil
}

// hasRun reports whether runID already has a scheduler in this process.
func (m *Manager) hasRun(runID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, exists := m.runs[runID]
	return exists
}

// register adds run to the registry unless another scheduler claimed its id
// first, returning the subscribers and secret resolver to wire into it.
func (m *Manager) register(run *Run) ([]sub, SecretResolver, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.runs[run.ID]; exists {
		return nil, nil, false
	}
	m.runs[run.ID] = run
	return append([]sub(nil), m.subs...), m.secrets, true
}

// applyCheckpoint loads a restored checkpoint into a fresh scheduler.
func (s *scheduler) applyCheckpoint(checkpoint *unfinishedCheckpoint) {
	s.states = checkpoint.states
	s.reviewSessions = checkpoint.reviewSessions
	s.restoredHold = checkpoint.hasParks()
	for stepID := range checkpoint.recoverySkipped {
		s.rt(stepID).skippedByOperator = true
	}
	for i := range s.wf.Steps {
		if checkpoint.states[s.wf.Steps[i].ID].Status == step.StatusSkipped && s.wf.Steps[i].When != "" {
			s.rt(s.wf.Steps[i].ID).skippedByGuard = true
		}
	}
	// The fan-out registry comes from the FanOutExpanded manifests folded by
	// restoreUnfinishedCheckpoint: the manifest is the authoritative creation
	// record, never a fresh producer read (see rebuildFanOutFamily).
	s.foreach = checkpoint.fanOut
	// A family left Running settles through child transitions, which are
	// edge-triggered on a child reaching a terminal status. If every child was
	// already terminal before the crash, no such transition will happen again,
	// so catch up once here. trySettleFamily is a no-op for a family with a
	// non-terminal child or one that already settled.
	for _, familyID := range s.foreach.order {
		s.trySettleFamily(familyID)
	}
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

	// fanOut is rebuilt from FanOutExpanded events (see rebuildFanOutFamily
	// below); Resume hands it straight to the new scheduler.
	fanOut fanOutRegistry
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

// unresolvedWaitsFromCheckpoint translates a resumed checkpoint's still-parked
// human waits into the wait-kind vocabulary observers consume. It is used only
// by Manager.Resume to seed one filtered restored summary per (run, epoch)
// through the notification lifecycle; the scheduler itself does not read it.
//
// Nonce is populated from the same identifier the live event will carry when
// its durable record is replayed through the observer's Publish path — the
// review round id, the AskUserQuestion request id, or the prompt alias — so
// the seed and the replay collapse onto one wait identity and the observer
// does not emit a second attention summary right after the restored one.
func unresolvedWaitsFromCheckpoint(c *unfinishedCheckpoint) []UnresolvedWait {
	if c == nil {
		return nil
	}
	var out []UnresolvedWait
	for stepID, session := range c.reviewSessions {
		out = append(out, UnresolvedWait{StepID: stepID, Kind: WaitReview, Nonce: session.RoundID})
	}
	for stepID := range c.inputs {
		out = append(out, UnresolvedWait{StepID: stepID, Kind: WaitInput})
	}
	for stepID, pending := range c.questions {
		if len(pending) == 0 {
			out = append(out, UnresolvedWait{StepID: stepID, Kind: WaitQuestion})
			continue
		}
		// One seed per still-unresolved AskUserQuestion request so a
		// replayed AgentQuestion Publish matches on the same request id.
		for _, q := range pending {
			out = append(out, UnresolvedWait{StepID: stepID, Kind: WaitQuestion, Nonce: q.Request.ID})
		}
	}
	for stepID := range c.recoveries {
		out = append(out, UnresolvedWait{StepID: stepID, Kind: WaitRecovery})
	}
	for stepID := range c.integrations {
		out = append(out, UnresolvedWait{StepID: stepID, Kind: WaitIntegrationConflict})
	}
	return out
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
	for _, familyID := range c.fanOut.order {
		if st := c.states[familyID]; st != nil && st.Status == step.StatusRunning {
			return true
		}
	}
	return false
}

func restoreUnfinishedCheckpoint(runDir datastore.Run, wf *workflow.Workflow, events []Event) (*unfinishedCheckpoint, error) {
	if wf == nil {
		return nil, errors.New("resume: workflow is unavailable")
	}
	f := newCheckpointFold(runDir, wf)
	for _, event := range events {
		if err := f.apply(event); err != nil {
			return nil, err
		}
	}
	states, started, fanOut := f.states, f.started, f.fanOut
	if started == nil || started.Workflow != wf.Meta.Name || len(started.Steps) != len(wf.Steps) {
		return nil, errors.New("resume: workflow does not match the historical run")
	}
	// The process was absent after its last durable observation. Do not turn
	// that unobserved interval into execution time when recovery parks workers.
	for _, state := range states {
		state.Timing.Pause(f.lastStatusAt)
	}
	for i, id := range started.Steps {
		if wf.Steps[i].ID != id {
			return nil, fmt.Errorf("resume: workflow step order changed at %q", id)
		}
	}
	for id, verdict := range f.verdicts {
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
		recoverySkipped:     f.recoverySkipped,
		fanOut:              fanOut,
	}

	for i := range wf.Steps {
		id := wf.Steps[i].ID
		// A foreach family left Running is a barrier waiting on its children,
		// never a worker that was mid-flight — it must not be misclassified as
		// interrupted. Its children (folded in below, independently) carry
		// their own real statuses and reopen normally.
		if isOpenFamilyBarrier(&wf.Steps[i], states[id].Status) {
			continue
		}
		if err := f.classifyPark(checkpoint, id); err != nil {
			return nil, err
		}
	}
	for _, familyID := range fanOut.order {
		fam := fanOut.families[familyID]
		if fam == nil {
			continue
		}
		for _, childID := range fam.Order {
			if err := f.classifyPark(checkpoint, childID); err != nil {
				return nil, err
			}
		}
	}
	if !checkpoint.hasOutstandingWork() && len(f.recoverySkipped) == 0 {
		return nil, errors.New("resume: run has no reopenable park")
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
func rebuildFanOutFamily(runDir datastore.Run, wf *workflow.Workflow, event FanOutExpanded, states map[string]*step.State) (*rebuiltFanOutFamily, error) {
	tmpl := wfStepByID(wf, event.FamilyID)
	if tmpl == nil || tmpl.ForEach == nil {
		return nil, fmt.Errorf("resume: fan-out family %q is not a foreach step in the current workflow", event.FamilyID)
	}
	m, err := runDir.Step(event.FamilyID).ReadFanOutManifest(event.Generation, event.Iteration)
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

func loadPersistedResult(runDir datastore.Run, stepID string, status step.Status, resultErr, subtype string) *step.Result {
	result := &step.Result{Status: status, Err: resultErr, Subtype: subtype}
	if data, err := os.ReadFile(runDir.Step(stepID).OutputJSON()); err == nil {
		result.Structured = data
	}
	if path := runDir.Step(stepID).Output(); fileExists(path) {
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
	s.branch.name = branch
	s.branch.worktree = wtPath
	s.branch.baseSHA = base
	if out, err := gitCmd(s.repoRoot, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		s.branch.base = strings.TrimSpace(out)
	}
	if s.branch.base != "" {
		if out, err := gitCmd(s.repoRoot, "merge-base", branch, s.branch.base); err == nil {
			s.branch.baseSHA = strings.TrimSpace(out)
		}
	}
	commits, err := stepCommitsFromLog(s.repoRoot, branch)
	if err != nil {
		_ = removeWorktree(s.repoRoot, wtPath)
		s.branch.worktree = ""
		return err
	}
	s.stepCommits = commits
	return nil
}
