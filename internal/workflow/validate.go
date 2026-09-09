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
	v.checkResourceLimits()
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
	if v.wf.Defaults.MaxReadOnly < 0 {
		v.errf("[defaults] max_read_only must be >= 0, got %d", v.wf.Defaults.MaxReadOnly)
	}
	if v.wf.Defaults.MaxMutating < 0 {
		v.errf("[defaults] max_mutating must be >= 0, got %d", v.wf.Defaults.MaxMutating)
	}
	if v.wf.Defaults.MaxCostUSD < 0 {
		v.errf("[defaults] max_cost_usd must be >= 0, got %g", v.wf.Defaults.MaxCostUSD)
	}
	if v.wf.Defaults.MaxSecurityFindings < 0 {
		v.errf("[defaults] max_security_findings must be >= 0, got %d", v.wf.Defaults.MaxSecurityFindings)
	}
	if v.wf.Defaults.MaxNetworkRequests < 0 {
		v.errf("[defaults] max_network_requests must be >= 0, got %d", v.wf.Defaults.MaxNetworkRequests)
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
		// Checked ahead of the general isIdent shape check so an id colliding
		// with the generated fan-out id marker gets the specific, actionable
		// error even though a marker (it contains '.') is also never a legal
		// identifier on its own.
		case strings.Contains(id, ForEachIDMarker):
			v.errf("step id %q must not contain reserved marker %q (used for generated fan-out child ids)", id, ForEachIDMarker)
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
	case StepCheck:
		v.checkCheck(s)
	case "":
		v.errf("step %q is missing a type", s.ID)
	default:
		v.errf("step %q has unknown type %q (want agent|command|review|check)", s.ID, s.Type)
	}

	v.checkInputs(s)
	v.checkOutputType(s)
	v.checkSchema(s)
	v.checkTuning(s)
	v.checkExecutionControls(s)
	v.checkFailure(s)
	v.checkWhen(s)
	v.checkValidate(s)
	v.checkRoutes(s)
	v.checkContext(s)
	v.checkStepSecurity(s)
	v.checkForEach(s)
}

func (v *validator) checkResourceLimits() {
	for class, limit := range v.wf.Defaults.ResourceLimits {
		if !isIdent(class) {
			v.errf("[defaults] resource_limits has invalid class %q", class)
		}
		if limit < 1 {
			v.errf("[defaults] resource_limits.%s must be >= 1, got %d", class, limit)
		}
	}
}

// checkTuning validates the model/reasoning knobs. These are inherited onto
// every step from [defaults] (like model), so they are checked for all step
// types rather than gated on agent.
func (v *validator) checkTuning(s *Step) {
	if s.ResourceClass != "" {
		if !isIdent(s.ResourceClass) {
			v.errf("step %q has invalid resource_class %q", s.ID, s.ResourceClass)
		} else if _, ok := v.wf.Defaults.ResourceLimits[s.ResourceClass]; !ok {
			v.errf("step %q resource_class %q has no [defaults] resource_limits entry", s.ID, s.ResourceClass)
		}
	}
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
	if s.Timeout.Duration < 0 {
		v.errf("step %q timeout must be greater than zero", s.ID)
	}
	if s.Backend != "" && !validBackend(s.Backend) {
		v.errf("step %q has invalid backend %q (want %s|%s|%s)", s.ID, s.Backend, BackendClaude, BackendCursor, BackendCodex)
	}
	if s.Transport != "" && !validTransport(s.Transport) {
		v.errf("step %q has invalid transport %q (want %s|%s)", s.ID, s.Transport, TransportSDK, TransportACP)
	}
	if (s.Backend == BackendCursor || s.Backend == BackendCodex) && s.Transport != TransportACP {
		v.errf("step %q backend %q requires transport %q", s.ID, s.Backend, TransportACP)
	}
}

