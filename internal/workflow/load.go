package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	defaultMaxParallel  = 4
	defaultArtifactsDir = ".jig/artifacts"
)

// Load reads, parses, and fully validates a workflow file. Authoring-artifact
// references (skill dirs, JSON schemas, agent files) are resolved relative to
// the file's own directory; command-step `script` paths are resolved relative
// to the project (git repo) root, the same anchor the runner uses at execution
// time (see workflow.ScriptPath / RepoRoot).
func Load(path string) (*Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Decode(string(data), filepath.Dir(path))
}

// Decode parses and validates a workflow from TOML text. baseDir is the root
// for resolving file-existence checks; pass "" to skip them and run structural
// validation only (useful for tests and editor tooling).
func Decode(data, baseDir string) (*Workflow, error) {
	var wf Workflow
	md, err := toml.Decode(data, &wf)
	if err != nil {
		return nil, fmt.Errorf("parse workflow: %w", err)
	}
	// Reject unknown keys so typos (`runn`, `dependson`) fail loudly instead
	// of silently doing nothing. Custom-unmarshaled fields (Input, OutputType)
	// consume their whole subtree, so their internals never show up here.
	if keys := md.Undecoded(); len(keys) > 0 {
		return nil, fmt.Errorf("unknown key(s) in workflow: %s", formatKeys(keys))
	}

	// Resolve agent files before profiles/defaults so file-derived tools/model
	// feed into worktree-isolation and [defaults] inheritance.
	if err := wf.resolveAgentFiles(baseDir); err != nil {
		return nil, err
	}
	if err := wf.resolveOutputTemplates(baseDir); err != nil {
		return nil, err
	}

	// Load built-in and project-local profiles, then apply them. Profiles run
	// after agent_file resolution (explicit step fields and file-derived values
	// both outrank the profile) and before applyDefaults ([defaults] is weakest).
	localProfiles, err := loadProfiles(baseDir)
	if err != nil {
		return nil, err
	}
	all := append(builtinProfiles(), localProfiles...)
	wf.profileIndex = buildProfileIndex(all)
	wf.applyProfiles()

	wf.applyDefaults()
	if err := wf.validate(baseDir); err != nil {
		return nil, err
	}
	return &wf, nil
}

// applyDefaults fills in engine defaults and propagates [defaults] down to each
// step, then builds the id index. It runs before validation so the validator
// sees fully-resolved steps.
func (wf *Workflow) applyDefaults() {
	if wf.Defaults.MaxParallel == 0 {
		wf.Defaults.MaxParallel = defaultMaxParallel
	}
	if wf.Defaults.ArtifactsDir == "" {
		wf.Defaults.ArtifactsDir = defaultArtifactsDir
	}

	wf.index = make(map[string]int, len(wf.Steps))
	for i := range wf.Steps {
		s := &wf.Steps[i]
		wf.index[s.ID] = i

		if s.OnFailure == "" {
			s.OnFailure = FailAbort
		}
		if s.OnFailure == FailRetry && s.MaxRetries == 0 {
			s.MaxRetries = 1
		}
		if s.OutputType.Kind == "" {
			s.OutputType.Kind = OutputText
		}

		// Agent-step defaults inherited from [defaults]. (Model/AllowedTools may
		// already be set from a resolved agent_file, which outranks these.)
		if s.Model == "" {
			s.Model = wf.Defaults.Model
		}
		if s.FallbackModel == "" {
			s.FallbackModel = wf.Defaults.FallbackModel
		}
		if s.Effort == "" {
			s.Effort = wf.Defaults.Effort
		}
		if s.MaxTurns == 0 {
			s.MaxTurns = wf.Defaults.MaxTurns
		}
		if s.MaxThinkingTokens == 0 {
			s.MaxThinkingTokens = wf.Defaults.MaxThinkingTokens
		}
		if s.MaxBudgetUSD == 0 {
			s.MaxBudgetUSD = wf.Defaults.MaxBudgetUSD
		}
		if s.PermissionMode == "" {
			s.PermissionMode = wf.Defaults.PermissionMode
		}

		// inject_context resolves to a plain bool: an explicit per-step value
		// wins, else the [defaults] value, else true. The raw *bool is left
		// untouched (not collapsed like the fields above) so the validator can
		// still tell an explicit per-step false from an inherited one when
		// rejecting the [step.context] + inject_context = false contradiction.
		s.injectContext = true
		if s.InjectContext != nil {
			s.injectContext = *s.InjectContext
		} else if wf.Defaults.InjectContext != nil {
			s.injectContext = *wf.Defaults.InjectContext
		}

		// Security: step fields override [defaults.security] when explicitly
		// set (non-nil pointer / non-empty slice), following the same
		// zero-value precedence as model/effort.
		if s.Security.Enabled == nil && wf.Defaults.Security.Enabled != nil {
			s.Security.Enabled = wf.Defaults.Security.Enabled
		}
		if s.Security.Tier1Enabled == nil && wf.Defaults.Security.Tier1Enabled != nil {
			s.Security.Tier1Enabled = wf.Defaults.Security.Tier1Enabled
		}
		if s.Security.Tier2Enabled == nil && wf.Defaults.Security.Tier2Enabled != nil {
			s.Security.Tier2Enabled = wf.Defaults.Security.Tier2Enabled
		}
		if len(s.Security.OutboundAllowlist) == 0 && len(wf.Defaults.Security.OutboundAllowlist) > 0 {
			s.Security.OutboundAllowlist = wf.Defaults.Security.OutboundAllowlist
		}

		// Worktree isolation is defaulted on for agent steps that carry
		// mutating tools; everything else runs in place unless asked otherwise.
		if s.Type == StepAgent && s.Isolation == "" {
			if s.isMutating() {
				s.Isolation = IsolationWorktree
			} else {
				s.Isolation = IsolationNone
			}
		} else if s.Isolation == "" {
			s.Isolation = IsolationNone
		}
	}
}

