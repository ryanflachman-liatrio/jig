package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/ryanflachman-liatrio/jig/internal/step"
)

// ContainerLifecycle is local durable ownership evidence. Exporters must not
// expose daemon, endpoint, image or container identities.
type ContainerLifecycle struct {
	Ownership step.ContainerPhase `json:"ownership"`
}

func (ContainerLifecycle) isEvent() {}

type containerLifecycleMsg struct {
	stepID    string
	execution uint64
	ownership step.ContainerPhase
	reply     chan error
}

func (containerLifecycleMsg) isSchedMsg() {}

func bindContainerLifecycle(req *StepRequest, inbox chan<- schedMsg, execution uint64) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("allocate execution nonce: %w", err)
	}
	req.ExecutionNonce = hex.EncodeToString(nonce[:])
	runID, stepID, generation, iteration, attempt, executionNonce := req.RunID, req.Step.ID, req.Generation, req.Iteration, req.Attempt, req.ExecutionNonce
	req.ContainerLifecycle = func(ctx context.Context, p step.ContainerPhase) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		// The caller supplies runtime observations; dispatch coordinates are engine-owned.
		p.RunID, p.StepID, p.Generation, p.Iteration, p.Attempt, p.Nonce = runID, stepID, generation, iteration, attempt, executionNonce
		reply := make(chan error, 1)
		select {
		case inbox <- containerLifecycleMsg{stepID: stepID, execution: execution, ownership: p, reply: reply}:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case err := <-reply:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (m containerLifecycleMsg) execute(s *scheduler) {
	p := m.ownership
	state := s.states[m.stepID]
	if !s.matchesExecution(m.stepID, m.execution) || state == nil || p.Nonce != s.executionNonces[m.stepID] || p.RunID != s.runID || p.StepID != m.stepID || p.Generation != state.Generation || p.Iteration != state.Iteration || p.Attempt != state.Attempt || s.fatalJournalErr != nil {
		m.reply <- fmt.Errorf("container lifecycle acknowledgement rejected")
		return
	}
	if s.containerPhases == nil {
		s.containerPhases = make(step.ContainerLedger)
	}
	if err := s.containerPhases.Check(p); err != nil {
		m.reply <- err
		return
	}
	if p.State == step.ContainerIntent || p.State == step.ContainerCreated {
		if s.stopping[m.stepID] || s.fatalCaptureErr != nil || (p.Phase == "main" && state.Status != step.StatusRunning) || (p.Phase == "validation" && state.Status != step.StatusValidating) {
			m.reply <- fmt.Errorf("container launch acknowledgement rejected")
			return
		}
		if err := s.authorizeSandboxStep(s.stepByID(m.stepID)); err != nil {
			m.reply <- err
			return
		}
	}
	if err := s.emit(ContainerLifecycle{Ownership: p}); err != nil {
		m.reply <- err
		return
	}
	s.containerPhases[p.Key()] = p
	m.reply <- nil
}

func (s *scheduler) containerUncertain(stepID string) bool {
	for _, p := range s.containerPhases {
		if p.StepID == stepID && !p.Quiescent() {
			return true
		}
	}
	return false
}

type containerQuiescenceMsg struct {
	stepID    string
	execution uint64
	reply     chan error
}

func (containerQuiescenceMsg) isSchedMsg() {}
func (m containerQuiescenceMsg) execute(s *scheduler) {
	if !s.matchesExecution(m.stepID, m.execution) || s.stopping[m.stepID] || s.fatalJournalErr != nil || s.fatalCaptureErr != nil {
		m.reply <- fmt.Errorf("finalization acknowledgement rejected")
	} else if s.containerUncertain(m.stepID) {
		m.reply <- &ExecutionUncertainError{}
	} else {
		m.reply <- nil
	}
}

// A validator is optional; publication still requires confirmed quiescence.
func acknowledgeContainerQuiescence(ctx context.Context, inbox chan<- schedMsg, stepID string, execution uint64) error {
	reply := make(chan error, 1)
	select {
	case inbox <- containerQuiescenceMsg{stepID: stepID, execution: execution, reply: reply}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func checkContainerReopen(events []Event) error {
	ledger := make(step.ContainerLedger)
	for _, event := range events {
		if e, ok := event.(ContainerLifecycle); ok {
			if err := ledger.Check(e.Ownership); err != nil {
				return fmt.Errorf("resume container ownership: %w", err)
			}
			ledger[e.Ownership.Key()] = e.Ownership
		}
	}
	for _, p := range ledger {
		if p.Retained() {
			return fmt.Errorf("resume blocked by retained container ownership; inspect and remediate manually")
		}
	}
	return nil
}
