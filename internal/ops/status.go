// Package ops provides non-interactive inspection and guarded control of persisted runs.
package ops

import (
	"fmt"
	"sort"
	"time"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/step"
)

type RunState string

const (
	StateSucceeded   RunState = "succeeded"
	StateFailed      RunState = "failed"
	StateOrphaned    RunState = "orphaned"
	StateCorrupt     RunState = "corrupt"
	StateActive      RunState = "active"
	StateInterrupted RunState = "interrupted"
	StatePaused      RunState = "paused"
)

type WaitReport struct {
	StepID string `json:"step_id,omitempty"`
	Kind   string `json:"kind"`
}

type StepReport struct {
	ID         string      `json:"id"`
	Status     step.Status `json:"status"`
	Attempt    int         `json:"attempt"`
	Iteration  int         `json:"iteration"`
	Generation int         `json:"generation"`
	Error      string      `json:"error"`
	Subtype    string      `json:"subtype"`
	CostUSD    float64     `json:"cost_usd"`
	Tokens     int         `json:"tokens"`

	// ParentID, FanOutIndex, and FanOutTotal are runtime fan-out provenance
	// (A8): ParentID is the declaring family's step id for a child, "" for
	// every ordinary step (including the family step itself, which stays a
	// zero-cost barrier reported like any other step). FanOutIndex/FanOutTotal
	// are the child's source position and the family's current-generation item
	// count. Omitted from JSON when empty/zero so ordinary status output is
	// unchanged.
	ParentID    string `json:"parent_id,omitempty"`
	FanOutIndex int    `json:"fan_out_index,omitempty"`
	FanOutTotal int    `json:"fan_out_total,omitempty"`
}

type RunReport struct {
	RunID        string       `json:"run_id"`
	Workflow     string       `json:"workflow"`
	State        RunState     `json:"state"`
	LockHeld     bool         `json:"lock_held"`
	Reopenable   bool         `json:"reopenable"`
	StartedAt    *time.Time   `json:"started_at"`
	UpdatedAt    *time.Time   `json:"updated_at"`
	FinishedAt   *time.Time   `json:"finished_at"`
	TotalCostUSD float64      `json:"total_cost_usd"`
	TotalTokens  int          `json:"total_tokens"`
	WaitingOn    []WaitReport `json:"waiting_on"`
	Steps        []StepReport `json:"steps"`
}

// InspectRun returns a durable status view. A corrupt journal returns both its
// decodable-prefix report and an error so clients can render diagnosis and fail.
func InspectRun(root, runID string) (RunReport, error) {
	runDir, err := datastore.ResolveRunDir(root, runID)
	if err != nil {
		return RunReport{RunID: runID}, err
	}
	records, replayErr := engine.ReplayJournalRecords(runDir)
	held, lockErr := engine.RunLockState(runDir)
	if lockErr != nil {
		return RunReport{RunID: runID}, lockErr
	}
	// A scheduler may release the lock between the first replay and the probe.
	// Re-read once after observing it free so a just-appended RunFinished wins.
	if !held {
		records, replayErr = engine.ReplayJournalRecords(runDir)
	}
	report := FoldStatus(runID, records, held)
	if len(records) == 0 {
		report.State = StateOrphaned
		report.Reopenable = false
		return report, fmt.Errorf("run %q has no readable journal records", runID)
	}
	if replayErr != nil {
		report.State = StateCorrupt
		report.Reopenable = false
		return report, replayErr
	}
	return report, nil
}

// ListRuns returns newest-first durable reports. Individual corrupt/orphaned
// histories remain in the result; err reports the first unhealthy history.
func ListRuns(root string) ([]RunReport, error) {
	if root == "" {
		return nil, fmt.Errorf("status: persistence root is empty")
	}
	ids, err := datastore.ListRunIDs(root)
	if err != nil {
		return nil, err
	}
	reports := make([]RunReport, 0, len(ids))
	var firstErr error
	for i := len(ids) - 1; i >= 0; i-- {
		report, inspectErr := InspectRun(root, ids[i])
		reports = append(reports, report)
		if inspectErr != nil && firstErr == nil {
			firstErr = inspectErr
		}
	}
	return reports, firstErr
}

