package workflow

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"jig/internal/agentcfg"
)

// AgentRef is the raw `agent` key on a step or in [defaults]: either a
// profile reference string ("@codex-reader") or an inline table. The table is
// kept undecoded until resolution, because which keys are valid depends on the
// backend, and an omitted backend is only known once `extends` is followed.
type AgentRef struct {
	Profile string
	table   map[string]any
}

// UnmarshalTOML accepts a profile reference string or an inline agent table.
func (r *AgentRef) UnmarshalTOML(data any) error {
	switch v := data.(type) {
	case string:
		if !strings.HasPrefix(v, "@") {
			return fmt.Errorf("agent %q must be a profile reference starting with '@' or an inline table", v)
		}
		r.Profile = v
		return nil
	case map[string]any:
		r.table = v
		return nil
	}
	return fmt.Errorf("agent must be a profile reference string or a table, got %T", data)
}

// agentKeys lists the keys each backend accepts, beyond the common ones.
var (
	commonAgentKeys = []string{"backend", "model", "append_system_prompt", "extends"}
	backendKeys     = map[string][]string{
		agentcfg.BackendClaude: {"effort", "tools", "disallowed_tools", "permission_mode", "max_turns", "max_budget_usd", "max_thinking_tokens", "fallback_model"},
		agentcfg.BackendCodex:  {"effort", "mode", "collaboration_mode"},
		agentcfg.BackendCursor: {"mode"},
	}
)

// agentSpec is one agent layer before extends resolution. Pointer fields
// record presence, so a child's explicit `tools = []` replaces its parent's
// list while an omitted `tools` inherits it.
type agentSpec struct {
	Backend string
	Extends string
	keys    []string // keys this layer set, for per-backend checking

	Model              *string
	AppendSystemPrompt *string
	Effort             *string
	Tools              *[]string
	DisallowedTools    *[]string
	PermissionMode     *string
	MaxTurns           *int
	MaxBudgetUSD       *float64
	MaxThinkingTokens  *int
	FallbackModel      *string
	Mode               *string
	CollaborationMode  *string
}

// parseAgentSpec decodes an agent table. skip names keys owned by the caller
// (a profile's `id`). Type errors are reported here; per-backend key checks
// wait until the backend is resolved.
func parseAgentSpec(table map[string]any, skip string) (*agentSpec, error) {
	s := &agentSpec{}
	keys := make([]string, 0, len(table))
	for k := range table {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == skip {
			continue
		}
		v := table[k]
		var err error
		switch k {
		case "backend":
			err = setString(&s.Backend, k, v)
		case "extends":
			err = setString(&s.Extends, k, v)
		case "model":
			s.Model, err = optString(k, v)
		case "append_system_prompt":
			s.AppendSystemPrompt, err = optString(k, v)
		case "effort":
			s.Effort, err = optString(k, v)
		case "permission_mode":
			s.PermissionMode, err = optString(k, v)
		case "fallback_model":
			s.FallbackModel, err = optString(k, v)
		case "mode":
			s.Mode, err = optString(k, v)
		case "collaboration_mode":
			s.CollaborationMode, err = optString(k, v)
		case "tools":
			s.Tools, err = optStrings(k, v)
		case "disallowed_tools":
			s.DisallowedTools, err = optStrings(k, v)
		case "max_turns":
			s.MaxTurns, err = optInt(k, v)
		case "max_thinking_tokens":
			s.MaxThinkingTokens, err = optInt(k, v)
		case "max_budget_usd":
			s.MaxBudgetUSD, err = optFloat(k, v)
		}
		if err != nil {
			return nil, err
		}
		s.keys = append(s.keys, k)
	}
	if s.Extends != "" && !strings.HasPrefix(s.Extends, "@") {
		return nil, fmt.Errorf("extends %q must start with '@'", s.Extends)
	}
	if s.Backend != "" && !agentcfg.ValidBackend(s.Backend) {
		return nil, fmt.Errorf("invalid backend %q (want %s)", s.Backend, strings.Join(agentcfg.Backends, "|"))
	}
	return s, nil
}

// checkKeys rejects any key the layer set that the backend does not accept,
// including keys valid only for another backend.
func (s *agentSpec) checkKeys(backend string) error {
	for _, k := range s.keys {
		if !contains(commonAgentKeys, k) && !contains(backendKeys[backend], k) {
			return fmt.Errorf("%s agent has unknown key %q", backend, k)
		}
	}
	return nil
}

