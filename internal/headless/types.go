package headless

import (
	"errors"
	"fmt"
	"io"
	"time"

	"jig/internal/engine"
	"jig/internal/workflow"
)

// Exit codes frozen by Spec 19 (docs/headless.md).
const (
	ExitOK          = 0
	ExitFailed      = 1 // run failed, or load/validate error before Start
	ExitUsage       = 2 // flag/arity errors (never started)
	ExitGate        = 3 // unexpected human gate / merge-without-policy
	ExitTimeout     = 4
	ExitInterrupted = 130 // SIGINT after Cancel+settle; SIGTERM mapped by CLI to 143
)

// OutputMode selects stdout formatting.
type OutputMode string

const (
	OutputText  OutputMode = "text"
	OutputJSON  OutputMode = "json"
	OutputJSONL OutputMode = "jsonl"
)

// Options configures a headless run. Manager must be provided (shared wiring
// lives in cmd/jig); either Workflow or WorkflowPath is required.
type Options struct {
	WorkflowPath string
	Workflow     *workflow.Workflow
	Manager      *engine.Manager

	Root    string // informational; Manager already carries the root
	Output  OutputMode
	Quiet   bool
	Timeout time.Duration

	ApproveMerge bool
	DiscardMerge bool

	// OnRecovery is abort|retry|resume|skip (default abort).
	OnRecovery string
	// OnConflict is abort only in headless (abort→recovery cascade). No agent.
	OnConflict string

	// CI is true when --ci was requested; used for start-time messaging and
	// approve-conflict fallback (discard when CI/discard is set).
	CI bool

	Stdout io.Writer
	Stderr io.Writer

	// Notifications, when non-nil, receives per-run notification policy/
	// binding registrations before Start emits its first live event. The
	// concrete type is opaque here so cmd/jig owns the composition boundary.
	Notifications NotificationRegistrar

	// OnRunStart, when non-nil, is invoked immediately after Manager.Start
	// returns successfully. cmd/jig uses it to notify the telemetry exporter
	// of the run's workflow metadata (backend / transport / model per step)
	// so subsequent StepStatus events carry those labels. Optional.
	OnRunStart func(runID string, wf *workflow.Workflow)
}

// NotificationRegistrar is the seam headless uses to install workflow policy
// and prepare operator bindings for a specific run without depending on the
// notification package. cmd/jig's Runtime implements this interface.
type NotificationRegistrar interface {
	// PrepareRun binds the run's resolved workflow notification policy and
	// resolves current operator bindings for this run. It must return
	// before Manager.Start is called.
	PrepareRun(runID string, policy workflow.NotificationPolicy)
}

// Recovery / conflict policy values (CLI + Policy).
const (
	RecoveryAbort  = "abort"
	RecoveryRetry  = "retry"
	RecoveryResume = "resume"
	RecoverySkip   = "skip"
	ConflictAbort  = "abort"
)

// Result is the settled outcome of a headless run.
type Result struct {
	ExitCode int
	Envelope Envelope
	Err      error // typed gate/timeout errors; nil on clean success
}

// Envelope is the flat machine-readable result (Spec 19 D16).
type Envelope struct {
	OK           bool       `json:"ok"`
	RunID        string     `json:"run_id"`
	Workflow     string     `json:"workflow"`
	Failed       bool       `json:"failed"`
	TotalCostUSD float64    `json:"total_cost_usd"`
	TotalTokens  int        `json:"total_tokens"`
	RunDir       string     `json:"run_dir"`
	Error        *ErrorInfo `json:"error"`
}

// ErrorInfo is the typed error carried in the JSON envelope.
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	StepID  string `json:"step_id,omitempty"`
}

// GateError is returned (and Cancelled) when an unexpected human gate fires.
type GateError struct {
	Code    string
	Message string
	StepID  string
}

func (e *GateError) Error() string {
	if e.StepID != "" {
		return fmt.Sprintf("%s: %s (step %q)", e.Code, e.Message, e.StepID)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// TimeoutError marks a wall-clock expiry.
type TimeoutError struct {
	Timeout time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("timeout after %s", e.Timeout)
}

// InterruptedError marks context cancellation from a signal (not timeout).
type InterruptedError struct{}

func (e *InterruptedError) Error() string { return "interrupted" }

func isGate(err error) bool {
	var g *GateError
	return errors.As(err, &g)
}

func isTimeout(err error) bool {
	var t *TimeoutError
	return errors.As(err, &t)
}

func isInterrupted(err error) bool {
	var i *InterruptedError
	return errors.As(err, &i)
}
