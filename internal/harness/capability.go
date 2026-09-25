package harness

import (
	"context"

	"jig/internal/agentcfg"
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

// ToolCall is one tool call awaiting a permission decision, normalized across
// backends. Name is the canonical tool name (Bash, Edit, WebFetch, …); Input
// holds the fields the Tier-1 rules read (command, url, file_path, content).
// InputResolved is false when a Bash call has no command or an edit has no
// path or content, so a guarded step can deny what it cannot inspect.
type ToolCall struct {
	ID            string
	Name          string
	Input         map[string]any
	InputResolved bool
}

// PermissionFn is invoked once per tool call when SessionSpec.Permission is
// set (requires CapPermissionCallback). Calls whose tool name cannot be
// resolved are denied by the harness before PermissionFn runs.
type PermissionFn func(ToolCall) Decision

type QuestionFn func(context.Context, interaction.QuestionRequest) interaction.QuestionResponse

// McpServerStdio is jig's harness-owned description of a local MCP server the
// agent process should spawn as its own child and speak MCP-over-stdio to —
// mirroring coder/acp-go-sdk's McpServerStdio wire shape without leaking that
// type into SessionSpec, so callers (helpchat) depend only on this package.
type McpServerStdio struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}

// SessionSpec is the jig-owned set of session options, replacing the Claude
// SDK's functional-option list so internal/harness has no SDK dependency.
// Fields below the blank line are capability-gated. The runner, not Open,
// enforces the gate: runner.AgentExecutor.Execute fails closed on an opted-in
// feature (block_on resume, a declared schema, AskUserQuestion) the harness
// does not advertise, and buildSessionSpec omits best-effort fields (partial
// streaming, the base schema) the harness lacks.
type SessionSpec struct {
	Prompt string
	// Model is the resolved agent's model (Agent.Base().Model), kept flat for
	// the config policy and non-step sessions.
	Model string
	// Agent is the step's resolved single-backend agent. Harnesses enforce its
	// backend-specific settings; nil means the backend's defaults.
	Agent agentcfg.Agent
	// AskUser reports whether the step allows the agent to ask the human a
	// question mid-run.
	AskUser bool
	Cwd     string
	// DiagnosticsDir is an optional run-local directory for transport diagnostics.
	// Harnesses that do not own a subprocess ignore it.
	DiagnosticsDir string

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

	// MCPServers lists local MCP servers the agent should spawn as its own
	// stdio child (ACP's McpServer wire type is out-of-process only — no
	// harness capability gate needed since every ACP agent must support the
	// stdio transport universally).
	MCPServers []McpServerStdio
}

// PromptPreviewer exposes the effective initial prompt for transports that
// inject instructions before sending it. Harnesses whose prompt is unchanged
// do not need to implement it.
type PromptPreviewer interface {
	PreviewPrompt(SessionSpec) string
}

// Effort returns the resolved agent's effort level, or "" when the backend has
// no effort setting or none was set.
func (s SessionSpec) Effort() string {
	switch a := s.Agent.(type) {
	case agentcfg.ClaudeAgent:
		return a.Effort
	case agentcfg.CodexAgent:
		return a.Effort
	}
	return ""
}
