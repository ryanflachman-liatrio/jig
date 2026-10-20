package engine

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/agentcfg"
	"github.com/ryanflachman-liatrio/jig/internal/manifest"
	"github.com/ryanflachman-liatrio/jig/internal/step"
)

func (s *scheduler) transition(stepID string, from, to step.Status) bool {
	return s.transitionRecovery(stepID, from, to, "")
}

func (s *scheduler) transitionRecovery(stepID string, from, to step.Status, recoveryAction string) bool {
	state := s.states[stepID]
	previous, previousTiming := state.Status, state.Timing
	if err := s.emit(s.transitionEvent(stepID, from, to, recoveryAction)); err != nil {
		state.Status, state.Timing = previous, previousTiming
		return false
	}
	// Centralized fan-out barrier settlement: every path that can complete a
	// child (normal success, on_failure="continue", operator recovery-skip, a
	// resumed/recovered child reaching a real terminal status, validation, and
	// integration completion) flows through this one transition function, so
	// this single hook — rather than editing each of those call sites — is
	// what checks whether a settling family's barrier can now close. It is a
	// no-op for every ordinary step and for the family's own transition.
	// terminalStatus's three statuses (succeeded/failed/skipped) are exactly
	// the set that can satisfy a fan-out family's barrier for one child.
	// Parked-but-alive statuses (stopped, awaiting_recovery,
	// awaiting_integration, needs_input) are deliberately excluded: the
	// family keeps waiting until that child later reaches a real terminal
	// status.
	if to.IsTerminal() {
		s.trySettleFamilyIfChild(stepID)
	}
	return true
}

func (s *scheduler) transitionEvent(stepID string, from, to step.Status, recoveryAction string) StepStatus {
	state := s.states[stepID]
	state.Status = to
	at := time.Now().UTC()
	state.Timing.Apply(to, at)
	timing := state.Timing
	ev := StepStatus{
		At:             at,
		Timing:         &timing,
		RunID:          s.runID,
		StepID:         stepID,
		From:           from,
		To:             to,
		Attempt:        state.Attempt,
		Iteration:      state.Iteration,
		Generation:     state.Generation,
		RecoveryAction: recoveryAction,
	}
	// Carry the failure reason and subtype so the TUI can surface them without
	// re-reading result.json. handle() guarantees state.Result.Err is populated
	// (executor error or gate detail) before a step transitions to Failed.
	if to == step.StatusFailed && state.Result != nil {
		ev.Err = state.Result.Err
		ev.Subtype = state.Result.Subtype
	}
	// Attach the step's cumulative cost/tokens so the monitor can display per-step
	// figures and a running total live (and on journal replay) without re-reading
	// result.json. Carried on every transition — not just the terminal one — so a
	// step re-running after a reset/retry keeps showing what earlier attempts
	// already cost instead of blanking to zero. Zero means "nothing spent yet".
	if state.SpentUSD > 0 {
		cost := state.SpentUSD
		ev.Cost = &cost
	}
	ev.Tokens = state.SpentTokens
	return ev
}

// emit writes an event to the journal (if a writer is configured), then fans
// it out to subscribers.  The journal write is synchronous and happens before
// any subscriber receives the event, preserving the "journal before fan-out"
// invariant: in-memory state is always fold(journal).
// emit is called only from the scheduler goroutine.
func (s *scheduler) emit(e Event) error {
	nextSeq := s.seq + 1
	if s.writer != nil {
		line, err := MarshalEnvelope(nextSeq, e)
		if err != nil {
			s.failJournal(err)
			return err
		}
		if err := s.writer.AppendLine(line, s.terminalManifest(e)); err != nil {
			s.failJournal(err)
			return err
		}
	}
	s.seq = nextSeq
	s.fanOut(e)
	return nil
}

func (s *scheduler) failJournal(err error) {
	if s.fatalJournalErr == nil {
		s.fatalJournalErr = err
	}
	if s.cancel != nil {
		s.cancel()
	}
}

// emitBatch persists a recovery transaction before exposing any member of the
// batch. It is used during Resume before runLoop starts, so returning an error
// lets the caller unwind scheduler registration and the run lock cleanly.
func (s *scheduler) emitBatch(events []Event) error {
	if len(events) == 0 {
		return nil
	}
	lines := make([][]byte, 0, len(events))
	nextSeq := s.seq
	for _, event := range events {
		nextSeq++
		line, err := MarshalEnvelope(nextSeq, event)
		if err != nil {
			return err
		}
		lines = append(lines, line)
	}
	if s.writer != nil {
		if err := s.writer.AppendBatch(lines); err != nil {
			return err
		}
	}
	s.seq = nextSeq
	for _, event := range events {
		s.fanOut(event)
	}
	return nil
}