func (v *validator) checkExecutionControls(s *Step) {
	if s.Retry != nil {
		if s.Retry.MaxAttempts < 2 {
			v.errf("step %q retry.max_attempts must be >= 2", s.ID)
		}
		if s.Idempotent == nil || !*s.Idempotent {
			v.errf("step %q automatic retries require idempotent = true", s.ID)
		}
		switch s.Retry.Backoff {
		case RetryBackoffNone, RetryBackoffFixed, RetryBackoffExponential:
		default:
			v.errf("step %q retry.backoff must be none|fixed|exponential", s.ID)
		}
		if s.Retry.Backoff != RetryBackoffNone && s.Retry.Initial.Duration <= 0 {
			v.errf("step %q retry.initial_backoff must be greater than zero when backoff is enabled", s.ID)
		}
		if len(s.Retry.RetryOn) == 0 {
			v.errf("step %q retry.retry_on must declare retryable failure classes", s.ID)
		}
		seen := map[string]bool{}
		for _, class := range s.Retry.RetryOn {
			switch class {
			case RetryTimeout, RetryTemporary, RetryExitFailure, RetryAgentError:
			default:
				v.errf("step %q retry.retry_on has unknown classification %q", s.ID, class)
			}
			if seen[class] {
				v.errf("step %q retry.retry_on repeats %q", s.ID, class)
			}
			seen[class] = true
		}
	}
	seenPaths := map[string]bool{}
	for _, path := range s.MutationPaths {
		clean := filepath.Clean(path)
		if path == "" || filepath.IsAbs(path) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			v.errf("step %q mutation_paths entry %q must be a repository-relative path or glob", s.ID, path)
		}
		if seenPaths[path] {
			v.errf("step %q mutation_paths repeats %q", s.ID, path)
		}
		seenPaths[path] = true
	}
	if len(s.MutationPaths) > 0 && s.Isolation != IsolationWorktree {
		v.errf("step %q mutation_paths requires isolation = \"worktree\"", s.ID)
	}
	seenSecrets := map[string]bool{}
	for _, name := range s.Secrets {
		if !isIdent(name) {
			v.errf("step %q secret reference %q must be a name, not a value", s.ID, name)
		}
		if seenSecrets[name] {
			v.errf("step %q secrets repeats %q", s.ID, name)
		}
		seenSecrets[name] = true
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
		dir := resolveAuthorPath(v.baseDir, s.Skill)
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			v.errf("agent step %q: skill dir %q not found", s.ID, s.Skill)
		} else if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
			v.errf("agent step %q: %s/SKILL.md not found", s.ID, s.Skill)
		}
	}
	// Prompt and output-template files are already read by their resolvers; any
	// error there aborts the load before we get here.
	if s.OutputTemplate != "" && v.baseDir != "" {
		if _, err := os.Stat(resolveAuthorPath(v.baseDir, s.OutputTemplate)); err != nil {
			v.errf("agent step %q: output_template %q not found", s.ID, s.OutputTemplate)
		}
	}
	if s.Isolation != IsolationWorktree && s.Isolation != IsolationNone {
		v.errf("agent step %q has invalid isolation %q (want worktree|none)", s.ID, s.Isolation)
	}
	if s.Run != "" || s.Script != "" || s.AppliesWhen != "" || s.Findings != nil || len(s.Review) > 0 {
		v.errf("agent step %q sets fields belonging to another step type (run/script/review)", s.ID)
	}
	if s.BlockOn != "" {
		cond, err := ParseCondition(s.BlockOn)
		if err != nil {
			v.errf("agent step %q block_on: %v", s.ID, err)
		} else if cond.Step != s.ID {
			v.errf("agent step %q block_on: condition must reference this step's own output (got %q, want %q)", s.ID, cond.Step, s.ID)
		} else if len(cond.Field) > 0 {
			// Agent steps always have the base schema; block_on evaluates the
			// step's own per-child output, never the foreach aggregate.
			v.checkOwnFieldRef(s.ID, "block_on", s, cond.Field)
		}
	}
}

func resolveAuthorPath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(baseDir, path)
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
	// Scripts are resolved relative to the project (git repo) root, not the
	// workflow file's directory — the same anchor the runner uses at execution
	// time (ScriptPath), so a script that validates is exactly the one that runs.
	// Fall back to baseDir when there is no enclosing git repo (fixtures / tests).
	if s.Script != "" && v.baseDir != "" {
		root := RepoRoot(v.baseDir)
		if root == "" {
			root = v.baseDir
		}
		if _, err := os.Stat(ScriptPath(root, s.Script)); err != nil {
			v.errf("command step %q: script %q not found (resolved from project root %q)", s.ID, s.Script, root)
		}
	}
	if s.AppliesWhen != "" || s.Findings != nil || len(s.Review) > 0 || hasAgentOnlyFields(s) {
		v.errf("command step %q sets fields belonging to another step type (agent skill/agent_file/tools or review)", s.ID)
	}
	if s.InjectContext != nil {
		v.errf("step %q: inject_context is only valid on agent steps", s.ID)
	}
}

