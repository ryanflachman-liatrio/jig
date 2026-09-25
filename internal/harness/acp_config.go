package harness

import (
	"context"
	"fmt"

	acpsdk "github.com/coder/acp-go-sdk"

	"jig/harness/acp"
	"jig/internal/agentcfg"
)

// acpSessionConfigPolicy lets an ACP backend opt into only the session
// configuration it has verified. It has two hooks: SessionMeta builds the
// adapter-specific _meta sent on session/new and session/load, and Apply sets
// config options after the session opens. New ACP harnesses can reuse the
// semantic categories without relying on Codex's option IDs.
type acpSessionConfigPolicy interface {
	SessionMeta(SessionSpec) (map[string]any, error)
	Apply(context.Context, *acp.Conn, string, SessionSpec) error
}

// sessionOptions assembles the session/new and session/load options shared by
// every ACP harness: the policy's _meta plus any MCP servers.
func sessionOptions(policy acpSessionConfigPolicy, spec SessionSpec) ([]acp.SessionOption, error) {
	meta, err := policy.SessionMeta(spec)
	if err != nil {
		return nil, err
	}
	return []acp.SessionOption{acp.WithMeta(meta), acp.WithMcpServers(toACPMcpServers(spec.MCPServers)...)}, nil
}

type semanticACPConfigPolicy struct {
	model  bool
	effort bool
	// adapterResolvesModel defers model-value validation to the adapter, for
	// adapters that map full model IDs (claude-haiku-4-5-20251001) onto
	// their alias options (haiku). An unresolvable model still fails closed
	// with the adapter's error.
	adapterResolvesModel bool
}

// SessionMeta sends no _meta: the semantic selectors are all applied after
// the session opens.
func (semanticACPConfigPolicy) SessionMeta(SessionSpec) (map[string]any, error) { return nil, nil }

func (p semanticACPConfigPolicy) Apply(ctx context.Context, conn *acp.Conn, sessionID string, spec SessionSpec) error {
	if p.model {
		set := conn.SetSelectConfigByCategory
		if p.adapterResolvesModel {
			set = conn.SetSelectConfigByCategoryAdapterValidated
		}
		if err := set(ctx, sessionID, acpsdk.SessionConfigOptionCategoryModel, spec.Model); err != nil {
			return err
		}
	}
	if p.effort {
		if err := conn.SetSelectConfigByCategory(ctx, sessionID, acpsdk.SessionConfigOptionCategoryThoughtLevel, spec.Effort()); err != nil {
			return err
		}
	}
	return nil
}

// claudeACPConfigPolicy enforces a ClaudeAgent through the Claude ACP adapter.
// Tool lists, limits and settingSources travel in _meta.claudeCode.options,
// which the adapter spreads over its SDK defaults. The permission mode cannot
// go there (the adapter overwrites _meta permissionMode), so Apply sets it
// through the mode config option, after the model: a model switch that
// invalidates the mode resets it to default.
type claudeACPConfigPolicy struct {
	semantic semanticACPConfigPolicy
}

func (claudeACPConfigPolicy) SessionMeta(spec SessionSpec) (map[string]any, error) {
	agent, err := claudeAgentOf(spec)
	if err != nil {
		return nil, err
	}
	// settingSources drops the operator's user and local settings, whose
	// permissions.allow rules would auto-approve calls the Tier-1 guard never
	// sees. The repository's .claude/settings.json and CLAUDE.md still load.
	options := map[string]any{"settingSources": []string{"project"}}
	if agent.Tools != nil {
		// SDK tools restricts the built-in set, so it would also remove
		// AskUserQuestion; ask_user alone decides whether the model sees it.
		tools := append([]string{}, agent.Tools...)
		if spec.AskUser {
			tools = append(tools, agentcfg.AskUserQuestion)
		}
		options["tools"] = tools
	}
	disallowed := append([]string(nil), agent.DisallowedTools...)
	if !spec.AskUser {
		disallowed = append(disallowed, agentcfg.AskUserQuestion)
	}
	if len(disallowed) > 0 {
		options["disallowedTools"] = disallowed
	}
	// The adapter applies these limits to the whole session, not per prompt,
	// and rejects session/prompt once one is reached.
	if agent.MaxTurns > 0 {
		options["maxTurns"] = agent.MaxTurns
	}
	if agent.MaxBudgetUSD > 0 {
		options["maxBudgetUsd"] = agent.MaxBudgetUSD
	}
	if agent.MaxThinkingTokens > 0 {
		options["maxThinkingTokens"] = agent.MaxThinkingTokens
	}
	if agent.FallbackModel != "" {
		options["fallbackModel"] = agent.FallbackModel
	}
	return map[string]any{"claudeCode": map[string]any{"options": options}}, nil
}

func (p claudeACPConfigPolicy) Apply(ctx context.Context, conn *acp.Conn, sessionID string, spec SessionSpec) error {
	agent, err := claudeAgentOf(spec)
	if err != nil {
		return err
	}
	if err := p.semantic.Apply(ctx, conn, sessionID, spec); err != nil {
		return err
	}
	// An unset mode is applied explicitly so the operator's settings.json
	// defaultMode never decides the step's posture.
	mode := agent.PermissionMode
	if mode == "" {
		mode = agentcfg.DefaultClaudePermissionMode
	}
	return conn.SetSelectConfigByCategory(ctx, sessionID, acpsdk.SessionConfigOptionCategoryMode, mode)
}

// claudeAgentOf returns the spec's Claude agent. A nil agent is the backend's
// defaults; any other backend's agent fails closed.
func claudeAgentOf(spec SessionSpec) (agentcfg.ClaudeAgent, error) {
	switch agent := spec.Agent.(type) {
	case nil:
		return agentcfg.ClaudeAgent{Common: agentcfg.Common{Model: spec.Model}}, nil
	case agentcfg.ClaudeAgent:
		return agent, nil
	default:
		return agentcfg.ClaudeAgent{}, wrongAgentError("acp", agent)
	}
}

func wrongAgentError(harnessName string, agent agentcfg.Agent) error {
	return fmt.Errorf("harness %s received %s agent", harnessName, agent.Backend())
}