func (s *scheduler) terminalManifest(e Event) *manifest.StepTerminal {
	ss, ok := e.(StepStatus)
	if !ok || !ss.To.IsTerminal() {
		return nil
	}
	state := s.states[ss.StepID]
	term := &manifest.StepTerminal{StepID: ss.StepID, Status: string(ss.To), Attempt: state.Attempt}
	if state.Result != nil {
		term.TotalCostUSD = state.Result.TotalCostUSD
		term.Result = state.Result
	}
	if wfStep := s.stepByID(ss.StepID); wfStep != nil {
		term.Backend = wfStep.Backend
		term.Model = wfStep.Model
		if a := wfStep.ResolvedAgent(); a != nil {
			if c, ok := a.(agentcfg.ClaudeAgent); ok {
				term.ToolPolicy = append(append([]string{}, c.Tools...), c.DisallowedTools...)
			}
			term.AgentPosture = agentcfg.Posture(a)
		}
	}
	term.IntegrationCommit = s.stepCommits[ss.StepID]
	if diff := s.rt(ss.StepID).diff; diff != "" {
		term.DiffSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(diff)))
	}
	return term
}

func (s *scheduler) fanOut(e Event) {
	switch e.(type) {
	case StepOutput, StepToolCall, StepMessage:
		fanOutLive(s.subs, e)
	default:
		fanOutCtrl(s.subs, e)
		if s.observers != nil {
			s.observers.dispatchPublish(e)
		}
	}
}

// snapshot is the Memento pattern's originator method: only scheduler (which
// owns all of a run's mutable state) knows how to capture that state into a
// RunSnapshot memento. Callers (Manager, replay.go) never reach into
// scheduler fields directly — they call snapshot()/the exported Snapshot
// wrappers and treat the result as opaque.
func (s *scheduler) snapshot() RunSnapshot {
	states := make([]step.State, len(s.wf.Steps))
	allDone := true
	var totalCost float64
	var totalTokens int
	for i, wfStep := range s.wf.Steps {
		st := s.states[wfStep.ID]
		states[i] = *st
		if !st.Status.IsTerminal() {
			allDone = false
		}
		// Cumulative across every attempt (see step.State.SpentUSD), so a run that
		// retried or reset a step reports the full amount it actually paid.
		totalCost += st.SpentUSD
		totalTokens += st.SpentTokens
	}
	// Runtime fan-out children never appear in wf.Steps (the static DAG stays
	// pure), so they are folded in here, family by family in expansion order
	// and source order within each family, to keep RunSnapshot deterministic.
	// The family step itself is a zero-cost barrier (its own SpentUSD/Tokens
	// stay at zero, counted once above) — only its children add to the totals,
	// so nothing is double-counted.
	for _, familyID := range s.foreach.order {
		fam := s.foreach.families[familyID]
		if fam == nil {
			continue
		}
		for _, childID := range fam.Order {
			st, ok := s.states[childID]
			if !ok {
				continue
			}
			states = append(states, *st)
			if !st.Status.IsTerminal() {
				allDone = false
			}
			totalCost += st.SpentUSD
			totalTokens += st.SpentTokens
		}
	}
	return RunSnapshot{
		ID:           s.runID,
		Workflow:     s.wf.Meta.Name,
		Steps:        states,
		Done:         allDone,
		Failed:       s.anyFailed(),
		TotalCostUSD: totalCost,
		TotalTokens:  totalTokens,
	}
}

// fanOutLive is the Observer pattern's notify step for the live channel (see
// Subscribe): it pushes e to every registered observer's live channel,
// dropping for slow consumers. Live events (StepOutput, StepToolCall,
// StepMessage) are liveness signals only; the durable content lives in the
// per-step transcript on disk.
func fanOutLive(subs []sub, e Event) {
	for _, s := range subs {
		select {
		case s.live <- e:
		default:
		}
	}
}

// fanOutCtrl is the Observer pattern's notify step for the ctrl channel (see
// Subscribe): it pushes e to every registered observer's ctrl channel,
// dropping for slow consumers. Ctrl events are critical (RunFinished,
// ReviewRequest, etc.) but the ctrl channel is sized for worst-case workflow
// volume, so drops should not occur in practice.
func fanOutCtrl(subs []sub, e Event) {
	for _, s := range subs {
		select {
		case s.ctrl <- e:
		default:
		}
	}
}
