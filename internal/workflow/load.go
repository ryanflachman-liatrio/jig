package workflow

import (
	"fmt"
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
// to the project (git repo) root. Load retains this provenance; Start captures
// scripts there before scheduling (see ScriptPath / RepoRoot).
func Load(path string) (*Workflow, error) {
	if err := captureValidatePath(path); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, CaptureStorage
	}
	capture := &authoringLoad{}
	data, binding, err := capture.file("", AssetWorkflow, abs, filepath.Dir(abs), abs)
	if err != nil {
		return nil, err
	}
	binding.Reference = path
	capture.sources = append(capture.sources, binding)
	return decodeWorkflowCaptured(string(data), filepath.Dir(abs), abs, nil, capture)
}

// Decode parses and validates a workflow from TOML text. baseDir is the root
// for resolving file-existence checks; pass "" to skip them and run structural
// validation only (useful for tests and editor tooling).
func Decode(data, baseDir string) (*Workflow, error) {
	return decodeWorkflow(data, baseDir, "")
}

func decodeWorkflow(data, baseDir, sourcePath string) (*Workflow, error) {
	if baseDir != "" {
		var err error
		baseDir, err = filepath.Abs(baseDir)
		if err != nil {
			return nil, CaptureStorage
		}
	}
	return decodeWorkflowCaptured(data, baseDir, sourcePath, nil, &authoringLoad{})
}

// DecodeLockedMetadata reconstructs legacy display metadata from supplied TOML
// and module records only. It never resolves files, profiles or agent sessions.
func DecodeLockedMetadata(data, baseDir, sourcePath string, sources []ModuleSource) (*Workflow, error) {
	wf, err := decodeMetadataTOML(data)
	if err != nil {
		return nil, err
	}
	locked := make(map[string]ModuleSource, len(sources))
	for _, source := range sources {
		if source.Path == "" || source.TOML == "" || source.SHA256 == "" {
			return nil, fmt.Errorf("invalid locked module source")
		}
		if !filepath.IsAbs(source.Path) || locked[source.Path].Path != "" {
			return nil, CaptureIntegrity
		}
		locked[source.Path] = source
	}
	wf.publicSteps = cloneSteps(wf.Steps)
	e := &moduleExpansion{metadataOnly: true, locked: locked, sources: map[string]ModuleSource{}}
	if err := e.expand(wf, baseDir, sourcePath, "", nil, nil, nil); err != nil {
		return nil, err
	}
	wf.moduleSources = append([]ModuleSource(nil), sources...)
	wf.lockedMetadata = true
	wf.sourcePath, wf.sourceTOML = sourcePath, data
	wf.applyDefaults()
	return wf, nil
}

func decodeMetadataTOML(data string) (*Workflow, error) {
	if len(data) > captureFileLimit {
		return nil, CaptureLimit
	}
	var wf Workflow
	if _, err := toml.Decode(data, &wf); err != nil {
		return nil, CaptureIntegrity
	}
	if len(wf.Steps) > 1024 {
		return nil, CaptureLimit
	}
	return &wf, nil
}

func decodeWorkflowCaptured(data, baseDir, sourcePath string, locked map[string]ModuleSource, capture *authoringLoad) (*Workflow, error) {
	wf, err := decodePreparedCaptured(data, baseDir, sourcePath, capture)
	if err != nil {
		return nil, err
	}
	if err := wf.resolveNotification(baseDir); err != nil {
		return nil, err
	}
	wf.resolveSandboxes("", nil, nil)
	if hasSubworkflow(wf) {
		if err := expandModules(wf, baseDir, sourcePath, locked); err != nil {
			return nil, err
		}
	}
	if wf.Module != nil && sourcePath == "" {
		return nil, fmt.Errorf("[module] is only valid in a subworkflow file")
	}
	if err := wf.validate(baseDir); err != nil {
		return nil, err
	}
	wf.sourcePath = sourcePath
	wf.sourceTOML = data
	wf.sourceBaseDir = baseDir
	return wf, nil
}

// decodePrepared loads authoring assets and defaults but deliberately defers
// validation. Module templates contain @module.<input> placeholders that only
// become ordinary graph edges once a parent has bound them.
func decodePrepared(data, baseDir string) (*Workflow, error) {
	return decodePreparedCaptured(data, baseDir, "", &authoringLoad{})
}

