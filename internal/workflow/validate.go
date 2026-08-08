package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// ValidationError aggregates every problem found in a workflow so the user sees
// all of them at once rather than fixing one and rerunning.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	if len(e.Problems) == 1 {
		return "invalid workflow: " + e.Problems[0]
	}
	return fmt.Sprintf("invalid workflow (%d problems):\n  - %s",
		len(e.Problems), strings.Join(e.Problems, "\n  - "))
}

// validator accumulates problems while walking the workflow.
type validator struct {
	wf       *Workflow
	baseDir  string // "" skips file-existence checks
	problems []string
}

func (v *validator) errf(format string, args ...any) {
	v.problems = append(v.problems, fmt.Sprintf(format, args...))
}

// validate runs every structural and referential check. baseDir roots the
// file-existence checks; "" skips them.
func (wf *Workflow) validate(baseDir string) error {
	v := &validator{wf: wf, baseDir: baseDir}

	// Resolve schema_file references first so cross-step field-ref checks see
	// every step's fully-parsed schema.
	v.resolveSchemas()
	v.checkMeta()
	v.checkSecurityConfig()
	v.checkIDs()
	for i := range wf.Steps {
		v.checkStep(&wf.Steps[i])
	}
	v.checkAcyclic()

	if len(v.problems) > 0 {
		sort.Strings(v.problems)
		return &ValidationError{Problems: v.problems}
	}
	return nil
}

func (v *validator) checkMeta() {
	if v.wf.Meta.Name == "" {
		v.errf("[workflow] name is required")
	}
	if v.wf.Meta.Version == "" {
		v.errf("[workflow] version is required")
	}
	if len(v.wf.Steps) == 0 {
		v.errf("workflow has no steps")
	}
	if v.wf.Defaults.MaxParallel < 1 {
		v.errf("[defaults] max_parallel must be >= 1, got %d", v.wf.Defaults.MaxParallel)
	}
}

// checkIDs enforces that every step has a unique, well-formed id, since ids are
// the key for every cross-reference in the file.
func (v *validator) checkIDs() {
	seen := make(map[string]bool, len(v.wf.Steps))
	for i := range v.wf.Steps {
		id := v.wf.Steps[i].ID
		switch {
		case id == "":
			v.errf("step #%d is missing an id", i+1)
		case !isIdent(id):
			v.errf("step id %q must be letters, digits, '_' or '-'", id)
		case seen[id]:
			v.errf("duplicate step id %q", id)
		}
		seen[id] = true
	}
}

func (v *validator) checkStep(s *Step) {
	// depends_on targets must exist and not be the step itself.
	for _, dep := range s.DependsOn {
		if dep == s.ID {
			v.errf("step %q depends on itself", s.ID)
		} else if _, ok := v.wf.index[dep]; !ok {
			v.errf("step %q depends_on unknown step %q", s.ID, dep)
		}
	}

	switch s.Type {
	case StepAgent:
		v.checkAgent(s)
	case StepCommand:
		v.checkCommand(s)
	case StepReview:
		v.checkReview(s)
	case "":
		v.errf("step %q is missing a type", s.ID)
	default:
		v.errf("step %q has unknown type %q (want agent|command|review)", s.ID, s.Type)
	}

	v.checkInputs(s)
	v.checkOutputType(s)
	v.checkSchema(s)
	v.checkTuning(s)
	v.checkFailure(s)
	v.checkWhen(s)
	v.checkValidate(s)
	v.checkLoop(s)
	v.checkContext(s)
	v.checkStepSecurity(s)
}

// checkTuning validates the model/reasoning knobs. These are inherited onto
// every step from [defaults] (like model), so they are checked for all step
// types rather than gated on agent.
func (v *validator) checkTuning(s *Step) {
	if s.Effort != "" && !s.Effort.valid() {
		v.errf("step %q has invalid effort %q (want low|medium|high|xhigh|max)", s.ID, s.Effort)
	}
	if s.PermissionMode != "" && !validPermissionMode(s.PermissionMode) {
		v.errf("step %q has invalid permission_mode %q (want default|acceptEdits|plan|bypassPermissions)", s.ID, s.PermissionMode)
	}
	if s.MaxThinkingTokens < 0 {
		v.errf("step %q max_thinking_tokens must be >= 0", s.ID)
	}
	if s.MaxBudgetUSD < 0 {
		v.errf("step %q max_budget_usd must be >= 0", s.ID)
	}
}

