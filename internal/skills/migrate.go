// Package skills relocates jig-only skills out of the shared .agents/skills
// tree, which every coding agent auto-discovers, into .agents/jig/skills,
// which only jig reads (through explicit workflow `skill =` references).
package skills

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SharedDir and JigDir are the project-relative skill homes.
var (
	SharedDir = filepath.Join(".agents", "skills")
	JigDir    = filepath.Join(".agents", "jig", "skills")
)

// skillLine matches a TOML `skill = "..."` assignment on a single line. The
// migration rewrites references textually so comments and layout survive.
var skillLine = regexp.MustCompile(`^(\s*skill\s*=\s*)"([^"]*)"`)

// Move is one skill directory relocated from SharedDir to JigDir.
type Move struct {
	Name string
	From string
	To   string
}

// Rewrite is one `skill =` reference updated to point at the moved skill.
type Rewrite struct {
	File string
	Line int
	Old  string
	New  string
}

// Plan is the full, side-effect-free description of a migration.
type Plan struct {
	Root     string
	Moves    []Move
	Rewrites []Rewrite
	// Kept lists referenced shared skills left in place by request.
	Kept []string
}

// Options configure PlanMigration.
type Options struct {
	// Root is the project root containing .agents/.
	Root string
	// Extra lists additional workflow files or directories to scan beyond
	// .agents/jig (e.g. examples/). Their references are rewritten too.
	Extra []string
	// Keep names shared skills that must stay in .agents/skills.
	Keep []string
}

// PlanMigration scans workflow TOML files for skill references that resolve
// to a direct child of .agents/skills and plans moving each into
// .agents/jig/skills. Unreferenced shared skills are never touched: only a
// workflow reference proves a skill belongs to jig.
func PlanMigration(opts Options) (*Plan, error) {
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, err
	}
	keep := make(map[string]bool, len(opts.Keep))
	for _, name := range opts.Keep {
		keep[name] = true
	}
	shared := filepath.Join(root, SharedDir)
	jig := filepath.Join(root, JigDir)

	files, err := workflowFiles(root, opts.Extra)
	if err != nil {
		return nil, err
	}

	plan := &Plan{Root: root}
	moved := map[string]bool{}
	kept := map[string]bool{}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		for i, line := range strings.Split(string(data), "\n") {
			m := skillLine.FindStringSubmatch(line)
			if m == nil || filepath.IsAbs(m[2]) {
				continue
			}
			name, ok := sharedSkillName(shared, filepath.Join(filepath.Dir(file), filepath.FromSlash(m[2])))
			if !ok {
				continue
			}
			if keep[name] {
				kept[name] = true
				continue
			}
			to := filepath.Join(jig, name)
			if !moved[name] {
				if _, err := os.Stat(filepath.Join(shared, name, "SKILL.md")); err != nil {
					return nil, fmt.Errorf("%s:%d: skill %q has no SKILL.md in %s", rel(root, file), i+1, name, SharedDir)
				}
				if _, err := os.Stat(to); err == nil {
					return nil, fmt.Errorf("cannot move skill %q: %s already exists", name, rel(root, to))
				} else if !errors.Is(err, fs.ErrNotExist) {
					return nil, err
				}
				moved[name] = true
				plan.Moves = append(plan.Moves, Move{Name: name, From: filepath.Join(shared, name), To: to})
			}
			newRef, err := filepath.Rel(filepath.Dir(file), to)
			if err != nil {
				return nil, err
			}
			plan.Rewrites = append(plan.Rewrites, Rewrite{File: file, Line: i + 1, Old: m[2], New: filepath.ToSlash(newRef)})
		}
	}
	for name := range kept {
		if !moved[name] {
			plan.Kept = append(plan.Kept, name)
		}
	}
	sort.Strings(plan.Kept)
	sort.Slice(plan.Moves, func(a, b int) bool { return plan.Moves[a].Name < plan.Moves[b].Name })
	return plan, nil
}

// Apply performs the planned moves, then rewrites references. Moves run first
// so a failed rename leaves every reference still pointing at a real skill.
func (p *Plan) Apply() error {
	if len(p.Moves) > 0 {
		if err := os.MkdirAll(filepath.Join(p.Root, JigDir), 0o755); err != nil {
			return err
		}
	}
	for _, mv := range p.Moves {
		if err := os.Rename(mv.From, mv.To); err != nil {
			return fmt.Errorf("move skill %q: %w", mv.Name, err)
		}
	}
	byFile := map[string][]Rewrite{}
	for _, rw := range p.Rewrites {
		byFile[rw.File] = append(byFile[rw.File], rw)
	}
	for file, rewrites := range byFile {
		if err := rewriteFile(file, rewrites); err != nil {
			return err
		}
	}
	return nil
}

func rewriteFile(file string, rewrites []Rewrite) error {
	info, err := os.Stat(file)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	lines := bytes.Split(data, []byte("\n"))
	for _, rw := range rewrites {
		idx := rw.Line - 1
		line := string(lines[idx])
		m := skillLine.FindStringSubmatchIndex(line)
		if m == nil || line[m[4]:m[5]] != rw.Old {
			return fmt.Errorf("%s:%d: reference changed since planning", file, rw.Line)
		}
		lines[idx] = []byte(line[:m[4]] + rw.New + line[m[5]:])
	}
	return os.WriteFile(file, bytes.Join(lines, []byte("\n")), info.Mode().Perm())
}

// workflowFiles returns every *.toml under .agents/jig (excluding the jig
// skills tree) plus the extra paths, de-duplicated and sorted.
func workflowFiles(root string, extra []string) ([]string, error) {
	seen := map[string]bool{}
	var files []string
	skip := filepath.Join(root, JigDir)
	walk := func(start string) error {
		return filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if path == start && errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				if path == skip {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.EqualFold(filepath.Ext(path), ".toml") && !seen[path] {
				seen[path] = true
				files = append(files, path)
			}
			return nil
		})
	}
	if err := walk(filepath.Join(root, ".agents", "jig")); err != nil {
		return nil, err
	}
	for _, p := range extra {
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		if _, err := os.Stat(p); err != nil {
			return nil, err
		}
		if err := walk(filepath.Clean(p)); err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

// sharedSkillName reports the skill name when target is a direct child of
// the shared skills dir.
func sharedSkillName(shared, target string) (string, bool) {
	r, err := filepath.Rel(shared, filepath.Clean(target))
	if err != nil || r == "." || strings.HasPrefix(r, "..") || strings.ContainsRune(r, filepath.Separator) {
		return "", false
	}
	return r, true
}

// IsShared reports whether an absolute skill path lives in a .agents/skills
// tree, where every agent will discover it.
func IsShared(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	return strings.Contains(clean, "/.agents/skills/")
}

func rel(root, path string) string {
	if r, err := filepath.Rel(root, path); err == nil {
		return r
	}
	return path
}
