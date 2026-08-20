// Package workflow defines the jig workflow schema and a loader that parses
// and validates a workflow .toml file into a fully-checked in-memory graph.
//
// The schema is documented in docs/workflow-schema.md. The design goal is a
// deterministic orchestration layer around non-deterministic agents: the graph,
// the I/O contract, the gates, and loop termination are all statically
// verifiable, so as many mistakes as possible surface at load time rather than
// mid-run.
package workflow

import (
	"fmt"
	"sort"
	"strings"
)

// StepType is the kind of work a step performs.
type StepType string

const (
	// StepAgent spins up a fresh agent context driven by a skill directory.
	StepAgent StepType = "agent"
	// StepCommand runs a deterministic shell command or script.
	StepCommand StepType = "command"
	// StepReview pauses the run for a human decision in the TUI.
	StepReview StepType = "review"
)

// FailurePolicy decides what happens when a step (or its validation) fails.
type FailurePolicy string

const (
	FailAbort    FailurePolicy = "abort"    // fail the whole run (default)
	FailRetry    FailurePolicy = "retry"    // re-run the step up to MaxRetries
	FailContinue FailurePolicy = "continue" // mark failed but keep going
)

// Isolation selects the filesystem sandbox a step runs in.
type Isolation string

const (
	// IsolationWorktree runs the step in its own git worktree so mutating
	// agents don't clobber each other and changes stay reviewable.
	IsolationWorktree Isolation = "worktree"
	IsolationNone     Isolation = "none"
)

// EffortLevel tunes how much reasoning the model spends per step. It maps
// straight onto the Claude Agent SDK's WithEffort option.
type EffortLevel string

const (
	EffortLow    EffortLevel = "low"
	EffortMedium EffortLevel = "medium"
	EffortHigh   EffortLevel = "high"
	EffortXHigh  EffortLevel = "xhigh"
	EffortMax    EffortLevel = "max"
)

// valid reports whether e is one of the known effort levels.
func (e EffortLevel) valid() bool {
	switch e {
	case EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax:
		return true
	}
	return false
}

// validPermissionMode reports whether s is one of the SDK's known permission
// modes. PermissionMode is a plain string field (not a named type like
// EffortLevel) since it is passed straight through to the SDK.
func validPermissionMode(s string) bool {
	switch s {
	case "default", "acceptEdits", "plan", "bypassPermissions":
		return true
	}
	return false
}

// Backend and transport name the agent vendor and how jig reaches it.
// Selected in TOML only (never via env). Cursor always uses ACP transport.
const (
	BackendClaude = "claude"
	BackendCursor = "cursor"

	TransportSDK = "sdk"
	TransportACP = "acp"
)

func validBackend(s string) bool {
	switch s {
	case BackendClaude, BackendCursor:
		return true
	}
	return false
}

func validTransport(s string) bool {
	switch s {
	case TransportSDK, TransportACP:
		return true
	}
	return false
}

// OutputKind is the shape of a step's typed verdict.
type OutputKind string

const (
	OutputText OutputKind = "text" // markdown content, no machine verdict (default)
	OutputBool OutputKind = "bool" // true/false verdict
	OutputEnum OutputKind = "enum" // one of a fixed set of values
)

// mutatingTools are the tools whose presence in a step's allowlist implies the
// step edits the working tree, which flips worktree isolation on by default.
var mutatingTools = map[string]bool{
	"Edit":         true,
	"MultiEdit":    true,
	"Write":        true,
	"Bash":         true,
	"NotebookEdit": true,
}

// Workflow is a parsed workflow file.
type Workflow struct {
	Meta     Meta     `toml:"workflow"`
	Defaults Defaults `toml:"defaults"`
	Steps    []Step   `toml:"step"`

	// index maps step id -> position in Steps, populated by applyDefaults so
	// validation and later execution can resolve references in O(1).
	index map[string]int
	// profileIndex maps profile id (e.g. "@interactive") -> AgentProfile,
	// populated at load time from built-ins and .agents/jig/profiles/*.toml.
	profileIndex map[string]*AgentProfile
}

