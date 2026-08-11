// Package step defines the per-step runtime model: Status, State, and Result.
// It is pure data — imported by engine, runner, manifest, and tui, but never
// imports any of them.
package step

import (
	"encoding/json"
	"time"
)

// Status is the lifecycle state of one step in a run.
type Status string

const (
	StatusPending        Status = "pending" // deps not yet satisfied
	StatusRunning        Status = "running"
	StatusValidating     Status = "validating"      // [step.validate] in progress
	StatusAwaitingReview Status = "awaiting_review" // parked on a human verdict
	StatusNeedsInput     Status = "needs_input"     // blocked by block_on, waiting for human input
	// StatusAwaitingRecovery is a failed step parked for a human recovery
	// decision instead of aborting the run: retry fresh, resume the failed agent
	// session with the error fed back in, or abort. It keeps the run and any
	// in-flight sibling steps alive while the human decides.
	StatusAwaitingRecovery Status = "awaiting_recovery"
	// StatusAwaitingIntegration is a step whose squash-merge into the run branch
	// hit a conflict and is parked for a human to resolve in the run worktree
	// (spec 06 A2). Like StatusAwaitingRecovery it is parked-but-alive: the run and
	// in-flight siblings keep running while the operator resolves or aborts.
	StatusAwaitingIntegration Status = "awaiting_integration"
	// StatusStopped is a step whose worker was deliberately stopped mid-run by an
	// operator (spec 07 B1) — not a failure and not end-of-run. Like the awaiting_*
	// states it is parked-but-alive: the run stays quiescent (no worker in flight)
	// and any in-flight siblings keep running. A stopped step is eligible to be
	// resumed (Run.Resume continues its agent session, spec 07 B2) or, once
	// Feature C lands, reset. Its partial worktree diff and transcript are captured
	// on cancel, so stopping never discards work.
	StatusStopped   Status = "stopped"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusSkipped   Status = "skipped" // when=false, or dep skipped/failed
)

// State is the scheduler's mutable record for one step.
// Only the scheduler goroutine reads or writes it.
type State struct {
	ID        string
	Status    Status
	Attempt   int // retry count under on_failure = "retry"
	Iteration int // loop iteration when re-run via [step.loop]
	// Generation counts manual operator resets (Run.Reset). Unlike Attempt,
	// which gates the MaxRetries budget, Generation is purely a provenance axis
	// that makes re-runs legible in the transcript and gates nothing.
	Generation int
	Result     *Result
	// SpentUSD / SpentTokens accumulate the cost and tokens of every executor
	// attempt for this step — across retries, loop re-runs, resets, and resumed
	// sessions. Result holds only the latest attempt; these are cumulative,
	// because a reset or retry does not refund what an earlier attempt already
	// cost. The scheduler adds each completed attempt's figures here and never
	// zeroes them (reset clears Result/Attempt/Iteration but keeps spend).
	SpentUSD    float64
	SpentTokens int
}

// Result is what execution produced; serialized as result.json by the manifest
// writer (Phase 2+).
type Result struct {
	Status       Status          `json:"status"`
	OutputPath   string          `json:"output_path,omitempty"`
	Structured   json.RawMessage `json:"structured,omitempty"`
	Verdict      string          `json:"verdict,omitempty"`
	ChangedFiles []string        `json:"changed_files,omitempty"`
	Duration     time.Duration   `json:"duration_ms"`
	Err          string          `json:"error,omitempty"`
	SessionID    string          `json:"session_id,omitempty"`
	// Subtype is the SDK's ResultMessage.Subtype — the closest thing to a
	// turn-level "why did this end" (e.g. a clean finish vs. hitting max_turns).
	Subtype string `json:"subtype,omitempty"`
	// TotalCostUSD is the dollar cost reported by the SDK for this agent step.
	// A nil pointer means the SDK did not report cost (vs. a reported $0.00).
	TotalCostUSD *float64        `json:"total_cost_usd,omitempty"`
	Usage        *map[string]any `json:"usage,omitempty"`
}

// TokenCount sums the token buckets in the SDK usage map — input, output, and
// both cache buckets — into a single total for the tokens the step processed.
// Returns (0, false) when no usage was reported so callers can distinguish
// "not yet known" from a genuine zero. The SDK decodes usage from JSON, so its
// numeric values arrive as float64.
func (r *Result) TokenCount() (int, bool) {
	if r.Usage == nil {
		return 0, false
	}
	usage := *r.Usage
	total, found := 0, false
	for _, k := range []string{
		"input_tokens",
		"output_tokens",
		"cache_creation_input_tokens",
		"cache_read_input_tokens",
	} {
		if f, ok := usage[k].(float64); ok {
			total += int(f)
			found = true
		}
	}
	return total, found
}
