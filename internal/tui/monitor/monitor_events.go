package monitor

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/sentinel"
	"jig/internal/step"
	questionpanel "jig/internal/tui/question"
)

func (m Model) handleEngineEvent(e engine.Event) (Model, tea.Cmd) {
	switch ev := e.(type) {
	case engine.RunStarted:
		if ev.RunID != m.RunID {
			return m, nil
		}
		m.workflow = ev.Workflow
		m.steps = make([]monitorStep, len(ev.Steps))
		m.index = make(map[string]int, len(ev.Steps))
		for i, id := range ev.Steps {
			m.steps[i] = monitorStep{id: id, status: step.StatusPending}
			m.index[id] = i
		}

	case engine.StepStatus:
		if ev.RunID != m.RunID {
			return m, nil
		}
		i, ok := m.index[ev.StepID]
		if !ok {
			return m, nil
		}
		m.steps[i].status = ev.To
		m.steps[i].iteration = ev.Iteration
		m.steps[i].attempt = ev.Attempt
		if ev.To == step.StatusFailed {
			m.steps[i].err = ev.Err
			m.steps[i].subtype = ev.Subtype
		}
		if ev.To == step.StatusRunning {
			m.steps[i].start = time.Now()
		}
		if ev.To == step.StatusSucceeded || ev.To == step.StatusFailed || ev.To == step.StatusSkipped {
			m.steps[i].end = time.Now()
			// Re-discover output files when a step reaches a terminal state — the
			// runner may have just written output.md/output.json.
			if m.RunDir != "" {
				m.stepFiles[ev.StepID] = stepOutputFiles(m.RunDir, ev.StepID, "")
			}
		}
		// Every StepStatus carries the step's cumulative cost/tokens (the engine
		// accrues each attempt and never refunds a reset/retry), so reflect them on
		// every transition and refresh the running totals.
		m.steps[i].cost = ev.Cost
		m.steps[i].tokens = ev.Tokens
		m.recomputeTotals()
		// Remove every queue entry for a step that is no longer blocked. Prune on
		// any transition away from a parked state (needs_input, awaiting_recovery)
		// and on all terminal transitions — but not the entry into a parked state,
		// whose gate entry arrives on the following ctrl event.
		if ev.To != step.StatusNeedsInput && ev.To != step.StatusAwaitingRecovery && ev.To != step.StatusAwaitingIntegration {
			for i := len(m.inputQueue) - 1; i >= 0; i-- {
				if m.inputQueue[i].stepID == ev.StepID {
					m.removeEntryAt(i)
				}
			}
			// If the pruned step had an active textarea entry, clear the textarea.
			if len(m.inputQueue) == 0 {
				m.promptTextarea = textarea.Model{}
			}
		}

	case engine.ReviewRequest:
		if ev.RunID != m.RunID {
			return m, nil
		}
		// Retain the request so the Transcript panel can show the diff when the step
		// is selected (Unit 5), even after the queue entry is answered.
		if m.reviews == nil {
			m.reviews = make(map[string]engine.ReviewRequest)
		}
		m.reviews[ev.StepID] = ev
		// Append a queue entry. Decision 6: no focus steal on arrival.
		evCopy := ev
		m.inputQueue = append(m.inputQueue, pendingInputEntry{
			kind:   inputKindReview,
			stepID: ev.StepID,
			review: &evCopy,
		})

	case engine.RecoveryRequest:
		if ev.RunID != m.RunID {
			return m, nil
		}
		// A step failed and parked for a recovery decision. Append a gate entry; no
		// focus steal on arrival (Decision 6), consistent with the other kinds.
		evCopy := ev
		m.inputQueue = append(m.inputQueue, pendingInputEntry{
			kind:     inputKindRecovery,
			stepID:   ev.StepID,
			recovery: &evCopy,
		})

	case engine.IntegrationConflictRequest:
		if ev.RunID != m.RunID {
			return m, nil
		}
		// A step's squash-merge conflicted and parked. Append a gate entry; no
		// focus steal on arrival (Decision 6), consistent with the other kinds.
		evCopy := ev
		m.inputQueue = append(m.inputQueue, pendingInputEntry{
			kind:        inputKindIntegrationConflict,
			stepID:      ev.StepID,
			integration: &evCopy,
		})

	case engine.FinalMergeRequest:
		if ev.RunID != m.RunID {
			return m, nil
		}
		// The run reached terminal with a non-empty run branch; the operator lands or
		// discards it (spec 06 A3). No focus steal on arrival (Decision 6). The entry
		// is keyed by the run branch since there is no owning step.
		evCopy := ev
		m.inputQueue = append(m.inputQueue, pendingInputEntry{
			kind:       inputKindFinalMerge,
			stepID:     ev.RunBranch,
			finalMerge: &evCopy,
		})

	case engine.InputRequest:
		if ev.RunID != m.RunID {
			return m, nil
		}
		// Decision 6: no focus steal on arrival.
		evCopy := ev
		wasEmpty := len(m.inputQueue) == 0
		m.inputQueue = append(m.inputQueue, pendingInputEntry{
			kind:    inputKindRequest,
			stepID:  ev.StepID,
			request: &evCopy,
		})
		// Build the textarea for the first entry only (it becomes active at index 0;
		// loadActiveTextarea reads activeInputIdx). A non-empty arrival leaves the
		// active entry — and its in-progress textarea — untouched.
		if wasEmpty {
			m.loadActiveTextarea()
		}

	case engine.AgentQuestion:
		if ev.RunID != m.RunID {
			return m, nil
		}
		// Update the step badge immediately — the scheduler inbox notification
		// may be dropped under load, so drive the display from this reliable event.
		if idx, ok := m.index[ev.StepID]; ok {
			m.steps[idx].status = step.StatusNeedsInput
		}
		// Decision 6: no focus steal on arrival.
		m.inputQueue = append(m.inputQueue, pendingInputEntry{
			kind:     inputKindQuestion,
			stepID:   ev.StepID,
			question: questionpanel.New(ev.Request).Resize(m.gateInnerWidth(), m.gateBodyHeight()-gateHeaderRows),
		})

	case engine.AgentQuestionResolved:
		if ev.RunID != m.RunID {
			return m, nil
		}
		for i := range m.inputQueue {
			entry := &m.inputQueue[i]
			if entry.kind == inputKindQuestion && entry.question.Request().ID == ev.RequestID {
				m.removeEntryAt(i)
				break
			}
		}

	case engine.StepMessage:
		if ev.RunID != m.RunID {
			return m, nil
		}
		if m.msgCount == nil {
			m.msgCount = make(map[string]int)
		}
		if m.stepFiles == nil {
			m.stepFiles = make(map[string][]outputFile)
		}
		if ev.Seq > m.msgCount[ev.StepID] {
			m.msgCount[ev.StepID] = ev.Seq
		}
		// A StepMessage means a message was just finalized to the transcript, so
		// the live-typing tail for that step is now on disk: reset it (the next
		// deltas belong to the next, not-yet-finalized bubble). If that step's
		// chat is open, re-read the transcript so the finalized entry appears.
		if buf, ok := m.stepOutput[ev.StepID]; ok {
			buf.Reset()
		}
		// The Transcript panel always shows the cursor's step, so re-read whenever
		// the finalized entry belongs to it and follow is active. A paused or
		// paged-back view keeps its byte window stable until the operator follows
		// again, while msgCount still drives the unseen indicator.
		if ev.StepID == m.chatStep && m.chatAutoScroll {
			m.loadChatTail()
			m.chatSeenSeq = m.latestChatSeq()
		}

	case engine.PromptRequest:
		if ev.RunID != m.RunID {
			return m, nil
		}
		// Decision 6: no focus steal on arrival.
		evCopy := ev
		wasEmpty := len(m.inputQueue) == 0
		m.inputQueue = append(m.inputQueue, pendingInputEntry{
			kind:   inputKindPrompt,
			stepID: ev.StepID,
			prompt: &evCopy,
		})
		// Build the textarea for the first entry only (it becomes active at index 0);
		// loadActiveTextarea derives the placeholder from the prompt label. A
		// non-empty arrival leaves the active entry's textarea untouched.
		if wasEmpty {
			m.loadActiveTextarea()
		}

	case engine.StepOutput:
		if ev.RunID != m.RunID {
			return m, nil
		}
		if m.stepOutput == nil {
			m.stepOutput = make(map[string]*strings.Builder)
		}
		buf, ok := m.stepOutput[ev.StepID]
		if !ok {
			buf = &strings.Builder{}
			m.stepOutput[ev.StepID] = buf
		}
		buf.WriteString(ev.Delta)

	case engine.RunError:
		if ev.RunID != m.RunID {
			return m, nil
		}
		// Engine-level failures (worktree setup, max_iterations) are not tied to a
		// single step's transition, so they would otherwise vanish. Retain the
		// most recent one for the summary.
		m.runErr = ev.Err
		// The run handle is no longer useful after a terminal error; clear it so
		// ctrl+h shows the "unavailable" message instead of trying to dispatch
		// recovery actions against a dead run.
		m.run = nil

	case engine.RunFinished:
		if ev.RunID != m.RunID {
			return m, nil
		}
		m.done = true
		m.failed = ev.Failed
		// Clear the live handle so ctrl+h shows "unavailable" for completed runs.
		m.run = nil

	case engine.SecurityFinding:
		if ev.RunID != m.RunID {
			return m, nil
		}
		// File is truth: read all findings from findings.jsonl to get the full
		// Detail field (redacted-secret preview) that the event omits.
		fPath := datastore.FindingsPath(m.RunDir)
		if findings, err := sentinel.ReadAll(fPath); err == nil {
			m.secFindings = findings
		} else {
			// Persistence off or file not yet written — synthesize a minimal record
			// from the event fields so the Security pane still populates.
			m.secFindings = append(m.secFindings, sentinel.Finding{
				StepID:      ev.StepID,
				Tier:        sentinel.Tier(ev.Tier),
				Monitor:     ev.Monitor,
				Severity:    sentinel.Severity(ev.Severity),
				Action:      sentinel.Action(ev.Action),
				Fingerprint: ev.Fingerprint,
			})
		}
		// Findings arrive after WindowSizeMsg, so the existing viewports still
		// occupy the rows now needed by the security summary until they are refit.
		m.resize()
	}
	return m, nil
}