// overlay returns parent with every field the child set replacing it. Lists
// replace rather than append.
func overlay(parent, child *agentSpec) *agentSpec {
	out := *parent
	out.Extends = child.Extends
	out.keys = child.keys
	if child.Backend != "" {
		out.Backend = child.Backend
	}
	pick := func(dst **string, src *string) {
		if src != nil {
			*dst = src
		}
	}
	pick(&out.Model, child.Model)
	pick(&out.AppendSystemPrompt, child.AppendSystemPrompt)
	pick(&out.Effort, child.Effort)
	pick(&out.PermissionMode, child.PermissionMode)
	pick(&out.FallbackModel, child.FallbackModel)
	pick(&out.Mode, child.Mode)
	pick(&out.CollaborationMode, child.CollaborationMode)
	if child.Tools != nil {
		out.Tools = child.Tools
	}
	if child.DisallowedTools != nil {
		out.DisallowedTools = child.DisallowedTools
	}
	if child.MaxTurns != nil {
		out.MaxTurns = child.MaxTurns
	}
	if child.MaxThinkingTokens != nil {
		out.MaxThinkingTokens = child.MaxThinkingTokens
	}
	if child.MaxBudgetUSD != nil {
		out.MaxBudgetUSD = child.MaxBudgetUSD
	}
	return &out
}

// agentProfileSpec is a union-shaped [[agent]] profile entry.
type agentProfileSpec struct {
	id   string
	file string
	spec *agentSpec
	err  error // decode error, reported only when the profile is used
}

// agentResolver resolves agent layers against the union profile index.
type agentResolver struct {
	profiles map[string]*agentProfileSpec
}

// resolveProfile resolves a profile reference through its extends chain.
func (r *agentResolver) resolveProfile(id string, chain []string) (*agentSpec, error) {
	for i, seen := range chain {
		if seen == id {
			cycle := append(append([]string(nil), chain[i:]...), id)
			return nil, fmt.Errorf("extends cycle: %s", strings.Join(cycle, " -> "))
		}
	}
	p, ok := r.profiles[id]
	if !ok {
		return nil, fmt.Errorf("unknown agent profile %q", id)
	}
	if p.err != nil {
		return nil, fmt.Errorf("profile %q (%s): %w", id, p.file, p.err)
	}
	spec, err := r.resolveSpec(p.spec, append(chain, id))
	if err != nil {
		return nil, fmt.Errorf("profile %q: %w", id, err)
	}
	return spec, nil
}

// resolveSpec follows a layer's extends chain and returns the merged layer
// with its backend settled and every layer's keys checked.
func (r *agentResolver) resolveSpec(s *agentSpec, chain []string) (*agentSpec, error) {
	merged := s
	if s.Extends != "" {
		parent, err := r.resolveProfile(s.Extends, chain)
		if err != nil {
			return nil, err
		}
		if s.Backend != "" && s.Backend != parent.Backend {
			return nil, fmt.Errorf("backend %q does not match %q from extends %q", s.Backend, parent.Backend, s.Extends)
		}
		merged = overlay(parent, s)
	} else if merged.Backend == "" {
		merged = overlay(&agentSpec{}, s)
		merged.Backend = agentcfg.BackendClaude
	}
	if err := s.checkKeys(merged.Backend); err != nil {
		return nil, err
	}
	return merged, nil
}

// resolveRef resolves a step or [defaults] agent key to a merged layer.
func (r *agentResolver) resolveRef(ref *AgentRef) (*agentSpec, error) {
	if ref.Profile != "" {
		return r.resolveProfile(ref.Profile, nil)
	}
	spec, err := parseAgentSpec(ref.table, "")
	if err != nil {
		return nil, err
	}
	return r.resolveSpec(spec, nil)
}

// concrete converts a fully merged layer into its backend's concrete type and
// validates its values.
func (s *agentSpec) concrete() (agentcfg.Agent, error) {
	common := agentcfg.Common{Model: deref(s.Model), AppendSystemPrompt: deref(s.AppendSystemPrompt)}
	var a agentcfg.Agent
	switch s.Backend {
	case agentcfg.BackendCodex:
		a = agentcfg.CodexAgent{Common: common, Effort: deref(s.Effort), Mode: deref(s.Mode), CollaborationMode: deref(s.CollaborationMode)}
	case agentcfg.BackendCursor:
		a = agentcfg.CursorAgent{Common: common, Mode: deref(s.Mode)}
	default:
		c := agentcfg.ClaudeAgent{
			Common:         common,
			Effort:         deref(s.Effort),
			PermissionMode: deref(s.PermissionMode),
			FallbackModel:  deref(s.FallbackModel),
		}
		if s.Tools != nil {
			c.Tools = append([]string{}, (*s.Tools)...)
		}
		if s.DisallowedTools != nil {
			c.DisallowedTools = append([]string(nil), (*s.DisallowedTools)...)
		}
		if s.MaxTurns != nil {
			c.MaxTurns = *s.MaxTurns
		}
		if s.MaxThinkingTokens != nil {
			c.MaxThinkingTokens = *s.MaxThinkingTokens
		}
		if s.MaxBudgetUSD != nil {
			c.MaxBudgetUSD = *s.MaxBudgetUSD
		}
		a = c
	}
	if err := agentcfg.Validate(a); err != nil {
		return nil, err
	}
	return a, nil
}

