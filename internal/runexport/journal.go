package runexport

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"math"
	"sort"

	"jig/internal/engine"
	"jig/internal/step"
)

// decodedRecord is one accepted journal line: its envelope plus the typed
// Event (nil for a syntactically valid but unrecognized kind).
type decodedRecord struct {
	env   engine.Envelope
	event engine.Event
}

// journalResult is the outcome of projecting journal.jsonl: the accepted
// prefix, the folded record view ops.FoldStatus expects, and at most one gap
// describing why the prefix ended early (nil when the whole file was read).
type journalResult struct {
	records []decodedRecord
	gap     *Gap
}

// decodeJournalPrefix streams r one newline-terminated record at a time and
// keeps only the valid prefix (spec FR-13): decoding stops at the first
// malformed complete record or oversized record, and a torn final record
// (no trailing newline) is omitted and reported rather than guessed at. An
// unknown kind is kept — UnmarshalEnvelope reports (env, nil, nil) for it —
// so the caller can still emit a fixed "unknown" event for it.
func decodeJournalPrefix(r io.Reader) journalResult {
	br := bufio.NewReaderSize(r, 64*1024)
	var out journalResult
	line := 0
	for {
		line++
		data, err := readBoundedLine(br, maxInputRecord)
		switch {
		case errors.Is(err, io.EOF):
			return out
		case errors.Is(err, errLineTorn):
			out.gap = &Gap{Reason: GapTornRecord, Source: "journal", Line: line}
			return out
		case errors.Is(err, errLineOversized):
			out.gap = &Gap{Reason: GapOversizedRecord, Source: "journal", Line: line}
			return out
		case err != nil:
			out.gap = &Gap{Reason: GapCorruptSource, Source: "journal", Line: line}
			return out
		}
		if len(data) == 0 {
			continue
		}
		env, event, decErr := engine.UnmarshalEnvelope(data)
		if decErr != nil {
			out.gap = &Gap{Reason: GapMalformedRecord, Source: "journal", Line: line}
			return out
		}
		out.records = append(out.records, decodedRecord{env: env, event: event})
	}
}

// engineJournalRecords adapts the accepted prefix into the shape
// ops.FoldStatus consumes, so state/totals folding is never reimplemented for
// export.
func (jr journalResult) engineJournalRecords() []engine.JournalRecord {
	out := make([]engine.JournalRecord, len(jr.records))
	for i, r := range jr.records {
		out[i] = engine.JournalRecord{Seq: r.env.Seq, Timestamp: r.env.Ts, Event: r.event}
	}
	return out
}

// seedAliases allocates step aliases in the prescribed precedence (spec
// FR-05): RunStarted.Steps declared order, then first journal occurrence,
// then (by the caller, via seedDiscoveredSteps) sorted directory discovery.
// A single forward pass suffices because allocateStep is idempotent.
func (jr journalResult) seedAliases(aliases *aliasTable) {
	for _, r := range jr.records {
		switch event := r.event.(type) {
		case engine.RunStarted:
			aliases.seedStepOrder(event.Steps)
		case engine.StepStatus:
			aliases.allocateStep(event.StepID)
		case engine.FanOutExpanded:
			aliases.allocateStep(event.FamilyID)
			ids := make([]string, 0, len(event.Instances))
			for _, inst := range event.Instances {
				ids = append(ids, inst.InstanceID)
			}
			sort.Strings(ids)
			for _, id := range ids {
				aliases.allocateStep(id)
			}
		case engine.StepMessage:
			aliases.allocateStep(event.StepID)
		case engine.GateResult:
			aliases.allocateStep(event.StepID)
		case engine.RouteSelected:
			aliases.allocateStep(event.StepID)
		case engine.RouteCapExceeded:
			aliases.allocateStep(event.StepID)
		case engine.StepsReset:
			aliases.allocateStep(event.Target)
			for _, id := range event.Closure {
				aliases.allocateStep(id)
			}
		case engine.ReviewRequest:
			aliases.allocateStep(event.StepID)
		case engine.ReviewSubmitted:
			aliases.allocateStep(event.StepID)
		case engine.InputRequest:
			aliases.allocateStep(event.StepID)
		case engine.RecoveryRequest:
			aliases.allocateStep(event.StepID)
		case engine.IntegrationConflictRequest:
			aliases.allocateStep(event.StepID)
		case engine.PromptRequest:
			aliases.allocateStep(event.StepID)
		case engine.AgentQuestion:
			aliases.allocateStep(event.StepID)
		case engine.AgentQuestionResolved:
			aliases.allocateStep(event.StepID)
		case engine.SecurityFinding:
			aliases.allocateStep(event.StepID)
		case engine.StepOutput:
			aliases.allocateStep(event.StepID)
		case engine.StepToolCall:
			aliases.allocateStep(event.StepID)
		case engine.RunError:
			// RunID only; no step reference.
		}
	}
}

// knownStatuses is the closed set of step.Status values the projection will
// pass through; anything else becomes "unknown".
var knownStatuses = map[step.Status]bool{
	step.StatusPending: true, step.StatusRunning: true, step.StatusValidating: true,
	step.StatusAwaitingReview: true, step.StatusNeedsInput: true, step.StatusAwaitingRecovery: true,
	step.StatusAwaitingIntegration: true, step.StatusStopped: true, step.StatusSucceeded: true,
	step.StatusFailed: true, step.StatusSkipped: true,
}