func (v *validator) checkCheck(s *Step) {
	if s.Run == "" && s.Script == "" {
		v.errf("check step %q requires `run` or `script`", s.ID)
	}
	if s.Run != "" && s.Script != "" {
		v.errf("check step %q sets both `run` and `script`; pick one", s.ID)
	}
	if s.Isolation != IsolationWorktree && s.Isolation != IsolationNone {
		v.errf("check step %q has invalid isolation %q (want worktree|none)", s.ID, s.Isolation)
	}
	if len(s.Review) > 0 || hasAgentOnlyFields(s) || s.OutputType.Kind != OutputEnum || s.Schema != nil || s.SchemaFile != "" {
		v.errf("check step %q sets fields belonging to another step type", s.ID)
	}
	for _, outcome := range []string{"pass", "fail", "skip", "error"} {
		if !contains(s.OutputType.Enum, outcome) {
			v.errf("check step %q output_type must include %q", s.ID, outcome)
		}
	}
	if s.AppliesWhen == "" {
		v.errf("check step %q requires `applies_when`; this is the only way a check may produce skip", s.ID)
	} else {
		cond, err := ParseCondition(s.AppliesWhen)
		if err != nil {
			v.errf("check step %q applies_when: %v", s.ID, err)
		} else {
			if !contains(s.DependsOn, cond.Step) {
				v.errf("check step %q applies_when references %q, which must be in depends_on", s.ID, cond.Step)
			}
			v.checkCondValue(s.ID, "applies_when", cond)
		}
	}
	if s.Findings == nil {
		v.errf("check step %q requires a [step.findings] interface", s.ID)
	} else {
		if s.Findings.SchemaVersion != CheckFindingsSchemaVersion {
			v.errf("check step %q findings.schema_version = %d, want %d", s.ID, s.Findings.SchemaVersion, CheckFindingsSchemaVersion)
		}
		if s.Findings.File == "" {
			v.errf("check step %q findings.file is required", s.ID)
		} else if filepath.IsAbs(s.Findings.File) || filepath.Clean(s.Findings.File) == ".." || strings.HasPrefix(filepath.Clean(s.Findings.File), ".."+string(filepath.Separator)) {
			v.errf("check step %q findings.file must stay within the execution directory", s.ID)
		}
		if len(s.Findings.RequiredTools) == 0 {
			v.errf("check step %q findings.required_tools must declare every required executable", s.ID)
		}
		seenTools := map[string]bool{}
		for _, tool := range s.Findings.RequiredTools {
			if strings.TrimSpace(tool) == "" {
				v.errf("check step %q findings.required_tools contains a blank executable", s.ID)
			} else if strings.ContainsAny(tool, `/\\`) {
				v.errf("check step %q findings.required_tools entry %q must be an executable name, not a path", s.ID, tool)
			} else if seenTools[tool] {
				v.errf("check step %q findings.required_tools repeats %q", s.ID, tool)
			}
			seenTools[tool] = true
		}
		for name, path := range s.Findings.Artifacts {
			if !isIdent(name) {
				v.errf("check step %q findings.artifacts has invalid export name %q", s.ID, name)
			}
			if path == "" || filepath.IsAbs(path) || filepath.Clean(path) == ".." || strings.HasPrefix(filepath.Clean(path), ".."+string(filepath.Separator)) {
				v.errf("check step %q findings.artifacts.%s must stay within the execution directory", s.ID, name)
			}
		}
	}
	if s.InjectContext != nil {
		v.errf("step %q: inject_context is only valid on agent steps", s.ID)
	}
}

