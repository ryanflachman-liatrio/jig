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

// Load reads, parses, and fully validates a workflow file. File-existence
// checks (skill dirs, JSON schemas, scripts) are resolved relative to the
// file's own directory.
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

		// Agent-step defaults inherited from [defaults].
		if s.Model == "" {
			s.Model = wf.Defaults.Model
		}
		if s.MaxTurns == 0 {
			s.MaxTurns = wf.Defaults.MaxTurns
		}
		if s.PermissionMode == "" {
			s.PermissionMode = wf.Defaults.PermissionMode
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

// formatKeys renders BurntSushi's dotted keys for an error message.
func formatKeys(keys []toml.Key) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k.String()
	}
	return strings.Join(parts, ", ")
}
