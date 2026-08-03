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

// loadProfiles scans .agents/jig/profiles/ adjacent to baseDir, parses every
// *.toml file, and returns the union of all declared profiles. An absent
// directory is not an error. Returns nil if baseDir is "" (structural-only mode).
func loadProfiles(baseDir string) ([]AgentProfile, error) {
	if baseDir == "" {
		return nil, nil
	}
	dir := filepath.Join(baseDir, ".agents", "jig", "profiles")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read profiles dir: %w", err)
	}

	reservedIDs := builtinProfileIDs()
	// seen maps profile id -> filename that first declared it, for duplicate detection.
	seen := make(map[string]string)
	var profiles []AgentProfile

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read profile file %q: %w", e.Name(), err)
		}
		var pf profileFile
		md, err := toml.Decode(string(data), &pf)
		if err != nil {
			return nil, fmt.Errorf("parse profile file %q: %w", e.Name(), err)
		}
		if keys := md.Undecoded(); len(keys) > 0 {
			return nil, fmt.Errorf("profile file %q: unknown key(s): %s", e.Name(), formatKeys(keys))
		}
		for _, p := range pf.Agents {
			if err := validateProfileID(p.ID, e.Name(), seen, reservedIDs); err != nil {
				return nil, err
			}
			seen[p.ID] = e.Name()
			profiles = append(profiles, p)
		}
	}
	return profiles, nil
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