func (v *validator) checkReview(s *Step) {
	if len(s.Review) == 0 {
		v.errf("review step %q requires at least one `review` target", s.ID)
	}
	labels := make(map[string]bool, len(s.Review))
	targets := make(map[string]bool, len(s.Review))
	for i := range s.Review {
		target := &s.Review[i]
		source := strings.TrimSpace(target.Source)
		file := strings.TrimSpace(target.File)
		label := strings.TrimSpace(target.Label)
		switch {
		case source == "" && file == "":
			v.errf("review step %q target %d requires exactly one of `source` or `file`", s.ID, i+1)
		case source != "" && file != "":
			v.errf("review step %q target %d sets both `source` and `file`; pick one", s.ID, i+1)
		}
		if label == "" {
			v.errf("review step %q target %d requires a non-blank `label`", s.ID, i+1)
		} else if labels[label] {
			v.errf("review step %q has duplicate review label %q", s.ID, label)
		}
		labels[label] = true

		if source != "" && file == "" {
			v.checkReviewSourceTarget(s, target, source, targets)
		}
		if file != "" && source == "" {
			v.checkReviewFileTarget(s, file, targets)
		}
	}
	// A review exists to capture a human decision, so it must be typed.
	if s.OutputType.Kind != OutputEnum && s.OutputType.Kind != OutputBool {
		v.errf("review step %q needs an output_type (bool or enum) to record the verdict", s.ID)
	}
	if s.Run != "" || s.Script != "" || s.AppliesWhen != "" || s.Findings != nil || hasAgentOnlyFields(s) {
		v.errf("review step %q sets fields belonging to another step type (agent skill/agent_file/tools or run/script)", s.ID)
	}
	if s.InjectContext != nil {
		v.errf("step %q: inject_context is only valid on agent steps", s.ID)
	}
}

// checkReviewSourceTarget preserves the existing source target semantics:
// sources may render a diff, an upstream value, or a static workflow file.
func (v *validator) checkReviewSourceTarget(s *Step, target *ReviewTarget, source string, targets map[string]bool) {
	resolved := source
	if target.ResolvedPath() != "" {
		resolved = target.ResolvedPath()
	}
	v.checkDuplicateReviewTarget(s, ReviewTargetSource, resolved, source, targets)
	if source == "diff" {
		return
	}
	if ref, ok := strings.CutPrefix(source, "@"); ok {
		stepID, field := parseRef(ref)
		ti, known := v.wf.index[stepID]
		if !known {
			v.errf("review step %q reviews unknown step %q", s.ID, stepID)
			return
		}
		if !contains(s.DependsOn, stepID) {
			v.errf("review step %q target %q must also list %q in depends_on", s.ID, source, stepID)
		}
		if len(field) > 0 {
			f, ok := v.checkFieldRef(s.ID, "review target "+source, &v.wf.Steps[ti], field)
			if ok && f.Type != FieldText {
				v.errf("review step %q target %q must reference a text field, got %q", s.ID, source, f.Type)
			}
		}
		return
	}
	if v.baseDir != "" {
		fi, err := os.Stat(target.ResolvedPath())
		if err != nil {
			v.errf("review step %q target %q: file not found", s.ID, source)
		} else if !fi.Mode().IsRegular() {
			v.errf("review step %q target %q is not a regular file", s.ID, source)
		}
	}
}

// checkReviewFileTarget accepts only a direct dependency's text field. The
// field value becomes a runtime path after its producer has completed.
func (v *validator) checkReviewFileTarget(s *Step, file string, targets map[string]bool) {
	v.checkDuplicateReviewTarget(s, ReviewTargetFile, file, file, targets)
	ref, ok := strings.CutPrefix(file, "@")
	if !ok {
		v.errf("review step %q file target %q must be an @step.field reference", s.ID, file)
		return
	}
	stepID, field := parseRef(ref)
	if stepID == "" || len(field) == 0 || contains(field, "") {
		v.errf("review step %q file target %q must be an @step.field reference", s.ID, file)
		return
	}
	ti, known := v.wf.index[stepID]
	if !known {
		v.errf("review step %q file target %q references unknown step %q", s.ID, file, stepID)
		return
	}
	if !contains(s.DependsOn, stepID) {
		v.errf("review step %q file target %q must also list %q in depends_on", s.ID, file, stepID)
	}
	f, ok := v.checkFieldRef(s.ID, "file target "+file, &v.wf.Steps[ti], field)
	if ok && f.Type != FieldText {
		v.errf("review step %q file target %q must reference a text field, got %q", s.ID, file, f.Type)
	}
}

