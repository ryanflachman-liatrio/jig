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
	StatusSucceeded      Status = "succeeded"
	StatusFailed         Status = "failed"
	StatusSkipped        Status = "skipped" // when=false, or dep skipped/failed
)

// State is the scheduler's mutable record for one step.
// Only the scheduler goroutine reads or writes it.
type State struct {
	ID        string
	Status    Status
	Attempt   int // retry count under on_failure = "retry"
	Iteration int // loop iteration when re-run via [step.loop]
	Result    *Result
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
}