// AgentProfile is a reusable bundle of agent configuration. Profiles let
// workflow authors give a step a named "personality" (tool access, model
// knobs, interaction style) without repeating the same fields on every step.
// Built-in profiles are hard-coded in profiles.go; project-local profiles
// live in .agents/jig/profiles/*.toml using [[agent]] tables.
type AgentProfile struct {
	ID                string      `toml:"id"`
	Tools             []string    `toml:"tools"`
	DisallowedTools   []string    `toml:"disallowed_tools"`
	Model             string      `toml:"model"`
	FallbackModel     string      `toml:"fallback_model"`
	Effort            EffortLevel `toml:"effort"`
	MaxTurns          int         `toml:"max_turns"`
	MaxThinkingTokens int         `toml:"max_thinking_tokens"`
	MaxBudgetUSD      float64     `toml:"max_budget_usd"`
	PermissionMode    string      `toml:"permission_mode"`
	// AskUserQuestion injects "AskUserQuestion" into the step's AllowedTools
	// so the agent can pause mid-run to collect human input via the TUI.
	AskUserQuestion    bool   `toml:"ask_user_question"`
	AppendSystemPrompt string `toml:"append_system_prompt"`
}

// Meta is the top-level [workflow] table.
type Meta struct {
	Name        string `toml:"name"`
	Version     string `toml:"version"`
	Description string `toml:"description"`
}

// SecurityConfig configures the two-tier agent security monitoring layer.
// Security is on by default when no [defaults.security] block is present; set
// enabled = false to opt out. Fleet-wide knobs (budget, concurrency, batching)
// live here; per-step overrides live in StepSecurity.
type SecurityConfig struct {
	// Enabled gates the entire security layer. nil = engine default (on).
	Enabled      *bool `toml:"enabled"`
	Tier1Enabled *bool `toml:"tier1_enabled"` // deterministic guard (default on)
	Tier2Enabled *bool `toml:"tier2_enabled"` // LLM monitor fleet (default on)

	// OutboundAllowlist is the set of hostnames the Tier-1 guard permits for
	// WebFetch and curl/wget Bash calls. Entries are validated as hostnames at
	// load time.
	OutboundAllowlist []string `toml:"outbound_allowlist"`

	// Fleet budget: Tier-2 monitor dispatch stops and degrades to Tier-1-only
	// once the accumulated monitor cost reaches this ceiling. 0 = no ceiling.
	FleetBudgetUSD float64 `toml:"fleet_budget_usd"`

	// ConcurrencyCap limits simultaneous Tier-2 monitor dispatches. 0 = use
	// the engine default. Must be >= 1 when explicitly set.
	ConcurrencyCap int `toml:"concurrency_cap"`

	// BatchSize and DebounceMs control how Tier-2 batches incoming transcript
	// entries before dispatching a monitor agent. 0 = use engine defaults.
	BatchSize  int `toml:"batch_size"`
	DebounceMs int `toml:"debounce_ms"`
}

// StepSecurity is the per-step overrideable subset of SecurityConfig. Fleet-wide
// settings (budget, concurrency, batch, debounce) apply at the run level and
// cannot be overridden per step.
type StepSecurity struct {
	Enabled      *bool `toml:"enabled"`
	Tier1Enabled *bool `toml:"tier1_enabled"`
	Tier2Enabled *bool `toml:"tier2_enabled"`
	// OutboundAllowlist adds step-local host exceptions on top of (or instead
	// of) the workflow-wide allowlist when non-empty.
	OutboundAllowlist []string `toml:"outbound_allowlist"`
}

// Defaults is the [defaults] table. Per-step fields override these.
type Defaults struct {
	Model             string      `toml:"model"`
	FallbackModel     string      `toml:"fallback_model"`
	Effort            EffortLevel `toml:"effort"`
	MaxTurns          int         `toml:"max_turns"`
	MaxThinkingTokens int         `toml:"max_thinking_tokens"`
	MaxBudgetUSD      float64     `toml:"max_budget_usd"`
	Cwd               string      `toml:"cwd"`
	PermissionMode    string      `toml:"permission_mode"`
	MaxParallel       int         `toml:"max_parallel"`
	ArtifactsDir      string      `toml:"artifacts_dir"`

	// InjectContext is the workflow-wide default for the engine-assembled
	// "Workflow context" preamble on agent steps. Parsed as *bool so "unset"
	// (nil ⇒ enabled) stays distinct from an explicit false; a per-step
	// inject_context overrides it.
	InjectContext *bool `toml:"inject_context"`

	// Backend / Transport select which agent vendor and wire protocol run
	// agent steps. Per-step values override these; see Step.Backend.
	Backend   string `toml:"backend"`
	Transport string `toml:"transport"`

	// Security is the workflow-wide security monitoring configuration. Security
	// is on by default when this block is absent.
	Security SecurityConfig `toml:"security"`
}