// hasAgentOnlyFields reports whether any explicitly-set, agent-only field is
// present, so command/review steps can reject them. Model/effort/etc. are
// excluded: they flow onto every step from [defaults] and are simply ignored by
// non-agent steps.
func hasAgentOnlyFields(s *Step) bool {
	return s.Skill != "" || s.AgentFile != "" || s.Profile != "" ||
		len(s.AllowedTools) > 0 || len(s.DisallowedTools) > 0 ||
		s.AppendSystemPrompt != "" || s.BlockOn != ""
}

// resolveSchemas loads each step's schema_file into its Schema field so the
// rest of validation treats inline [step.schema] tables and JSON Schema files
// identically. When baseDir is "" (structural-only mode) file schemas are left
// unresolved and their field refs are skipped, mirroring other file checks.
func (v *validator) resolveSchemas() {
	for i := range v.wf.Steps {
		s := &v.wf.Steps[i]
		if s.Schema != nil || s.SchemaFile == "" || v.baseDir == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(v.baseDir, s.SchemaFile))
		if err != nil {
			v.errf("step %q: schema_file %q not found", s.ID, s.SchemaFile)
			continue
		}
		sc, err := ParseJSONSchema(data)
		if err != nil {
			v.errf("step %q: schema_file %q is %v", s.ID, s.SchemaFile, err)
			continue
		}
		sc.File = s.SchemaFile
		s.Schema = sc
	}
}

// checkSchema enforces that a step has at most one output shape and that
// structured output is only declared on producer (agent) steps. It also
// rejects declared field names that collide with the base schema.
func (v *validator) checkSchema(s *Step) {
	hasScalar := s.OutputType.Kind == OutputBool || s.OutputType.Kind == OutputEnum
	// After resolveSchemas an inline schema has File=="" while a file-loaded one
	// has File set; if both were declared, inline wins and a leftover SchemaFile
	// betrays the conflict.
	inline := s.Schema != nil && s.Schema.File == ""
	if inline && s.SchemaFile != "" {
		v.errf("step %q sets both [step.schema] and schema_file; pick one", s.ID)
	}
	if s.Schema == nil && s.SchemaFile == "" {
		return
	}
	if s.Type != StepAgent {
		v.errf("step %q: structured output (schema/schema_file) is only valid on agent steps", s.ID)
	}
	if hasScalar {
		v.errf("step %q sets both a bool/enum output_type and a schema; a step has one output shape", s.ID)
	}
	if inline && len(s.Schema.Fields) == 0 {
		v.errf("step %q [step.schema] declares no fields", s.ID)
	}
	// Base schema fields are reserved across all agent steps; collisions are
	// caught here so the merged schema at dispatch time is always unambiguous.
	if s.Schema != nil {
		for _, f := range s.Schema.Fields {
			if BaseFieldNames[f.Name] {
				v.errf("step %q [step.schema] field %q is reserved by the base schema", s.ID, f.Name)
			}
		}
	}
}

// checkProfile validates the profile field on an agent step.
func (v *validator) checkProfile(s *Step) {
	if s.Profile == "" {
		return
	}
	if !strings.HasPrefix(s.Profile, "@") {
		v.errf("agent step %q: profile %q must start with '@'", s.ID, s.Profile)
		return
	}
	p, ok := v.wf.profileIndex[s.Profile]
	if !ok {
		v.errf("agent step %q: unknown profile %q", s.ID, s.Profile)
		return
	}
	// block_on and AskUserQuestion both pause the agent for human input but via
	// completely different engine paths; combining them on the same step is a
	// design error.
	if p.AskUserQuestion && s.BlockOn != "" {
		v.errf("agent step %q: block_on and AskUserQuestion (from profile %q) serve overlapping purposes; use one", s.ID, s.Profile)
	}
}