func FoldStatus(runID string, records []engine.JournalRecord, lockHeld bool) RunReport {
	report := RunReport{RunID: runID, LockHeld: lockHeld, WaitingOn: []WaitReport{}, Steps: []StepReport{}}
	stepIndex := map[string]int{}
	waits := map[string]WaitReport{}
	finished := false
	failed := false

	for _, record := range records {
		if !record.Timestamp.IsZero() {
			ts := record.Timestamp
			report.UpdatedAt = &ts
		}
		switch event := record.Event.(type) {
		case engine.RunStarted:
			report.RunID = event.RunID
			report.Workflow = event.Workflow
			if !record.Timestamp.IsZero() {
				ts := record.Timestamp
				report.StartedAt = &ts
			}
			for _, id := range event.Steps {
				if _, ok := stepIndex[id]; ok {
					continue
				}
				stepIndex[id] = len(report.Steps)
				report.Steps = append(report.Steps, StepReport{ID: id, Status: step.StatusPending})
			}
		case engine.FanOutExpanded:
			// Pre-register every child with its family/index/total (A8) so
			// parent_id/index/total are always present, even for a child whose
			// first StepStatus hasn't been journaled yet, and so ordering
			// (below its family) is stable regardless of dispatch order.
			for _, inst := range event.Instances {
				if _, ok := stepIndex[inst.InstanceID]; ok {
					continue
				}
				stepIndex[inst.InstanceID] = len(report.Steps)
				report.Steps = append(report.Steps, StepReport{
					ID:          inst.InstanceID,
					Status:      step.StatusPending,
					ParentID:    event.FamilyID,
					FanOutIndex: inst.Index,
					FanOutTotal: len(event.Instances),
				})
			}
		case engine.StepStatus:
			i, ok := stepIndex[event.StepID]
			if !ok {
				i = len(report.Steps)
				stepIndex[event.StepID] = i
				report.Steps = append(report.Steps, StepReport{ID: event.StepID})
			}
			sr := &report.Steps[i]
			sr.Status, sr.Attempt, sr.Iteration, sr.Generation = event.To, event.Attempt, event.Iteration, event.Generation
			sr.Error, sr.Subtype = event.Err, event.Subtype
			if event.Cost != nil {
				sr.CostUSD = *event.Cost
			}
			sr.Tokens = event.Tokens
			if kind := waitKind(event.To); kind != "" {
				waits[event.StepID+"\x00"+kind] = WaitReport{StepID: event.StepID, Kind: kind}
			} else {
				for key, wait := range waits {
					if wait.StepID == event.StepID {
						delete(waits, key)
					}
				}
			}
		case engine.ReviewRequest:
			waits[event.StepID+"\x00review"] = WaitReport{StepID: event.StepID, Kind: "review"}
		case engine.PromptRequest:
			waits[event.StepID+"\x00prompt"] = WaitReport{StepID: event.StepID, Kind: "prompt"}
		case engine.InputRequest:
			waits[event.StepID+"\x00input"] = WaitReport{StepID: event.StepID, Kind: "input"}
		case engine.AgentQuestion:
			waits[event.StepID+"\x00question"] = WaitReport{StepID: event.StepID, Kind: "question"}
		case engine.RecoveryRequest:
			waits[event.StepID+"\x00recovery"] = WaitReport{StepID: event.StepID, Kind: "recovery"}
			if i, ok := stepIndex[event.StepID]; ok && event.Err != "" {
				report.Steps[i].Error = event.Err
			}
		case engine.IntegrationConflictRequest:
			waits[event.StepID+"\x00integration"] = WaitReport{StepID: event.StepID, Kind: "integration"}
		case engine.FinalMergeRequest:
			waits["\x00final_merge"] = WaitReport{Kind: "final_merge"}
		case engine.RunFinished:
			finished, failed = true, event.Failed
			if !record.Timestamp.IsZero() {
				ts := record.Timestamp
				report.FinishedAt = &ts
			}
		}
	}

	report.Steps = orderStepsWithChildren(report.Steps)

	for _, sr := range report.Steps {
		report.TotalCostUSD += sr.CostUSD
		report.TotalTokens += sr.Tokens
	}
	for _, wait := range waits {
		report.WaitingOn = append(report.WaitingOn, wait)
	}
	sort.Slice(report.WaitingOn, func(i, j int) bool {
		if report.WaitingOn[i].StepID == report.WaitingOn[j].StepID {
			return report.WaitingOn[i].Kind < report.WaitingOn[j].Kind
		}
		return report.WaitingOn[i].StepID < report.WaitingOn[j].StepID
	})

	switch {
	case finished && !failed:
		report.State = StateSucceeded
	case finished:
		report.State = StateFailed
	case lockHeld:
		report.State = StateActive
	case hasInterrupted(report.Steps):
		report.State = StateInterrupted
	default:
		report.State = StatePaused
	}
	report.Reopenable = !finished && !lockHeld && len(records) > 0
	return report
}

// orderStepsWithChildren places every foreach family's runtime children
// (across every generation, in the order their FanOutExpanded/StepStatus
// events were folded) directly beneath their family, without disturbing the
// relative order of ordinary (non-child) steps. A child never appears at the
// top level.
func orderStepsWithChildren(steps []StepReport) []StepReport {
	children := map[string][]StepReport{}
	hasChildren := false
	top := make([]StepReport, 0, len(steps))
	for _, s := range steps {
		if s.ParentID != "" {
			children[s.ParentID] = append(children[s.ParentID], s)
			hasChildren = true
			continue
		}
		top = append(top, s)
	}
	if !hasChildren {
		return steps
	}
	out := make([]StepReport, 0, len(steps))
	for _, s := range top {
		out = append(out, s)
		out = append(out, children[s.ID]...)
	}
	return out
}

func hasInterrupted(steps []StepReport) bool {
	for _, s := range steps {
		if s.Status == step.StatusRunning || s.Status == step.StatusValidating {
			return true
		}
	}
	return false
}

func waitKind(status step.Status) string {
	switch status {
	case step.StatusAwaitingReview:
		return "review"
	case step.StatusNeedsInput:
		return "input"
	case step.StatusAwaitingRecovery:
		return "recovery"
	case step.StatusAwaitingIntegration:
		return "integration"
	case step.StatusStopped:
		return "stopped"
	default:
		return ""
	}
}