// Step is one node in the workflow graph. Fields are grouped by the step type
// they apply to; validate() enforces that only the right fields are set for a
// given Type.
type Step struct {
	ID         string        `toml:"id"`
	Type       StepType      `toml:"type"`
	DependsOn  []string      `toml:"depends_on"`
	When       string        `toml:"when"`
	Output     string        `toml:"output"`
	OutputType OutputType    `toml:"output_type"`
	OnFailure  FailurePolicy `toml:"on_failure"`
	MaxRetries int           `toml:"max_retries"`

	// Agent-only.
	Skill     string    `toml:"skill"`
	AgentFile string    `toml:"agent_file"` // xor with Skill: a Claude agent .md file
	Profile   string    `toml:"profile"`    // "@id" references a named AgentProfile
	Inputs    []Input   `toml:"inputs"`
	Isolation Isolation `toml:"isolation"`

	// InjectContext toggles the engine-assembled "Workflow context" preamble
	// for this agent step. Parsed as *bool so an unset value (inherit from
	// [defaults]) is distinguishable from an explicit false; the effective value
	// (defaulting to true) is resolved at load time into injectContext. The raw
	// pointer is deliberately preserved so the validator can reject an explicit
	// inject_context = false alongside a [step.context] block (the block would
	// be inert) without an inherited false being mistaken for the same thing.
	InjectContext *bool `toml:"inject_context"`
	injectContext bool  // effective value resolved by applyDefaults (step > [defaults] > true)

	// Context is the optional [step.context] authoring block: author-supplied
	// purpose/notes that supplement — never replace — the engine's graph-derived
	// framing. Agent-only; see StepContextSpec.
	Context *StepContextSpec `toml:"context"`

	// Tool access: AllowedTools is the allowlist; DisallowedTools is the
	// complementary denylist (e.g. everything but Bash).
	AllowedTools    []string `toml:"allowed_tools"`
	DisallowedTools []string `toml:"disallowed_tools"`

	// Model & reasoning knobs (all overridable from [defaults]).
	Model             string      `toml:"model"`
	FallbackModel     string      `toml:"fallback_model"`
	Effort            EffortLevel `toml:"effort"`
	MaxTurns          int         `toml:"max_turns"`
	MaxThinkingTokens int         `toml:"max_thinking_tokens"`
	MaxBudgetUSD      float64     `toml:"max_budget_usd"`
	PermissionMode    string      `toml:"permission_mode"`

	// Backend is the agent vendor (claude today). Transport is how jig reaches
	// it: "sdk" (Claude Agent SDK) or "acp" (ACP→Claude). Inherited from
	// [defaults]; resolved to non-empty values by applyDefaults.
	Backend   string `toml:"backend"`
	Transport string `toml:"transport"`

	// OutputTemplate is a path (relative to the workflow file) to a markdown
	// template that structures the agent's text response. The engine reads it at
	// load time and appends it under the ## Output prompt section so the agent
	// fills its sections in the conversation — no agent Write tool required.
	OutputTemplate     string `toml:"output_template"`
	outputTemplateBody string // resolved body, populated at load time

	// AppendSystemPrompt is injected after the skill/agent-file prompt for
	// per-step constraints. agentPrompt holds the body of a resolved AgentFile
	// (the agent's system prompt); it is populated at load time, not decoded.
	AppendSystemPrompt string `toml:"append_system_prompt"`
	agentPrompt        string

	// Structured output (producer agents). At most one of these, and neither
	// alongside a bool/enum output_type. Schema is the TOML-native form parsed
	// straight from [step.schema]; SchemaFile points at a raw JSON Schema. Both
	// resolve into Schema (with Schema.File set for the file form) before
	// validation, so downstream field-ref checks are uniform.
	Schema     *Schema `toml:"schema"`
	SchemaFile string  `toml:"schema_file"`

	// Command-only. Exactly one of Run/Script.
	Run    string `toml:"run"`
	Script string `toml:"script"`

	// Review-only. "@stepid" (render that markdown) or "diff".
	Review      string `toml:"review"`
	MaxMessages int    `toml:"max_messages"` // review-only; 0 = engine default (10)

	// Agent-only. Condition checked against the step's own structured output
	// after it completes. While true the step stays in StatusNeedsInput and the
	// TUI surfaces a compose box; on false the step succeeds and downstream proceeds.
	BlockOn string `toml:"block_on"`

	Validate *Validate    `toml:"validate"`
	Loop     *Loop        `toml:"loop"`
	Security StepSecurity `toml:"security"`
}