func (v *validator) checkAgent(s *Step) {
	v.checkProfile(s)
	// A step is driven by exactly one of a skill dir or a Claude agent file.
	switch {
	case s.Skill == "" && s.AgentFile == "":
		v.errf("agent step %q requires `skill` or `agent_file`", s.ID)
	case s.Skill != "" && s.AgentFile != "":
		v.errf("agent step %q sets both `skill` and `agent_file`; pick one", s.ID)
	case s.Skill != "" && v.baseDir != "":
		dir := filepath.Join(v.baseDir, s.Skill)
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			v.errf("agent step %q: skill dir %q not found", s.ID, s.Skill)
		} else if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
			v.errf("agent step %q: %s/SKILL.md not found", s.ID, s.Skill)
		}
	}
	// A set AgentFile has already been read and parsed by resolveAgentFiles;
	// any error there aborts the load before we get here.
	if s.Isolation != IsolationWorktree && s.Isolation != IsolationNone {
		v.errf("agent step %q has invalid isolation %q (want worktree|none)", s.ID, s.Isolation)
	}
	if s.Run != "" || s.Script != "" || s.Review != "" {
		v.errf("agent step %q sets fields belonging to another step type (run/script/review)", s.ID)
	}
	if s.MaxMessages != 0 {
		v.errf("agent step %q: max_messages is only valid on review steps", s.ID)
	}
	if s.BlockOn != "" {
		cond, err := ParseCondition(s.BlockOn)
		if err != nil {
			v.errf("agent step %q block_on: %v", s.ID, err)
		} else if cond.Step != s.ID {
			v.errf("agent step %q block_on: condition must reference this step's own output (got %q, want %q)", s.ID, cond.Step, s.ID)
		} else if len(cond.Field) > 0 {
			// Agent steps always have the base schema; checkFieldRef resolves
			// against the merged (base + declared) schema.
			v.checkFieldRef(s.ID, "block_on", s, cond.Field)
		}
	}
}

// checkContext validates the optional [step.context] block. The block is
// agent-only (shared with inject_context); a non-string purpose/notes is already
// rejected by the TOML decoder before validation, so nothing to re-check here.
// On an agent step it additionally cannot be combined with an explicit per-step
// inject_context = false — the block would be inert, so that is a contradiction.
// The contradiction reads the *explicit* per-step pointer (not the
// inherited/effective value), so a step that merely inherits false from
// [defaults] is not flagged — its block is inert but the author may be mid-edit
// (audit Open Question, resolved to explicit-false).
func (v *validator) checkContext(s *Step) {
	if s.Context == nil {
		return
	}
	if s.Type != StepAgent {
		v.errf("step %q: [step.context] is only valid on agent steps", s.ID)
		return
	}
	if s.InjectContext != nil && !*s.InjectContext {
		v.errf("agent step %q: [step.context] with inject_context = false is a contradiction (the block would be inert)", s.ID)
	}
}

func (v *validator) checkCommand(s *Step) {
	switch {
	case s.Run == "" && s.Script == "":
		v.errf("command step %q requires `run` or `script`", s.ID)
	case s.Run != "" && s.Script != "":
		v.errf("command step %q sets both `run` and `script`; pick one", s.ID)
	}
	if s.Script != "" && v.baseDir != "" {
		if _, err := os.Stat(filepath.Join(v.baseDir, s.Script)); err != nil {
			v.errf("command step %q: script %q not found", s.ID, s.Script)
		}
	}
	if s.Review != "" || hasAgentOnlyFields(s) {
		v.errf("command step %q sets fields belonging to another step type (agent skill/agent_file/tools or review)", s.ID)
	}
	if s.MaxMessages != 0 {
		v.errf("command step %q: max_messages is only valid on review steps", s.ID)
	}
	if s.InjectContext != nil {
		v.errf("step %q: inject_context is only valid on agent steps", s.ID)
	}
}