func (v *validator) checkDuplicateReviewTarget(s *Step, kind ReviewTargetKind, key, reference string, targets map[string]bool) {
	key = string(kind) + ":" + key
	if targets[key] {
		v.errf("review step %q has duplicate review %s %q", s.ID, kind, reference)
	}
	targets[key] = true
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
		if in.Artifact != "" {
			producer := &v.wf.Steps[ti]
			if producer.Type != StepCheck || producer.Findings == nil || producer.Findings.Artifacts[in.Artifact] == "" {
				v.errf("step %q input %s references undeclared check artifact", s.ID, in.String())
			}
			continue
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
	if in.Once && in.From != "user" {
		v.errf("step %q input once is only valid with from=\"user\"", s.ID)
	}
}

// checkFieldRef verifies a dotted field path resolves in the target step's
// reference schema (the normal effective schema for an ordinary producer, or
// the synthetic aggregate schema for a foreach family — see
// Step.ReferenceSchema), returning the named Field. Use this for every
// cross-step reference (inputs, review targets, when/route.when/applies_when).
// A target whose schema_file is still unresolved (baseDir == "") is skipped
// rather than flagged.
func (v *validator) checkFieldRef(stepID, ctx string, target *Step, path []string) (*Field, bool) {
	return v.checkFieldRefIn(stepID, ctx, target, target.ReferenceSchema(), path)
}

// checkOwnFieldRef resolves a field path against a step's own per-instance
// output schema rather than its (possibly aggregate) reference schema. Use
// this for block_on: it is evaluated once per child against that child's own
// structured output, never the family aggregate.
func (v *validator) checkOwnFieldRef(stepID, ctx string, target *Step, path []string) (*Field, bool) {
	return v.checkFieldRefIn(stepID, ctx, target, target.EffectiveSchema(), path)
}

func (v *validator) checkFieldRefIn(stepID, ctx string, target *Step, sc *Schema, path []string) (*Field, bool) {
	name := strings.Join(path, ".")
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

// checkRoutes validates bounded, explicit back-edge selection. Duplicate
// guards are rejected and a multi-branch decision must make its fallback
// explicit, so a route can never be selected by declaration accident.
func (v *validator) checkRoutes(s *Step) {
	if len(s.Routes) == 0 {
		if s.Type == StepCheck {
			v.errf("check step %q requires an automatic remediation route for fail and error", s.ID)
		}
		return
	}
	fallback := -1
	seen := map[string]bool{}
	var guarded []*Condition
	for i, r := range s.Routes {
		label := fmt.Sprintf("step %q route %d", s.ID, i+1)
		if r.Goto == "" {
			v.errf("%s requires `goto`", label)
		} else if _, ok := v.wf.index[r.Goto]; !ok {
			v.errf("%s goto unknown step %q", label, r.Goto)
		} else if !v.routeTargetPrecedes(r.Goto, s.ID) {
			v.errf("%s goto %q must be this step or an upstream remediation/review step", label, r.Goto)
		}
		if r.MaxIterations < 1 {
			v.errf("%s max_iterations must be >= 1", label)
		}
		if r.Fallback {
			if r.When != "" {
				v.errf("%s fallback cannot set `when`", label)
			}
			if fallback >= 0 {
				v.errf("step %q has more than one fallback route", s.ID)
			}
			fallback = i
			continue
		}
		if r.When == "" {
			v.errf("%s requires `when` or fallback = true", label)
			continue
		}
		cond, err := ParseCondition(r.When)
		if err != nil {
			v.errf("%s when: %v", label, err)
			continue
		}
		if cond.Step != s.ID && !contains(s.DependsOn, cond.Step) {
			v.errf("%s when references %q, which must be this step or in its depends_on", label, cond.Step)
		}
		v.checkCondValue(s.ID, "route.when", cond)
		guarded = append(guarded, cond)
		if seen[r.When] {
			v.errf("step %q has duplicate route guard %q", s.ID, r.When)
		}
		seen[r.When] = true
		if r.Feedback != "" && !strings.HasPrefix(r.Feedback, "@") {
			v.errf("%s feedback must be \"@stepid\", got %q", label, r.Feedback)
		} else if r.Feedback != "" {
			feedbackID, _ := parseRef(strings.TrimPrefix(r.Feedback, "@"))
			if _, ok := v.wf.index[feedbackID]; !ok {
				v.errf("%s feedback references unknown step %q", label, feedbackID)
			}
		}
	}
	if fallback >= 0 && fallback != len(s.Routes)-1 {
		v.errf("step %q fallback route must be last", s.ID)
	}
	if len(s.Routes) > 1 && fallback < 0 && !v.routesExhaustOutput(s, false) {
		v.errf("step %q has multiple routes but no explicit fallback or exhaustive own-output routes", s.ID)
	}
	if fallback >= 0 && v.routesExhaustOutput(s, true) {
		v.errf("step %q fallback route is unreachable because guarded routes exhaust its output", s.ID)
	}
	if len(guarded) > 1 && !routesAreMutuallyExclusive(guarded) {
		v.errf("step %q route guards are not mutually exclusive", s.ID)
	}
	if s.Type == StepCheck && !v.hasAutomaticCheckRemediation(s) {
		v.errf("check step %q requires an automatic remediation route covering both fail and error", s.ID)
	}
}

// hasAutomaticCheckRemediation accepts either explicit fail and error guards or
// a single non-pass guard. Skip is engine-produced before route selection, so
// the latter is equivalent to fail-or-error for a dispatched check.
func (v *validator) hasAutomaticCheckRemediation(s *Step) bool {
	fail, runtimeErr := false, false
	for _, r := range s.Routes {
		if r.Fallback {
			continue
		}
		cond, err := ParseCondition(r.When)
		if err != nil || cond.Step != s.ID || len(cond.Field) != 0 {
			continue
		}
		if !v.routeTargetPrecedes(r.Goto, s.ID) {
			continue
		}
		if cond.Op == CondNeq && cond.Value == "pass" {
			return true
		}
		if cond.Op != CondEq {
			continue
		}
		switch cond.Value {
		case "fail":
			fail = true
		case "error":
			runtimeErr = true
		}
	}
	return fail && runtimeErr
}

// routesAreMutuallyExclusive accepts only equality tests over one typed value
// source with distinct values. This deliberately conservative rule avoids
// pretending that arbitrary boolean expressions are statically disjoint.
func routesAreMutuallyExclusive(guards []*Condition) bool {
	first := guards[0]
	values := map[string]bool{}
	for _, guard := range guards[1:] {
		if guard.Step != first.Step || strings.Join(guard.Field, ".") != strings.Join(first.Field, ".") {
			return false
		}
	}
	if len(guards) == 2 && first.Value == guards[1].Value && first.Op != guards[1].Op &&
		(first.Op == CondEq || first.Op == CondNeq) && (guards[1].Op == CondEq || guards[1].Op == CondNeq) {
		return true
	}
	for _, guard := range guards {
		if guard.Op != CondEq || values[guard.Value] {
			return false
		}
		values[guard.Value] = true
	}
	return true
}

// routesExhaustOutput permits omitting a fallback only when guarded routes
// partition every declared enum verdict of their owning step. Anything less
// exhaustive needs the author to say where an otherwise-unmatched result goes.
func (v *validator) routesExhaustOutput(s *Step, ignoreFallback bool) bool {
	values := s.OutputType.Enum
	if s.OutputType.Kind == OutputBool {
		values = []string{"true", "false"}
	} else if s.OutputType.Kind != OutputEnum {
		return false
	}
	seen := make(map[string]bool, len(s.Routes))
	for _, r := range s.Routes {
		if r.Fallback {
			if ignoreFallback {
				continue
			}
			return false
		}
		cond, err := ParseCondition(r.When)
		if err != nil || cond.Step != s.ID || len(cond.Field) != 0 || cond.Op != CondEq || seen[cond.Value] {
			return false
		}
		seen[cond.Value] = true
	}
	for _, value := range values {
		if !seen[value] {
			return false
		}
	}
	return true
}

func (v *validator) routeTargetPrecedes(target, source string) bool {
	if target == source {
		return true
	}
	seen := map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		if id == target {
			return true
		}
		if seen[id] {
			return false
		}
		seen[id] = true
		idx, ok := v.wf.index[id]
		if !ok {
			return false
		}
		for _, dep := range v.wf.Steps[idx].DependsOn {
			if visit(dep) {
				return true
			}
		}
		return false
	}
	return visit(source)
}

// checkCondValue verifies that a guard's comparison is legal for whatever it
// references: a schema field (when the condition carries a field path) or the
// step's scalar output_type verdict. guard names the source ("when"/"route.when")
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

	// No field path: the scalar output_type verdict. A foreach family has no
	// single scalar meaning across N children — a guard must name an aggregate
	// field such as `analyze.all_succeeded`.
	if ts.ForEach != nil {
		v.errf("step %q %s: %q is a foreach family; compare an aggregate field (e.g. %s.all_succeeded), not the bare family", stepID, guard, cond.Step, cond.Step)
		return
	}
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

// reservedForEachNames are the fan-out metadata names surfaced alongside `as`
// in the child agent prompt/command environment (position, total, instance
// id); an `as` binding may not shadow them.
var reservedForEachNames = map[string]bool{
	"index":       true,
	"total":       true,
	"instance_id": true,
	"id":          true,
}

// checkForEach validates an optional [step.foreach] block: the list source,
// item binding, hard bounds, and the exclusions (route/from="user"/fixed
// output) that keep a foreach template from doing anything that would race
// children or prompt N times before dispatch.
func (v *validator) checkForEach(s *Step) {
	fe := s.ForEach
	if fe == nil {
		return
	}
	if s.Type != StepAgent && s.Type != StepCommand {
		v.errf("step %q: [step.foreach] is only valid on agent or command steps", s.ID)
		return
	}

	// items must be an exact "@step.field" reference to a direct dependency's
	// list field -- never a bare step, a file, an artifact, or a runtime id.
	ref, isRef := strings.CutPrefix(fe.Items, "@")
	stepID, field := "", []string(nil)
	if isRef {
		stepID, field = parseRef(ref)
	}
	switch {
	case fe.Items == "":
		v.errf("step %q [step.foreach] requires `items`", s.ID)
	case !isRef || stepID == "" || len(field) != 1 || field[0] == "":
		v.errf("step %q [step.foreach] items %q must be an exact @step.field reference", s.ID, fe.Items)
	default:
		ti, ok := v.wf.index[stepID]
		if !ok {
			v.errf("step %q [step.foreach] items references unknown step %q", s.ID, stepID)
		} else if !contains(s.DependsOn, stepID) {
			v.errf("step %q [step.foreach] items producer %q must also appear in depends_on", s.ID, stepID)
		} else if f, ok := v.checkFieldRef(s.ID, "foreach items", &v.wf.Steps[ti], field); ok && f.Type != FieldList {
			v.errf("step %q [step.foreach] items %q must reference a list field, got %s", s.ID, fe.Items, f.Type)
		}
	}

	switch {
	case fe.As == "":
		v.errf("step %q [step.foreach] requires `as`", s.ID)
	case !isIdent(fe.As):
		v.errf("step %q [step.foreach] as %q must be an identifier", s.ID, fe.As)
	case reservedForEachNames[fe.As]:
		v.errf("step %q [step.foreach] as %q collides with reserved fan-out metadata", s.ID, fe.As)
	default:
		for _, in := range s.Inputs {
			if in.As == fe.As {
				v.errf("step %q [step.foreach] as %q collides with an input's `as`", s.ID, fe.As)
				break
			}
		}
	}

	if fe.MaxItems < 1 {
		v.errf("step %q [step.foreach] max_items must be >= 1, got %d", s.ID, fe.MaxItems)
	}
	if fe.MaxParallel < 0 {
		v.errf("step %q [step.foreach] max_parallel must be >= 0 (0 = inherit only global/resource limits), got %d", s.ID, fe.MaxParallel)
	}

	if len(s.Routes) > 0 {
		v.errf("step %q: a foreach template cannot declare [[step.route]]; a route may target the family from elsewhere", s.ID)
	}
	if s.Output != "" {
		v.errf("step %q: a foreach template cannot declare a fixed `output` path; parallel children would race on it", s.ID)
	}
	for _, in := range s.Inputs {
		if in.From == "user" {
			v.errf("step %q: a foreach template cannot declare a from=\"user\" input", s.ID)
			break
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
