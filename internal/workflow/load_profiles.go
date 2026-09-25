package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// profileFile is the decoded form of a .agents/jig/profiles/*.toml file.
// Each [[agent]] table is one AgentProfile entry.
type profileFile struct {
	Agents []AgentProfile `toml:"agent"`
}

// rawProfileFile is the same file kept undecoded per entry, so each [[agent]]
// table can also be read as a single-backend agent.
type rawProfileFile struct {
	Agents []map[string]any `toml:"agent"`
}

// loadProfiles scans <project root>/.agents/jig/profiles/, parses every *.toml
// file, and returns the union of all declared profiles in both their legacy
// and single-backend agent forms. The project root is RepoRoot(baseDir),
// falling back to baseDir outside a git checkout. An absent directory is not
// an error. Returns nil if baseDir is "" (structural-only mode).
func loadProfiles(baseDir string) ([]AgentProfile, map[string]*agentProfileSpec, error) {
	if baseDir == "" {
		return nil, nil, nil
	}
	root := RepoRoot(baseDir)
	if root == "" {
		root = baseDir
	}
	dir := filepath.Join(root, ".agents", "jig", "profiles")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read profiles dir: %w", err)
	}

	reservedIDs := builtinProfileIDs()
	// seen maps profile id -> filename that first declared it, for duplicate detection.
	seen := make(map[string]string)
	var profiles []AgentProfile
	specs := make(map[string]*agentProfileSpec)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("read profile file %q: %w", e.Name(), err)
		}
		var pf profileFile
		md, err := toml.Decode(string(data), &pf)
		if err != nil {
			return nil, nil, fmt.Errorf("parse profile file %q: %w", e.Name(), err)
		}
		// A key unknown to the legacy form is still fine when it belongs to
		// the single-backend agent form; per-backend checks run on use.
		var unknown []toml.Key
		for _, k := range md.Undecoded() {
			if len(k) != 2 || !isAgentKey(k[1]) {
				unknown = append(unknown, k)
			}
		}
		if len(unknown) > 0 {
			return nil, nil, fmt.Errorf("profile file %q: unknown key(s): %s", e.Name(), formatKeys(unknown))
		}
		var raw rawProfileFile
		if _, err := toml.Decode(string(data), &raw); err != nil {
			return nil, nil, fmt.Errorf("parse profile file %q: %w", e.Name(), err)
		}
		for i, p := range pf.Agents {
			if err := validateProfileID(p.ID, e.Name(), seen, reservedIDs); err != nil {
				return nil, nil, err
			}
			seen[p.ID] = e.Name()
			profiles = append(profiles, p)
			spec, err := parseAgentSpec(raw.Agents[i], "id")
			specs[p.ID] = &agentProfileSpec{id: p.ID, file: e.Name(), spec: spec, err: err}
		}
	}
	return profiles, specs, nil
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
// only valid ident characters after the '@', is not a duplicate, and does not
// shadow a built-in.
func validateProfileID(id, filename string, seen map[string]string, reserved map[string]bool) error {
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
	if reserved[id] {
		return fmt.Errorf("profile file %q: agent id %q shadows a built-in profile; choose a different id", filename, id)
	}
	if prev, dup := seen[id]; dup {
		return fmt.Errorf("profile file %q: duplicate agent id %q (also declared in %q)", filename, id, prev)
	}
	return nil
}

// buildProfileIndex creates an id-keyed lookup from a flat profile slice.
func buildProfileIndex(profiles []AgentProfile) map[string]*AgentProfile {
	idx := make(map[string]*AgentProfile, len(profiles))
	for i := range profiles {
		idx[profiles[i].ID] = &profiles[i]
	}
	return idx
}