func (v *validator) checkReview(s *Step) {
	if s.Review == "" {
		v.errf("review step %q requires `review` (\"@stepid\" or \"diff\")", s.ID)
	} else if ref, ok := strings.CutPrefix(s.Review, "@"); ok {
		step, field := parseRef(ref)
		if ti, known := v.wf.index[step]; !known {
			v.errf("review step %q reviews unknown step %q", s.ID, step)
		} else if len(field) > 0 {
			v.checkFieldRef(s.ID, "review target "+s.Review, &v.wf.Steps[ti], field)
		}
	} else if s.Review != "diff" {
		v.errf("review step %q: `review` must be \"@stepid\" or \"diff\", got %q", s.ID, s.Review)
	}
	// A review exists to capture a human decision, so it must be typed.
	if s.OutputType.Kind != OutputEnum && s.OutputType.Kind != OutputBool {
		v.errf("review step %q needs an output_type (bool or enum) to record the verdict", s.ID)
	}
	if s.Run != "" || s.Script != "" || hasAgentOnlyFields(s) {
		v.errf("review step %q sets fields belonging to another step type (agent skill/agent_file/tools or run/script)", s.ID)
	}
	if s.MaxMessages < 0 {
		v.errf("review step %q: max_messages must be >= 0", s.ID)
	}
	if s.InjectContext != nil {
		v.errf("step %q: inject_context is only valid on agent steps", s.ID)
	}
}

// checkInputs enforces the "always explicit" rule: an @ref input must resolve
// to a real step AND that step must be listed in depends_on, so the data edge
// and the ordering edge never disagree. from="user" inputs are validated
// separately by checkUserInput.
func (v *validator) checkInputs(s *Step) {
	for _, in := range s.Inputs {
		if in.From != "" {
			v.checkUserInput(s, in)
			continue
		}
		if in.Ref == "" {
			continue // literal path; existence is a runtime concern
		}
		ti, ok := v.wf.index[in.Ref]
		if !ok {
			v.errf("step %q input @%s references unknown step", s.ID, in.Ref)
			continue
		}
		if !contains(s.DependsOn, in.Ref) {
			v.errf("step %q input @%s must also appear in depends_on", s.ID, in.Ref)
		}
		if len(in.RefField) > 0 {
			v.checkFieldRef(s.ID, "input "+in.String(), &v.wf.Steps[ti], in.RefField)
		}
	}
}

// checkUserInput validates a from="user" input. These are only valid on agent
// steps, require both label and as, and cannot mix with ref/path/inline.
func (v *validator) checkUserInput(s *Step, in Input) {
	if in.From != "user" {
		v.errf("step %q input: unknown from value %q (only \"user\" is supported)", s.ID, in.From)
	}
	if in.Label == "" {
		v.errf("step %q from=\"user\" input requires `label`", s.ID)
	}
	if in.As == "" {
		v.errf("step %q from=\"user\" input requires `as`", s.ID)
	}
	if s.Type != StepAgent {
		v.errf("step %q from=\"user\" input is only valid on agent steps", s.ID)
	}
}

// checkFieldRef verifies a dotted field path resolves in the target step's
// output schema, returning the named Field. For agent steps the effective
// schema is the merged base + declared schema, so base fields (summary, status,
// etc.) are always reachable without a declared [step.schema]. A target whose
// schema_file is still unresolved (baseDir == "") is skipped rather than flagged.
func (v *validator) checkFieldRef(stepID, ctx string, target *Step, path []string) (*Field, bool) {
	name := strings.Join(path, ".")
	var sc *Schema
	if target.Type == StepAgent {
		// Agent steps always have at least the base schema.
		sc = MergedSchema(target.Schema)
	} else {
		sc = target.Schema
	}
	if sc == nil {
		if target.SchemaFile == "" {
			v.errf("step %q %s references field %q but step %q declares no schema", stepID, ctx, name, target.ID)
		}
		return nil, false
	}
	f, ok := sc.lookup(path)
	if !ok {
		v.errf("step %q %s: step %q schema has no field %q", stepID, ctx, target.ID, name)
		return nil, false
	}
	return f, true
}

func (v *validator) checkOutputType(s *Step) {
	switch s.OutputType.Kind {
	case OutputText, OutputBool:
	case OutputEnum:
		if len(s.OutputType.Enum) == 0 {
			v.errf("step %q output_type enum is empty", s.ID)
		}
	default:
		v.errf("step %q has invalid output_type %q (want text|bool|enum)", s.ID, s.OutputType.Kind)
	}
}

