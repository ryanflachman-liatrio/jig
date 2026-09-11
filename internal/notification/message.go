package notification

import (
	"time"

	"jig/internal/workflow"
)

// Notification is one logical outbound notification ready for dispatch. It is
// composed by the lifecycle normalizer and consumed by the dispatcher. IDs
// remain stable across retries so a retry cannot inadvertently look like a
// fresh notification, though the receiver may still see duplicates when the
// last accepted delivery predated a retry.
type Notification struct {
	ID           string
	Event        workflow.NotificationEvent
	Timestamp    time.Time
	Workflow     string
	RunID        string
	Epoch        int
	Attention    []AttentionDescriptor
	AttentionN   int
	OmittedN     int
	IsRestored   bool
	Destinations []Binding
}

// AttentionDescriptor is one attention item in a coalesced summary. The fixed
// action vocabulary is derived from Kind so a workflow-controlled label can
// never influence the outbound text.
type AttentionDescriptor struct {
	StepID string
	Kind   AttentionKind
}

// AttentionKind classifies each unresolved human wait.
type AttentionKind string

const (
	AttentionReview              AttentionKind = "review"
	AttentionInput               AttentionKind = "input"
	AttentionPrompt              AttentionKind = "prompt"
	AttentionQuestion            AttentionKind = "question"
	AttentionRecovery            AttentionKind = "recovery"
	AttentionIntegrationConflict AttentionKind = "integration_conflict"
	AttentionFinalMerge          AttentionKind = "final_merge"
)

// Action returns the fixed operator-facing text for an attention kind. Callers
// must not derive any other language from workflow-controlled input.
func (k AttentionKind) Action() string {
	switch k {
	case AttentionReview:
		return "Review required"
	case AttentionInput:
		return "Agent input required"
	case AttentionPrompt:
		return "User prompt required"
	case AttentionQuestion:
		return "Agent question required"
	case AttentionRecovery:
		return "Recovery decision required"
	case AttentionIntegrationConflict:
		return "Integration conflict resolution required"
	case AttentionFinalMerge:
		return "Final merge decision required"
	default:
		return "Attention required"
	}
}

// WaitIdentity is the coordinate a lifecycle normalizer uses to deduplicate
// waits. StepID may be empty for run-scoped waits (final merge). Nonce
// carries the request/round identity where the engine exposes one, so a new
// review round or a sequential question is not treated as a repeated
// observation of the same wait. Generation captures the reset counter for a
// step (or the reopen epoch for run-scoped waits) so a wait created after a
// reset/reopen never coalesces with a wait from the previous generation.
type WaitIdentity struct {
	StepID     string
	Kind       AttentionKind
	Nonce      string
	Generation int
}