// AgentPrompt returns the body of the resolved agent file for this step, or ""
// if none was set. The field is populated at load time by resolveAgentFiles.
// The getter exists so runner.AgentExecutor can access it without importing
// the workflow package's unexported fields directly.
func (s *Step) AgentPrompt() string { return s.agentPrompt }

// OutputTemplateBody returns the resolved markdown template body for this step,
// or "" if no output_template was set. Populated at load time by
// resolveOutputTemplates; the runner appends it under the ## Output section.
func (s *Step) OutputTemplateBody() string { return s.outputTemplateBody }

// InjectContextEnabled reports whether the engine should assemble and prepend
// the deterministic "Workflow context" preamble for this agent step. The
// effective value is resolved once at load time by applyDefaults (an explicit
// per-step inject_context wins, else [defaults], else the default of true), so
// the engine gets a single boolean read and never has to re-consult [defaults].
func (s *Step) InjectContextEnabled() bool { return s.injectContext }

// isMutating reports whether the step's tool allowlist implies it edits the
// working tree.
func (s *Step) isMutating() bool {
	for _, t := range s.AllowedTools {
		if mutatingTools[t] {
			return true
		}
	}
	return false
}

// Input is one entry of a step's `inputs` array. It is either a reference to a
// prior step's output (Ref, from "@stepid") or a literal file path (Path).
// RefField, from "@stepid.field.sub", selects a field out of that step's
// structured JSON output instead of the whole artifact. Inline requests that
// the content be injected into the prompt rather than just its path.
// From="user" pauses the step and collects free-form text from the human via
// the TUI; Label is the prompt shown, As is a name hint for the agent.
type Input struct {
	Ref      string
	RefField []string
	Path     string
	Inline   bool
	From     string // "user" for interactive collection
	Label    string // prompt shown in TUI (required when From="user")
	As       string // name hint passed to agent prompt (required when From="user")
}

// UnmarshalTOML accepts either a string ("@stepid", "@stepid.field", or a path)
// or an inline table ({ path = "…", inline = true }, { ref = "…", inline =
// true }, or { from = "user", label = "…", as = "…" }).
func (in *Input) UnmarshalTOML(data any) error {
	switch v := data.(type) {
	case string:
		if ref, ok := strings.CutPrefix(v, "@"); ok {
			in.Ref, in.RefField = parseRef(ref)
		} else {
			in.Path = v
		}
		return nil
	case map[string]any:
		if raw, ok := v["ref"]; ok {
			s, ok := raw.(string)
			if !ok {
				return fmt.Errorf("input `ref` must be a string, got %T", raw)
			}
			in.Ref, in.RefField = parseRef(strings.TrimPrefix(s, "@"))
		}
		if raw, ok := v["path"]; ok {
			s, ok := raw.(string)
			if !ok {
				return fmt.Errorf("input `path` must be a string, got %T", raw)
			}
			in.Path = s
		}
		if raw, ok := v["inline"]; ok {
			b, ok := raw.(bool)
			if !ok {
				return fmt.Errorf("input `inline` must be a bool, got %T", raw)
			}
			in.Inline = b
		}
		if raw, ok := v["from"]; ok {
			s, ok := raw.(string)
			if !ok {
				return fmt.Errorf("input `from` must be a string, got %T", raw)
			}
			in.From = s
		}
		if raw, ok := v["label"]; ok {
			s, ok := raw.(string)
			if !ok {
				return fmt.Errorf("input `label` must be a string, got %T", raw)
			}
			in.Label = s
		}
		if raw, ok := v["as"]; ok {
			s, ok := raw.(string)
			if !ok {
				return fmt.Errorf("input `as` must be a string, got %T", raw)
			}
			in.As = s
		}
		if in.From != "" && (in.Ref != "" || in.Path != "") {
			return fmt.Errorf("input table: `from` cannot be combined with `ref` or `path`")
		}
		if in.Ref == "" && in.Path == "" && in.From == "" {
			return fmt.Errorf("input table must set `ref`, `path`, or `from`")
		}
		if in.Ref != "" && in.Path != "" {
			return fmt.Errorf("input table sets both `ref` and `path`; pick one")
		}
		return nil
	default:
		return fmt.Errorf("input must be a string or table, got %T", data)
	}
}

