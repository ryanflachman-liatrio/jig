// Package agentcfg defines the single-backend agent configuration union shared
// by the workflow loader and the harnesses. An Agent describes exactly one
// backend, and each concrete type carries only the settings that backend's
// adapter can enforce natively. The package is a leaf: it imports no other jig
// package, so both internal/workflow and internal/harness can depend on it.
package agentcfg

// Backend names the agent vendor jig reaches over ACP.
const (
	BackendClaude = "claude"
	BackendCodex  = "codex"
	BackendCursor = "cursor"
)

// Backends lists every valid backend in display order.
var Backends = []string{BackendClaude, BackendCodex, BackendCursor}

// ValidBackend reports whether s names a known backend.
func ValidBackend(s string) bool {
	switch s {
	case BackendClaude, BackendCodex, BackendCursor:
		return true
	}
	return false
}

// Agent is the sealed union of backend-specific agent configurations. The
// concrete types are ClaudeAgent, CodexAgent and CursorAgent.
type Agent interface {
	isAgent()
	// Backend returns the discriminator (one of the Backend* constants).
	Backend() string
	// Base returns the settings every backend accepts. (A method named Common
	// would collide with the embedded Common field.)
	Base() Common
}

// Common holds the settings every backend accepts.
type Common struct {
	Model              string `json:"model,omitempty"`
	AppendSystemPrompt string `json:"append_system_prompt,omitempty"`
}

// ClaudeAgent is an agent run by the Claude ACP adapter.
//
// Tools distinguishes absence from emptiness: nil means all built-in tools,
// while a non-nil empty slice means no built-in tools. The JSON form keeps
// that distinction (null versus []), so it survives the run snapshot.
type ClaudeAgent struct {
	Common
	Effort            string   `json:"effort,omitempty"`
	Tools             []string `json:"tools"`
	DisallowedTools   []string `json:"disallowed_tools,omitempty"`
	PermissionMode    string   `json:"permission_mode,omitempty"`
	MaxTurns          int      `json:"max_turns,omitempty"`
	MaxBudgetUSD      float64  `json:"max_budget_usd,omitempty"`
	MaxThinkingTokens int      `json:"max_thinking_tokens,omitempty"`
	FallbackModel     string   `json:"fallback_model,omitempty"`
}

// CodexAgent is an agent run by the Codex ACP adapter.
type CodexAgent struct {
	Common
	Effort            string `json:"effort,omitempty"`
	Mode              string `json:"mode,omitempty"`
	CollaborationMode string `json:"collaboration_mode,omitempty"`
}

// CursorAgent is an agent run by the Cursor adapter.
type CursorAgent struct {
	Common
	Mode string `json:"mode,omitempty"`
}

func (ClaudeAgent) isAgent() {}
func (CodexAgent) isAgent()  {}
func (CursorAgent) isAgent() {}

func (ClaudeAgent) Backend() string { return BackendClaude }
func (CodexAgent) Backend() string  { return BackendCodex }
func (CursorAgent) Backend() string { return BackendCursor }

func (a ClaudeAgent) Base() Common { return a.Common }
func (a CodexAgent) Base() Common  { return a.Common }
func (a CursorAgent) Base() Common { return a.Common }