// applyProfiles folds each step's referenced AgentProfile into the step's
// fields, using the same zero-value semantics as applyDefaults: profile values
// only fill in fields the step (and agent_file, if any) left unset.
//
// AskUserQuestion injection is the one exception: it is always additive,
// appending the tool even if the step already has an explicit AllowedTools
// list, so that @interactive reliably enables the tool regardless of what else
// is listed.
func (wf *Workflow) applyProfiles() {
	if wf.profileIndex == nil {
		return
	}
	for i := range wf.Steps {
		s := &wf.Steps[i]
		if s.Profile == "" {
			continue
		}
		p, ok := wf.profileIndex[s.Profile]
		if !ok {
			continue // unknown profile; validator will report the error
		}
		if len(s.AllowedTools) == 0 && len(p.Tools) > 0 {
			s.AllowedTools = p.Tools
		}
		if len(s.DisallowedTools) == 0 && len(p.DisallowedTools) > 0 {
			s.DisallowedTools = p.DisallowedTools
		}
		if s.Model == "" && p.Model != "" {
			s.Model = p.Model
		}
		if s.FallbackModel == "" && p.FallbackModel != "" {
			s.FallbackModel = p.FallbackModel
		}
		if s.Effort == "" && p.Effort != "" {
			s.Effort = p.Effort
		}
		if s.MaxTurns == 0 && p.MaxTurns != 0 {
			s.MaxTurns = p.MaxTurns
		}
		if s.MaxThinkingTokens == 0 && p.MaxThinkingTokens != 0 {
			s.MaxThinkingTokens = p.MaxThinkingTokens
		}
		if s.MaxBudgetUSD == 0 && p.MaxBudgetUSD != 0 {
			s.MaxBudgetUSD = p.MaxBudgetUSD
		}
		if s.PermissionMode == "" && p.PermissionMode != "" {
			s.PermissionMode = p.PermissionMode
		}
		if s.AppendSystemPrompt == "" && p.AppendSystemPrompt != "" {
			s.AppendSystemPrompt = p.AppendSystemPrompt
		}
		if p.AskUserQuestion {
			injectAskUserQuestion(s)
		}
	}
}

// injectAskUserQuestion appends "AskUserQuestion" to s.AllowedTools if it is
// not already present.
func injectAskUserQuestion(s *Step) {
	for _, t := range s.AllowedTools {
		if t == "AskUserQuestion" {
			return
		}
	}
	s.AllowedTools = append(s.AllowedTools, "AskUserQuestion")
}

// formatKeys renders BurntSushi's dotted keys for an error message.
func formatKeys(keys []toml.Key) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k.String()
	}
	return strings.Join(parts, ", ")
}