// resolveAgents resolves every agent step's agent: the step's `agent`, else
// [defaults] agent, else the implicit Claude agent. The chosen agent is used
// whole. An agent_file's frontmatter tools/model form a Claude base layer
// beneath it.
func (wf *Workflow) resolveAgents(r *agentResolver) error {
	for i := range wf.Steps {
		s := &wf.Steps[i]
		if s.Type != StepAgent {
			continue
		}
		ref := s.Agent
		where := fmt.Sprintf("step %q", s.ID)
		if ref == nil {
			ref = wf.Defaults.Agent
			where = fmt.Sprintf("step %q: [defaults] agent", s.ID)
		}
		spec := &agentSpec{Backend: agentcfg.BackendClaude}
		if ref != nil {
			var err error
			if spec, err = r.resolveRef(ref); err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
		}
		if s.AgentFile != "" {
			if spec.Backend != agentcfg.BackendClaude {
				return fmt.Errorf("step %q: agent_file requires a claude agent, got %s", s.ID, spec.Backend)
			}
			spec = overlay(s.agentFileBase(), spec)
		}
		agent, err := spec.concrete()
		if err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		s.SnapshotAgent = &AgentSnapshot{Agent: agent}
	}
	return nil
}

// agentFileBase is the Claude layer an agent_file's frontmatter contributes.
func (s *Step) agentFileBase() *agentSpec {
	base := &agentSpec{Backend: agentcfg.BackendClaude}
	if s.agentFileTools != nil {
		tools := append([]string{}, s.agentFileTools...)
		base.Tools = &tools
	}
	if s.agentFileModel != "" {
		model := s.agentFileModel
		base.Model = &model
	}
	return base
}

// ResolvedAgent returns the step's resolved agent, or nil for non-agent steps
// and steps not yet resolved.
func (s *Step) ResolvedAgent() agentcfg.Agent {
	if s.SnapshotAgent == nil {
		return nil
	}
	return s.SnapshotAgent.Agent
}

// AgentSnapshot carries a resolved agent through the run snapshot. Its JSON
// form is the concrete agent's fields plus a "backend" discriminator.
type AgentSnapshot struct {
	Agent agentcfg.Agent
}

// MarshalJSON writes {"backend": "<b>", ...concrete fields}.
func (a AgentSnapshot) MarshalJSON() ([]byte, error) {
	if a.Agent == nil {
		return []byte("null"), nil
	}
	body, err := json.Marshal(a.Agent)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	backend, _ := json.Marshal(a.Agent.Backend())
	fields["backend"] = backend
	return json.Marshal(fields)
}

// UnmarshalJSON decodes into the concrete type named by "backend". An unknown
// backend is an error, never a fallback.
func (a *AgentSnapshot) UnmarshalJSON(data []byte) error {
	var head struct {
		Backend string `json:"backend"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return err
	}
	var err error
	switch head.Backend {
	case agentcfg.BackendClaude:
		var c agentcfg.ClaudeAgent
		err = json.Unmarshal(data, &c)
		a.Agent = c
	case agentcfg.BackendCodex:
		var c agentcfg.CodexAgent
		err = json.Unmarshal(data, &c)
		a.Agent = c
	case agentcfg.BackendCursor:
		var c agentcfg.CursorAgent
		err = json.Unmarshal(data, &c)
		a.Agent = c
	default:
		return fmt.Errorf("snapshot agent has unknown backend %q", head.Backend)
	}
	return err
}

func setString(dst *string, key string, v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("agent `%s` must be a string, got %T", key, v)
	}
	*dst = s
	return nil
}

func optString(key string, v any) (*string, error) {
	var s string
	if err := setString(&s, key, v); err != nil {
		return nil, err
	}
	return &s, nil
}

func optStrings(key string, v any) (*[]string, error) {
	raw, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("agent `%s` must be an array of strings, got %T", key, v)
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		s, ok := e.(string)
		if !ok {
			return nil, fmt.Errorf("agent `%s` must be an array of strings, got element %T", key, e)
		}
		out = append(out, s)
	}
	return &out, nil
}

func optInt(key string, v any) (*int, error) {
	n, ok := v.(int64)
	if !ok {
		return nil, fmt.Errorf("agent `%s` must be an integer, got %T", key, v)
	}
	i := int(n)
	return &i, nil
}

func optFloat(key string, v any) (*float64, error) {
	switch n := v.(type) {
	case float64:
		return &n, nil
	case int64:
		f := float64(n)
		return &f, nil
	}
	return nil, fmt.Errorf("agent `%s` must be a number, got %T", key, v)
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
