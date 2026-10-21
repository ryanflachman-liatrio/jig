package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func skill(t *testing.T, root, name string) {
	writeFile(t, filepath.Join(root, ".agents", "skills", name, "SKILL.md"), "---\nname: "+name+"\ndescription: d\n---\nbody\n")
}

func TestMigrateMovesOnlyReferencedSkillsAndRewritesRefs(t *testing.T) {
	root := t.TempDir()
	skill(t, root, "plan")
	skill(t, root, "shared")
	skill(t, root, "unused")
	writeFile(t, filepath.Join(root, ".agents", "jig", "wf.toml"),
		"[[step]]\nid = \"a\"\nskill = \"../skills/plan\"   # keep comment\n[[step]]\nskill = \"../skills/shared\"\n")
	writeFile(t, filepath.Join(root, ".agents", "jig", "modules", "m.toml"),
		"skill = \"../../skills/plan\"\n")
	writeFile(t, filepath.Join(root, "examples", "e.toml"), "skill = \"../.agents/skills/plan\"\n")

	plan, err := PlanMigration(Options{Root: root, Extra: []string{"examples"}, Keep: []string{"shared"}})
	if err != nil {
		t.Fatalf("PlanMigration: %v", err)
	}
	if len(plan.Moves) != 1 || plan.Moves[0].Name != "plan" {
		t.Fatalf("moves = %+v, want only plan", plan.Moves)
	}
	if len(plan.Rewrites) != 3 {
		t.Fatalf("rewrites = %+v, want 3", plan.Rewrites)
	}
	if len(plan.Kept) != 1 || plan.Kept[0] != "shared" {
		t.Fatalf("kept = %v", plan.Kept)
	}
	// Planning has no side effects.
	if _, err := os.Stat(filepath.Join(root, ".agents", "skills", "plan")); err != nil {
		t.Fatalf("plan moved files: %v", err)
	}

	if err := plan.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".agents", "jig", "skills", "plan", "SKILL.md")); err != nil {
		t.Fatalf("skill not moved: %v", err)
	}
	for _, name := range []string{"shared", "unused"} {
		if _, err := os.Stat(filepath.Join(root, ".agents", "skills", name)); err != nil {
			t.Fatalf("%s should stay shared: %v", name, err)
		}
	}
	wf := readFile(t, filepath.Join(root, ".agents", "jig", "wf.toml"))
	if !strings.Contains(wf, `skill = "skills/plan"   # keep comment`) || !strings.Contains(wf, `skill = "../skills/shared"`) {
		t.Fatalf("wf.toml = %q", wf)
	}
	if got := readFile(t, filepath.Join(root, ".agents", "jig", "modules", "m.toml")); got != "skill = \"../skills/plan\"\n" {
		t.Fatalf("m.toml = %q", got)
	}
	if got := readFile(t, filepath.Join(root, "examples", "e.toml")); got != "skill = \"../.agents/jig/skills/plan\"\n" {
		t.Fatalf("e.toml = %q", got)
	}

	// A second run is a no-op.
	again, err := PlanMigration(Options{Root: root, Extra: []string{"examples"}})
	if err != nil {
		t.Fatalf("second PlanMigration: %v", err)
	}
	if len(again.Moves) != 1 || again.Moves[0].Name != "shared" {
		t.Fatalf("second plan moves = %+v, want only the no-longer-kept shared skill", again.Moves)
	}
}

func TestMigrateRefusesExistingDestination(t *testing.T) {
	root := t.TempDir()
	skill(t, root, "plan")
	writeFile(t, filepath.Join(root, ".agents", "jig", "skills", "plan", "SKILL.md"), "x")
	writeFile(t, filepath.Join(root, ".agents", "jig", "wf.toml"), "skill = \"../skills/plan\"\n")
	if _, err := PlanMigration(Options{Root: root}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want already exists", err)
	}
}

func TestMigrateReportsMissingSkill(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".agents", "jig", "wf.toml"), "skill = \"../skills/ghost\"\n")
	if _, err := PlanMigration(Options{Root: root}); err == nil || !strings.Contains(err.Error(), "wf.toml:1") {
		t.Fatalf("err = %v, want located error", err)
	}
}

func TestIsShared(t *testing.T) {
	if !IsShared("/r/.agents/skills/plan") || IsShared("/r/.agents/jig/skills/plan") {
		t.Fatal("IsShared misclassified")
	}
}