// String renders the input back to its source form, for error messages.
func (in Input) String() string {
	if in.From != "" {
		return "from:" + in.From + "(" + in.As + ")"
	}
	if in.Ref != "" {
		s := "@" + in.Ref
		if len(in.RefField) > 0 {
			s += "." + strings.Join(in.RefField, ".")
		}
		return s
	}
	return in.Path
}

// parseRef splits a reference like "stepid.field.sub" into the step id and the
// field path within that step's structured output. A bare "stepid" yields a nil
// field path (the whole artifact).
func parseRef(s string) (step string, field []string) {
	head, rest, found := strings.Cut(s, ".")
	if !found {
		return s, nil
	}
	return head, strings.Split(rest, ".")
}

// OutputType is a step's `output_type`. It is either a bare kind ("text" or
// "bool") or a table declaring an enum ({ enum = ["a", "b"] }).
type OutputType struct {
	Kind OutputKind
	Enum []string
}

// UnmarshalTOML accepts a string kind or a table with an `enum` array.
func (o *OutputType) UnmarshalTOML(data any) error {
	switch v := data.(type) {
	case string:
		o.Kind = OutputKind(v)
		return nil
	case map[string]any:
		raw, ok := v["enum"]
		if !ok {
			return fmt.Errorf("output_type table must set `enum`")
		}
		arr, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("output_type `enum` must be an array, got %T", raw)
		}
		o.Kind = OutputEnum
		o.Enum = make([]string, 0, len(arr))
		for i, e := range arr {
			s, ok := e.(string)
			if !ok {
				return fmt.Errorf("output_type `enum`[%d] must be a string, got %T", i, e)
			}
			o.Enum = append(o.Enum, s)
		}
		return nil
	default:
		return fmt.Errorf("output_type must be a string or table, got %T", data)
	}
}

// allows reports whether v is a legal value for this output type.
func (o OutputType) allows(v string) bool {
	switch o.Kind {
	case OutputBool:
		return v == "true" || v == "false"
	case OutputEnum:
		for _, e := range o.Enum {
			if e == v {
				return true
			}
		}
	}
	return false
}

// FieldType is the type of one field in a producer step's output schema.
type FieldType string

const (
	FieldText   FieldType = "text"    // JSON string
	FieldNumber FieldType = "number"  // JSON number
	FieldBool   FieldType = "bool"    // JSON boolean
	FieldEnum   FieldType = "enum"    // JSON string constrained to Enum
	FieldList   FieldType = "list"    // JSON array of Elem
	FieldObject FieldType = "object"  // JSON object with Fields
	FieldAny    FieldType = "unknown" // opaque (from a JSON Schema we can't map)
)

// Field is one property of a producer step's output schema. It is the shared
// in-memory shape for both the TOML-native [step.schema] form and a parsed
// schema_file, so field-ref type checking is identical regardless of source.
type Field struct {
	Name   string
	Type   FieldType
	Enum   []string // FieldEnum
	Elem   *Field   // FieldList element type
	Fields []*Field // FieldObject properties, sorted by Name
}

// Schema is a producer step's structured output contract. Fields is the ordered
// (by name) set of top-level properties. File records the source path when the
// schema was loaded from schema_file rather than an inline [step.schema] table.
type Schema struct {
	Fields []*Field
	File   string
}

// UnmarshalTOML parses an inline [step.schema] table. Each key is a field name
// whose value is a field spec:
//
//	summary = "text"                       # scalar: text | number | bool
//	status  = { enum = ["ok", "fail"] }    # enum
//	tags    = { list = "text" }            # array of a spec
//	source  = { url = "text", n = "number" }  # nested object (any other keys)
func (sc *Schema) UnmarshalTOML(data any) error {
	m, ok := data.(map[string]any)
	if !ok {
		return fmt.Errorf("[step.schema] must be a table, got %T", data)
	}
	fields, err := parseFields(m)
	if err != nil {
		return err
	}
	sc.Fields = fields
	return nil
}