func projectedStatus(s step.Status) string {
	if knownStatuses[s] {
		return string(s)
	}
	return "unknown"
}

func finiteNonNegative(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0
}

// projectEvent builds the closed structural projection for one accepted
// journal record (spec's "structural event projection" table). Unknown kinds
// (event == nil) produce a bare unknown record with sequence/time only.
func projectEvent(rec decodedRecord, aliases *aliasTable, anchor *timeAnchor) EventRecord {
	out := EventRecord{Seq: rec.env.Seq, TimeMS: anchor.relativeMS(rec.env.Ts), Kind: rec.env.Kind}
	if rec.event == nil {
		out.Kind = "unknown"
		return out
	}
	switch event := rec.event.(type) {
	case engine.RunStarted:
		steps := make([]string, len(event.Steps))
		for i, id := range event.Steps {
			steps[i] = aliases.stepAlias(id)
		}
		out.Data = mustJSON(map[string]any{"steps": steps})
	case engine.RunFinished:
		out.Data = mustJSON(map[string]any{"failed": event.Failed})
	case engine.StepStatus:
		fields := map[string]any{
			"step":       aliases.stepAlias(event.StepID),
			"from":       projectedStatus(event.From),
			"to":         projectedStatus(event.To),
			"attempt":    event.Attempt,
			"iteration":  event.Iteration,
			"generation": event.Generation,
		}
		if event.Cost != nil && finiteNonNegative(*event.Cost) {
			fields["cost_usd"] = *event.Cost
		}
		if event.Tokens >= 0 {
			fields["tokens"] = event.Tokens
		}
		out.Data = mustJSON(fields)
	case engine.RouteSelected:
		out.Data = mustJSON(map[string]any{
			"step": aliases.stepAlias(event.StepID), "goto": aliases.stepAlias(event.Goto),
			"route_index": event.RouteIndex, "iteration": event.Iteration, "max": event.Max,
		})
	case engine.RouteCapExceeded:
		out.Data = mustJSON(map[string]any{
			"step": aliases.stepAlias(event.StepID), "goto": aliases.stepAlias(event.Goto),
			"route_index": event.RouteIndex, "iteration": event.Iteration, "max": event.Max,
		})
	case engine.StepsReset:
		closure := make([]string, len(event.Closure))
		for i, id := range event.Closure {
			closure[i] = aliases.stepAlias(id)
		}
		out.Data = mustJSON(map[string]any{"target": aliases.stepAlias(event.Target), "closure": closure})
	case engine.FanOutExpanded:
		children := make([]string, len(event.Instances))
		for i, inst := range event.Instances {
			children[i] = aliases.stepAlias(inst.InstanceID)
		}
		out.Data = mustJSON(map[string]any{
			"family": aliases.stepAlias(event.FamilyID), "generation": event.Generation,
			"iteration": event.Iteration, "children": children, "total": len(event.Instances),
		})
	case engine.GateResult:
		out.Data = mustJSON(map[string]any{"step": aliases.stepAlias(event.StepID), "passed": event.Passed})
	case engine.ReviewRequest:
		out.Data = mustJSON(map[string]any{"step": aliases.stepAlias(event.StepID)})
	case engine.ReviewSubmitted:
		out.Data = mustJSON(map[string]any{"step": aliases.stepAlias(event.StepID), "comment_count": event.CommentCount})
	case engine.InputRequest:
		out.Data = mustJSON(map[string]any{"step": aliases.stepAlias(event.StepID)})
	case engine.RecoveryRequest:
		out.Data = mustJSON(map[string]any{"step": aliases.stepAlias(event.StepID)})
	case engine.IntegrationConflictRequest:
		out.Data = mustJSON(map[string]any{"step": aliases.stepAlias(event.StepID)})
	case engine.FinalMergeRequest:
		out.Data = mustJSON(map[string]any{})
	case engine.PromptRequest:
		out.Data = mustJSON(map[string]any{"step": aliases.stepAlias(event.StepID)})
	case engine.AgentQuestion:
		out.Data = mustJSON(map[string]any{"step": aliases.stepAlias(event.StepID)})
	case engine.AgentQuestionResolved:
		out.Data = mustJSON(map[string]any{"step": aliases.stepAlias(event.StepID)})
	case engine.SecurityFinding:
		out.Data = mustJSON(map[string]any{"step": aliases.stepAlias(event.StepID)})
	case engine.StepMessage:
		out.Data = mustJSON(map[string]any{"step": aliases.stepAlias(event.StepID), "transcript_seq": event.Seq, "iteration": event.Iteration})
	case engine.StepOutput:
		out.Data = mustJSON(map[string]any{"step": aliases.stepAlias(event.StepID)})
	case engine.StepToolCall:
		out.Data = mustJSON(map[string]any{"step": aliases.stepAlias(event.StepID)})
	case engine.RunError:
		out.Data = mustJSON(map[string]any{})
	default:
		out.Kind = "unknown"
		out.Data = nil
	}
	return out
}

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}