func (v *validator) checkFailure(s *Step) {
	switch s.OnFailure {
	case FailAbort, FailRetry, FailContinue:
	default:
		v.errf("step %q has invalid on_failure %q (want abort|retry|continue)", s.ID, s.OnFailure)
	}
	if s.MaxRetries < 0 {
		v.errf("step %q max_retries must be >= 0", s.ID)
	}
}

func (v *validator) checkWhen(s *Step) {
	if s.When == "" {
		return
	}
	cond, err := ParseCondition(s.When)
	if err != nil {
		v.errf("step %q when: %v", s.ID, err)
		return
	}
	// A guard must reference a step this one waits for, else the verdict may
	// not exist yet when the guard is evaluated.
	if !contains(s.DependsOn, cond.Step) {
		v.errf("step %q when references %q, which must be in its depends_on", s.ID, cond.Step)
	}
	v.checkCondValue(s.ID, "when", cond)
}

func (v *validator) checkValidate(s *Step) {
	if s.Validate == nil {
		return
	}
	val := s.Validate
	if val.Command == "" && val.OutputSchema == "" && !val.OutputExists && val.OutputContains == "" {
		v.errf("step %q [step.validate] has no checks", s.ID)
	}
	if (val.OutputSchema != "" || val.OutputContains != "" || val.OutputExists) && s.Output == "" {
		v.errf("step %q [step.validate] checks its output but the step declares no `output`", s.ID)
	}
	if val.OutputSchema != "" && v.baseDir != "" {
		if _, err := os.Stat(filepath.Join(v.baseDir, val.OutputSchema)); err != nil {
			v.errf("step %q: output_schema %q not found", s.ID, val.OutputSchema)
		}
	}
}

// checkLoop validates a bounded back-edge: the target must exist, the cap must
// be positive (guaranteeing termination), and the guard must reference either
// this step's own verdict or one of its dependencies.
func (v *validator) checkLoop(s *Step) {
	if s.Loop == nil {
		return
	}
	l := s.Loop
	if l.Goto == "" {
		v.errf("step %q [step.loop] requires `goto`", s.ID)
	} else if _, ok := v.wf.index[l.Goto]; !ok {
		v.errf("step %q [step.loop] goto unknown step %q", s.ID, l.Goto)
	}
	if l.MaxIterations < 1 {
		v.errf("step %q [step.loop] max_iterations must be >= 1 to guarantee termination", s.ID)
	}
	if l.When == "" {
		v.errf("step %q [step.loop] requires `when`", s.ID)
	} else if cond, err := ParseCondition(l.When); err != nil {
		v.errf("step %q loop.when: %v", s.ID, err)
	} else {
		if cond.Step != s.ID && !contains(s.DependsOn, cond.Step) {
			v.errf("step %q loop.when references %q, which must be this step or in its depends_on", s.ID, cond.Step)
		}
		v.checkCondValue(s.ID, "loop.when", cond)
	}
	if ref, ok := strings.CutPrefix(l.Feedback, "@"); ok {
		if _, known := v.wf.index[ref]; !known {
			v.errf("step %q loop.feedback @%s references unknown step", s.ID, ref)
		}
	} else if l.Feedback != "" {
		v.errf("step %q loop.feedback must be \"@stepid\", got %q", s.ID, l.Feedback)
	}
}

// checkCondValue verifies that a guard's comparison is legal for whatever it
// references: a schema field (when the condition carries a field path) or the
// step's scalar output_type verdict. guard names the source ("when"/"loop.when")
// for error messages.
func (v *validator) checkCondValue(stepID, guard string, cond *Condition) {
	target, ok := v.wf.index[cond.Step]
	if !ok {
		return // unknown-step error already reported by the caller
	}
	ts := &v.wf.Steps[target]

	// A field path resolves against the referenced step's structured schema.
	if len(cond.Field) > 0 {
		if f, ok := v.checkFieldRef(stepID, guard, ts, cond.Field); ok {
			v.checkFieldCond(stepID, guard, cond, f)
		}
		return
	}

	// No field path: the scalar output_type verdict.
	ot := ts.OutputType
	switch cond.Op {
	case CondTruthy:
		if ot.Kind != OutputBool {
			v.errf("step %q %s: bare %q requires that step to have output_type = bool", stepID, guard, cond.Step)
		}
	case CondEq, CondNeq:
		if ot.Kind == OutputText {
			v.errf("step %q %s compares %q, which has no typed verdict (output_type is text)", stepID, guard, cond.Step)
		} else if !ot.allows(cond.Value) {
			v.errf("step %q %s: %q is not a valid value for step %q", stepID, guard, cond.Value, cond.Step)
		}
	}
}