// parseFields turns a name->spec table into a name-sorted slice of Fields.
func parseFields(m map[string]any) ([]*Field, error) {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := make([]*Field, 0, len(names))
	for _, name := range names {
		f, err := parseFieldSpec(name, m[name])
		if err != nil {
			return nil, err
		}
		fields = append(fields, f)
	}
	return fields, nil
}

// parseFieldSpec parses one field's spec into a Field.
func parseFieldSpec(name string, spec any) (*Field, error) {
	switch v := spec.(type) {
	case string:
		switch FieldType(v) {
		case FieldText, FieldNumber, FieldBool:
			return &Field{Name: name, Type: FieldType(v)}, nil
		default:
			return nil, fmt.Errorf("field %q: unknown type %q (want text|number|bool, or a table)", name, v)
		}
	case map[string]any:
		switch {
		case v["enum"] != nil:
			enum, err := toStringSlice(name, v["enum"])
			if err != nil {
				return nil, err
			}
			if len(enum) == 0 {
				return nil, fmt.Errorf("field %q: enum is empty", name)
			}
			return &Field{Name: name, Type: FieldEnum, Enum: enum}, nil
		case v["list"] != nil:
			elem, err := parseFieldSpec(name+"[]", v["list"])
			if err != nil {
				return nil, err
			}
			return &Field{Name: name, Type: FieldList, Elem: elem}, nil
		default:
			// Any other keys describe a nested object.
			children, err := parseFields(v)
			if err != nil {
				return nil, err
			}
			if len(children) == 0 {
				return nil, fmt.Errorf("field %q: empty object; give it fields, an `enum`, or a `list`", name)
			}
			return &Field{Name: name, Type: FieldObject, Fields: children}, nil
		}
	default:
		return nil, fmt.Errorf("field %q: spec must be a string or table, got %T", name, spec)
	}
}

func toStringSlice(name string, raw any) ([]string, error) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("field %q: enum must be an array, got %T", name, raw)
	}
	out := make([]string, 0, len(arr))
	for i, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil, fmt.Errorf("field %q: enum[%d] must be a string, got %T", name, i, e)
		}
		out = append(out, s)
	}
	return out, nil
}

// lookup walks a dotted field path (e.g. ["source", "url"]) and returns the
// Field it names, descending through object fields. It does not index into
// lists.
func (sc *Schema) lookup(path []string) (*Field, bool) {
	if sc == nil || len(path) == 0 {
		return nil, false
	}
	fields := sc.Fields
	var cur *Field
	for _, seg := range path {
		cur = nil
		for _, f := range fields {
			if f.Name == seg {
				cur = f
				break
			}
		}
		if cur == nil {
			return nil, false
		}
		fields = cur.Fields // only non-nil for objects; deeper paths then miss
	}
	return cur, true
}

// StepContextSpec is an optional [step.context] table on an agent step. It is
// author-supplied context that *supplements, never replaces*, the graph-derived
// framing the engine assembles into the step-context preamble: Purpose says why
// the step exists (rendered on the step's own preamble and propagated onto a
// consumer's neighbor line), Notes is local free-form guidance for the step.
// Both are optional; an absent or empty block changes nothing.
type StepContextSpec struct {
	Purpose string `toml:"purpose"`
	Notes   string `toml:"notes"`
}

// Validate is a [step.validate] table: the deterministic gate a step must pass
// before its dependents run.
type Validate struct {
	Command        string `toml:"command"`
	OutputSchema   string `toml:"output_schema"`
	OutputExists   bool   `toml:"output_exists"`
	OutputContains string `toml:"output_contains"`
}

// Loop is a [step.loop] table: a bounded back-edge that re-runs Goto (and the
// subgraph between it and this step) while When holds, capped by MaxIterations
// so the workflow is guaranteed to terminate.
type Loop struct {
	When          string `toml:"when"`
	Goto          string `toml:"goto"`
	MaxIterations int    `toml:"max_iterations"`
	Feedback      string `toml:"feedback"` // "@stepid" fed into Goto's next run
}
