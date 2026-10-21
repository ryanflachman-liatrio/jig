package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

const skillsTestWorkflow = `[workflow]
name = "w"
version = "1"

[[step]]
id = "a"
type = "agent"
skill = "../skills/draft"
`

func writeSkillsFixture(t *testing.T) (root, wfPath string) {
	t.Helper()
	root = t.TempDir()
	skillDir := filepath.Join(root, ".agents", "skills", "draft")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: draft\ndescription: d\n---\nDraft it.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wfPath = filepath.Join(root, ".agents", "jig", "w.toml")
	if err := os.MkdirAll(filepath.Dir(wfPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wfPath, []byte(skillsTestWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, wfPath
}

func TestSkillsMigrateDryRunThenApply(t *testing.T) {
	root, wfPath := writeSkillsFixture(t)

	wf, err := workflow.Load(wfPath)
	if err != nil {
		t.Fatalf("Load before: %v", err)
	}
	if w := sharedSkillWarnings(wfPath, wf); len(w) != 1 {
		t.Fatalf("warnings before = %v, want 1", w)
	}

	var out, errOut bytes.Buffer
	if code := runSkillsMigrate([]string{"--root", root}, &out, &errOut); code != 0 {
		t.Fatalf("dry-run exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "dry-run: 1 skill(s)") {
		t.Fatalf("dry-run output = %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".agents", "skills", "draft")); err != nil {
		t.Fatalf("dry-run moved skill: %v", err)
	}

	out.Reset()
	if code := runSkillsMigrate([]string{"--root", root, "--apply"}, &out, &errOut); code != 0 {
		t.Fatalf("apply exit %d: %s", code, errOut.String())
	}
	wf, err = workflow.Load(wfPath)
	if err != nil {
		t.Fatalf("Load after: %v", err)
	}
	if w := sharedSkillWarnings(wfPath, wf); len(w) != 0 {
		t.Fatalf("warnings after = %v, want none", w)
	}
	if got := wf.Steps[0].AgentPrompt(); !strings.Contains(got, "Draft it.") {
		t.Fatalf("prompt after migrate = %q", got)
	}
}

func TestSkillsUsage(t *testing.T) {
	if code := runSkills(nil); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}
