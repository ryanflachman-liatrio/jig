package agentcfg

import (
	"fmt"
	"strings"
)

// Mode tables. Each list matches the pinned adapter's accepted values exactly;
// update them together with the adapter pins.
var (
	ClaudePermissionModes  = []string{"default", "acceptEdits", "plan", "dontAsk", "bypassPermissions"}
	CodexModes             = []string{"read-only", "agent", "agent-full-access"}
	CodexCollaborationMode = []string{"default", "plan"}
	CursorModes            = []string{"agent", "plan", "ask"}
	EffortLevels           = []string{"low", "medium", "high", "xhigh", "max"}
)

// Applied defaults for an unset mode.
const (
	DefaultClaudePermissionMode   = "default"
	DefaultCodexMode              = "agent"
	DefaultCodexCollaborationMode = "default"
	DefaultCursorMode             = "agent"
)

// AskUserQuestion is controlled only by a step's ask_user field; it is never a
// valid entry in a Claude tool list.
const AskUserQuestion = "AskUserQuestion"

// PlanReadOnlyTools are the only tools the permission second layer allows
// under Claude permission_mode = "plan". Enforcing this set is what makes a
// plan-mode step safe to treat as non-mutating.
var PlanReadOnlyTools = []string{"Read", "Grep", "Glob", "WebSearch", "WebFetch"}

// claudeTools is the set of built-in Claude tool names for the pinned Claude
// ACP adapter (SDK 0.3.232). Update it whenever the adapter pin is bumped.
// AskUserQuestion is deliberately absent; see AskUserQuestion.
var claudeTools = []string{
	"Agent", "Bash", "BashOutput", "Edit", "ExitPlanMode", "Glob", "Grep",
	"KillShell", "ListMcpResources", "MultiEdit", "NotebookEdit", "Read",
	"ReadMcpResource", "Skill", "SlashCommand", "Task", "TodoWrite",
	"WebFetch", "WebSearch", "Write",
}

// ClaudeTools returns a copy of the built-in Claude tool names.
func ClaudeTools() []string { return append([]string(nil), claudeTools...) }

// ValidClaudeTool reports whether name is a bare built-in Claude tool name.
// Permission-rule syntax such as "Bash(rm:*)" is rejected.
func ValidClaudeTool(name string) bool { return contains(claudeTools, name) }

// mutatingClaudeTools imply a Claude step edits the working tree.
var mutatingClaudeTools = []string{"Edit", "MultiEdit", "Write", "Bash", "NotebookEdit"}

// Validate checks an agent's enumerated values. Errors name the backend, the
// key, the bad value and the valid set. Tool-list rules are checked separately
// by ValidateClaudeTools because their messages name the offending list.
func Validate(a Agent) error {
	switch a := a.(type) {
	case ClaudeAgent:
		if err := checkEnum(BackendClaude, "effort", a.Effort, EffortLevels); err != nil {
			return err
		}
		if err := checkEnum(BackendClaude, "permission_mode", a.PermissionMode, ClaudePermissionModes); err != nil {
			return err
		}
		if a.MaxTurns < 0 || a.MaxThinkingTokens < 0 || a.MaxBudgetUSD < 0 {
			return fmt.Errorf("claude agent limits (max_turns, max_thinking_tokens, max_budget_usd) must be >= 0")
		}
		return ValidateClaudeTools(a)
	case CodexAgent:
		if err := checkEnum(BackendCodex, "effort", a.Effort, EffortLevels); err != nil {
			return err
		}
		if err := checkEnum(BackendCodex, "mode", a.Mode, CodexModes); err != nil {
			return err
		}
		return checkEnum(BackendCodex, "collaboration_mode", a.CollaborationMode, CodexCollaborationMode)
	case CursorAgent:
		return checkEnum(BackendCursor, "mode", a.Mode, CursorModes)
	}
	return fmt.Errorf("unknown agent type %T", a)
}

// ValidateClaudeTools rejects AskUserQuestion and any entry that is not a bare
// built-in tool name in tools or disallowed_tools.
func ValidateClaudeTools(a ClaudeAgent) error {
	for _, list := range []struct {
		key   string
		names []string
	}{{"tools", a.Tools}, {"disallowed_tools", a.DisallowedTools}} {
		for _, name := range list.names {
			if name == AskUserQuestion {
				return fmt.Errorf("claude agent %s must not list %q; set ask_user on the step instead", list.key, name)
			}
			if !ValidClaudeTool(name) {
				return fmt.Errorf("claude agent %s has unknown tool %q (want bare names: %s)", list.key, name, strings.Join(claudeTools, ", "))
			}
		}
	}
	return nil
}

// NeverPrompts reports whether the agent runs in a mode where the adapter
// never raises a permission request, which blinds the Tier-1 guard. dontAsk
// denies instead of prompting, so it is not a never-prompt mode.
func NeverPrompts(a Agent) bool {
	_, _, ok := NeverPromptSetting(a)
	return ok
}

// NeverPromptSetting returns the key and value that make the agent a
// never-prompt agent, for error messages.
func NeverPromptSetting(a Agent) (key, value string, ok bool) {
	switch a := a.(type) {
	case ClaudeAgent:
		if a.PermissionMode == "bypassPermissions" {
			return "permission_mode", a.PermissionMode, true
		}
	case CodexAgent:
		if a.Mode == "agent-full-access" {
			return "mode", a.Mode, true
		}
	}
	return "", "", false
}

// IsMutating reports whether an agent may edit the working tree, which
// defaults the step to worktree isolation.
func IsMutating(a Agent) bool {
	switch a := a.(type) {
	case ClaudeAgent:
		if a.PermissionMode == "plan" {
			return false
		}
		if a.Tools == nil {
			return true
		}
		for _, t := range a.Tools {
			if contains(mutatingClaudeTools, t) {
				return true
			}
		}
		return false
	case CodexAgent:
		return a.Mode != "read-only"
	case CursorAgent:
		return a.Mode == "" || a.Mode == "agent"
	}
	return true
}

// Posture summarizes an agent's resolved permission posture for the run
// manifest. An unset mode is reported as the applied default.
func Posture(a Agent) map[string]string {
	p := map[string]string{"backend": a.Backend()}
	switch a := a.(type) {
	case ClaudeAgent:
		p["permission_mode"] = orDefault(a.PermissionMode, DefaultClaudePermissionMode)
		if a.Tools == nil {
			p["tools"] = "*"
		} else {
			p["tools"] = strings.Join(a.Tools, ",")
		}
		if len(a.DisallowedTools) > 0 {
			p["disallowed_tools"] = strings.Join(a.DisallowedTools, ",")
		}
	case CodexAgent:
		p["mode"] = orDefault(a.Mode, DefaultCodexMode)
		p["collaboration_mode"] = orDefault(a.CollaborationMode, DefaultCodexCollaborationMode)
	case CursorAgent:
		p["mode"] = orDefault(a.Mode, DefaultCursorMode)
	}
	return p
}

func checkEnum(backend, key, value string, valid []string) error {
	if value == "" || contains(valid, value) {
		return nil
	}
	return fmt.Errorf("%s agent has invalid %s %q (want %s)", backend, key, value, strings.Join(valid, "|"))
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