func decodePreparedCaptured(data, baseDir, sourcePath string, capture *authoringLoad) (*Workflow, error) {
	if len(data) > captureFileLimit {
		return nil, CaptureLimit
	}
	var wf Workflow
	md, err := toml.Decode(data, &wf)
	if err != nil {
		return nil, fmt.Errorf("parse workflow: %w", err)
	}
	if len(wf.Steps) > 1024 {
		return nil, CaptureLimit
	}
	wf.authoring = capture
	anchor := RepoRoot(baseDir)
	if anchor == "" {
		anchor = baseDir
	}
	for i := range wf.Steps {
		wf.Steps[i].scriptAnchor = anchor
		wf.Steps[i].assetAnchor = baseDir
		wf.Steps[i].declaring = sourcePath
		if v := wf.Steps[i].Validate; v != nil {
			v.outputSchemaReference = v.OutputSchema
		}
	}
	// Reject unknown keys so typos (`runn`, `dependson`) fail loudly instead
	// of silently doing nothing. Custom-unmarshaled fields (Input, OutputType)
	// consume their whole subtree, so their internals never show up here.
	if keys := md.Undecoded(); len(keys) > 0 {
		return nil, fmt.Errorf("unknown key(s) in workflow: %s", formatKeys(keys))
	}

	wf.sourcePath, wf.sourceTOML = sourcePath, data
	if err := wf.validateSandboxDeclarations(); err != nil {
		return nil, err
	}

	if wf.Module != nil && wf.Notification != nil {
		return nil, fmt.Errorf("notification policy is only valid in the root workflow")
	}

	// Resolve prompt-bearing files before profiles/defaults so file-derived
	// tools/model feed into worktree-isolation and [defaults] inheritance.
	if err := wf.resolveSkills(baseDir); err != nil {
		return nil, err
	}
	if err := wf.resolveAgentFiles(baseDir); err != nil {
		return nil, err
	}
	if err := wf.resolveOutputTemplates(baseDir); err != nil {
		return nil, err
	}
	if err := wf.resolveReviewTargets(baseDir); err != nil {
		return nil, err
	}

	// Resolve each agent step's agent after agent_file resolution (its
	// frontmatter is the Claude base layer) and before applyDefaults, which
	// derives isolation from the resolved agent.
	agentProfiles, err := loadProfiles(baseDir)
	if err != nil {
		return nil, err
	}
	if err := wf.resolveAgents(&agentResolver{profiles: agentProfiles}); err != nil {
		return nil, err
	}

	if baseDir != "" {
		for i := range wf.Steps {
			s := &wf.Steps[i]
			if s.Schema == nil && s.SchemaFile != "" {
				data, err := wf.readAsset(s, AssetSchema, s.SchemaFile, baseDir)
				if err != nil {
					return nil, err
				}
				sc, err := ParseJSONSchema(data)
				if err != nil {
					return nil, assetCaptureError(s.ID, AssetSchema, CaptureIntegrity)
				}
				sc.File = s.SchemaFile
				s.Schema = sc
			}
		}
	}
	wf.applyDefaults()
	return &wf, nil
}

func hasSubworkflow(wf *Workflow) bool {
	for _, s := range wf.Steps {
		if s.Type == StepSubworkflow {
			return true
		}
	}
	return false
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

		// Backend and Model mirror the resolved agent for downstream readers.
		if a := s.ResolvedAgent(); a != nil {
			s.Backend = a.Backend()
			s.Model = a.Base().Model
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

		// Worktree isolation is defaulted on for agent steps whose resolved
		// agent may mutate; everything else runs in place unless asked otherwise.
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

// resolveReviewTargets populates each literal source target with an absolute
// path under baseDir. File targets name runtime products and are intentionally
// left unresolved until their producer has run. Existence checks remain owned by
// validate() for static targets.
func (wf *Workflow) resolveReviewTargets(baseDir string) error {
	for i := range wf.Steps {
		for j := range wf.Steps[i].Review {
			target := &wf.Steps[i].Review[j]
			src := strings.TrimSpace(target.Source)
			target.Source = src
			target.File = strings.TrimSpace(target.File)
			if baseDir == "" || src == "" || src == "diff" || strings.HasPrefix(src, "@") {
				continue
			}
			target.resolvedPath = filepath.Join(baseDir, src)
		}
	}
	return nil
}

// formatKeys renders BurntSushi's dotted keys for an error message.
func formatKeys(keys []toml.Key) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k.String()
	}
	return strings.Join(parts, ", ")
}
