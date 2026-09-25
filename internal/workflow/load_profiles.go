package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// rawProfileFile is a .agents/jig/profiles/*.toml file. Each [[agent]] table
// is kept undecoded so it can be read as a single-backend agent.
type rawProfileFile struct {
	Agents []map[string]any `toml:"agent"`
}

// loadProfiles scans <project root>/.agents/jig/profiles/, parses every *.toml
// file, and returns every declared profile keyed by id. The project root is RepoRoot(baseDir),
// falling back to baseDir outside a git checkout. An absent directory is not
// an error. Returns nil if baseDir is "" (structural-only mode).
func loadProfiles(baseDir string) (map[string]*agentProfileSpec, error) {
	if baseDir == "" {
		return nil, nil
	}
	root := RepoRoot(baseDir)
	if root == "" {
		root = baseDir
	}
	dir := filepath.Join(root, ".agents", "jig", "profiles")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read profiles dir: %w", err)
	}

	// seen maps profile id -> filename that first declared it, for duplicate detection.
	seen := make(map[string]string)
	specs := make(map[string]*agentProfileSpec)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read profile file %q: %w", e.Name(), err)
		}
		var raw rawProfileFile
		if _, err := toml.Decode(string(data), &raw); err != nil {
			return nil, fmt.Errorf("parse profile file %q: %w", e.Name(), err)
		}
		for _, table := range raw.Agents {
			id, _ := table["id"].(string)
			if err := validateProfileID(id, e.Name(), seen); err != nil {
				return nil, err
			}
			seen[id] = e.Name()
			spec, err := parseAgentSpec(table, "id")
			if err == nil {
				err = checkAgentKeys(spec)
			}
			if err != nil {
				return nil, fmt.Errorf("profile file %q: agent %q: %w", e.Name(), id, err)
			}
			specs[id] = &agentProfileSpec{id: id, file: e.Name(), spec: spec}
		}
	}
	return specs, nil
}

// checkAgentKeys rejects keys no backend accepts. Per-backend checks run once
// the extends chain has settled the backend.
func checkAgentKeys(s *agentSpec) error {
	for _, k := range s.keys {
		if !isAgentKey(k) {
			return fmt.Errorf("unknown key %q", k)
		}
	}
	return nil
}

// isAgentKey reports whether k is accepted by some backend's agent form.
func isAgentKey(k string) bool {
	if contains(commonAgentKeys, k) {
		return true
	}
	for _, keys := range backendKeys {
		if contains(keys, k) {
			return true
		}
	}
	return false
}

// validateProfileID checks that an id is non-empty, starts with '@', contains
// only valid ident characters after the '@', and is not a duplicate.
func validateProfileID(id, filename string, seen map[string]string) error {
	if id == "" {
		return fmt.Errorf("profile file %q: [[agent]] missing `id`", filename)
	}
	if !strings.HasPrefix(id, "@") {
		return fmt.Errorf("profile file %q: agent id %q must start with '@'", filename, id)
	}
	bare := strings.TrimPrefix(id, "@")
	if !isIdent(bare) {
		return fmt.Errorf("profile file %q: agent id %q must use letters, digits, '_' or '-' after '@'", filename, id)
	}
	if prev, dup := seen[id]; dup {
		return fmt.Errorf("profile file %q: duplicate agent id %q (also declared in %q)", filename, id, prev)
	}
	return nil
}
