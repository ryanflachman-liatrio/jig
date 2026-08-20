package harness

import (
	"context"

	"jig/internal/interaction"
)

// Capability is one optional behavior a Harness may advertise before Open is
// called. AgentExecutor gates SessionSpec fields and step semantics (guard,
// AskUserQuestion, resume, structured output) on these — never on a runtime
// type assertion after the fact — so a harness that cannot honor a feature
// fails closed instead of silently degrading.
type Capability uint8

const (
	// CapPermissionCallback means the harness invokes SessionSpec.Permission
	// synchronously before a tool call executes, and a Deny decision actually
	// blocks execution (not merely "the callback fired").
	CapPermissionCallback Capability = 1 << iota
	// CapUserQuestion means the harness can pause an in-flight turn and invoke
	// SessionSpec.Question for a structured question round-trip.
	CapUserQuestion
	// CapSessionResume means Open honors SessionSpec.Resume to reconnect a
	// prior conversation (block_on / Stop-Resume rely on this).
	CapSessionResume
	// CapStructuredOutput means the harness enforces SessionSpec.Schema and
	// surfaces the model's structured response.
	CapStructuredOutput
	// CapPartialStreaming means the harness emits incremental text/thinking
	// deltas as they are generated, not just the finalized block.
	CapPartialStreaming
)

// CapabilitySet is the set of capabilities a Harness advertises via
// Harness.Capabilities(), queryable before Open is ever called.
type CapabilitySet uint8

// NewCapabilitySet builds a CapabilitySet from the given capabilities.
func NewCapabilitySet(caps ...Capability) CapabilitySet {
	var s CapabilitySet
	for _, c := range caps {
		s |= CapabilitySet(c)
	}
	return s
}

// Has reports whether the set includes cap.
func (s CapabilitySet) Has(cap Capability) bool {
	return s&CapabilitySet(cap) != 0
}

// Decision is a harness-agnostic permission verdict, mirroring
// sentinel.Decision's shape (Allow + a human-readable Reason fed back to the
// agent on denial) without internal/harness importing internal/sentinel —
// AgentExecutor builds a PermissionFn by closing over *sentinel.Guard.
type Decision struct {
	Allow  bool
	Reason string
}

// PermissionFn is invoked once per tool call when SessionSpec.Permission is
// set (requires CapPermissionCallback).
type PermissionFn func(toolName string, input map[string]any) Decision

type QuestionFn func(context.Context, interaction.QuestionRequest) interaction.QuestionResponse

// SessionSpec is the jig-owned set of session options, replacing the Claude
// SDK's functional-option list so internal/harness has no SDK dependency.
// Fields below the blank line are capability-gated: a harness that receives
// one it does not advertise in Capabilities() must reject Open (defensive
// symmetry against false-capability escalation), never silently ignore it.
type SessionSpec struct {
	Prompt            string
	Model             string
	FallbackModel     string
	Effort            string
	MaxTurns          int
	MaxThinkingTokens int
	MaxBudgetUSD      float64
	PermissionMode    string
	AllowedTools      []string
	DisallowedTools   []string
	Cwd               string

	// Permission requires CapPermissionCallback.
	Permission PermissionFn
	// Question requires CapUserQuestion.
	Question QuestionFn
	// Resume (a prior session/conversation id) requires CapSessionResume.
	Resume string
	// Schema (a JSON Schema for structured output) requires CapStructuredOutput.
	Schema map[string]any
	// Partial (request incremental streaming) requires CapPartialStreaming.
	Partial bool
}