// checkFieldCond verifies a comparison against a resolved schema field: enums
// must compare to a declared value, bools to true/false, and only leaf scalar
// fields may be compared at all.
func (v *validator) checkFieldCond(stepID, guard string, cond *Condition, f *Field) {
	name := strings.Join(cond.Field, ".")
	switch cond.Op {
	case CondTruthy:
		if f.Type != FieldBool {
			v.errf("step %q %s: bare field %q requires it to be type bool, got %s", stepID, guard, name, f.Type)
		}
	case CondEq, CondNeq:
		switch f.Type {
		case FieldEnum:
			if !contains(f.Enum, cond.Value) {
				v.errf("step %q %s: %q is not a valid value for field %q (enum: %s)",
					stepID, guard, cond.Value, name, strings.Join(f.Enum, ", "))
			}
		case FieldBool:
			if cond.Value != "true" && cond.Value != "false" {
				v.errf("step %q %s: field %q is bool; value must be true or false, got %q", stepID, guard, name, cond.Value)
			}
		case FieldText, FieldNumber, FieldAny:
			// Comparable against a free literal; nothing further to check.
		default:
			v.errf("step %q %s: field %q is %s and cannot be compared", stepID, guard, name, f.Type)
		}
	}
}

// checkAcyclic confirms the depends_on edges form a DAG. Loop back-edges are
// deliberately excluded: they are the only legal cycles and are bounded by
// max_iterations.
func (v *validator) checkAcyclic() {
	const (
		white = 0 // unvisited
		gray  = 1 // on the current DFS stack
		black = 2 // fully explored
	)
	color := make([]int, len(v.wf.Steps))

	var dfs func(i int) bool // returns true if a cycle is found
	dfs = func(i int) bool {
		color[i] = gray
		for _, dep := range v.wf.Steps[i].DependsOn {
			j, ok := v.wf.index[dep]
			if !ok {
				continue // unknown dep already reported
			}
			if color[j] == gray {
				v.errf("dependency cycle through steps %q and %q", v.wf.Steps[i].ID, v.wf.Steps[j].ID)
				return true
			}
			if color[j] == white && dfs(j) {
				return true
			}
		}
		color[i] = black
		return false
	}

	for i := range v.wf.Steps {
		if color[i] == white && dfs(i) {
			return // one cycle report is enough; the graph is unusable
		}
	}
}

// checkSecurityConfig validates the [defaults.security] block. Per-step
// security overrides are validated by checkStepSecurity inside checkStep.
func (v *validator) checkSecurityConfig() {
	sec := &v.wf.Defaults.Security
	if sec.FleetBudgetUSD < 0 {
		v.errf("[defaults.security] fleet_budget_usd must be >= 0, got %g", sec.FleetBudgetUSD)
	}
	if sec.ConcurrencyCap < 0 {
		v.errf("[defaults.security] concurrency_cap must be >= 1 when set, got %d", sec.ConcurrencyCap)
	}
	for i, host := range sec.OutboundAllowlist {
		if !isValidHost(host) {
			v.errf("[defaults.security] outbound_allowlist[%d] %q is not a valid hostname", i, host)
		}
	}
}

// checkStepSecurity validates a step's [step.security] override block.
func (v *validator) checkStepSecurity(s *Step) {
	for i, host := range s.Security.OutboundAllowlist {
		if !isValidHost(host) {
			v.errf("step %q [step.security] outbound_allowlist[%d] %q is not a valid hostname", s.ID, i, host)
		}
	}
}

// isValidHost reports whether s looks like a valid hostname (or host:port).
// The check is intentionally simple: non-empty, no whitespace, no scheme,
// only letters/digits/dots/hyphens/colons.
func isValidHost(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) {
			return false
		}
		if r == ':' || r == '.' || r == '-' || r == '_' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') {
			continue
		}
		// reject scheme separators and path chars
		return false
	}
	return true
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
