package scaffold

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"jig/internal/workflow"
)

func TestTemplateAssetsEmbedded(t *testing.T) {
	entries, err := fs.ReadDir(templateAssets, "templates")
	if err != nil {
		t.Fatalf("read embedded templates: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("embedded templates directory is empty")
	}
}

func TestPlan(t *testing.T) {
	t.Run("computes complete ordered plan without writing", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "My-Project")
		plan, err := Plan(Options{Dir: target, Template: "minimal"})
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}

		if plan.Name != "my-project" {
			t.Fatalf("Name = %q, want %q", plan.Name, "my-project")
		}
		wantPaths := []string{
			filepath.Join(target, ".agents", "jig", "my-project.toml"),
			filepath.Join(target, ".gitignore"),
		}
		if got := plannedPaths(plan.Files); !reflect.DeepEqual(got, wantPaths) {
			t.Fatalf("planned paths = %v, want %v", got, wantPaths)
		}
		if plan.Files[0].Exists || plan.Files[0].Append {
			t.Fatalf("workflow plan flags = Exists:%t Append:%t", plan.Files[0].Exists, plan.Files[0].Append)
		}
		if plan.Files[1].Exists || !plan.Files[1].Append || string(plan.Files[1].Contents) != ".jig/\n" {
			t.Fatalf("gitignore plan = %#v", plan.Files[1])
		}
		if !bytes.Contains(plan.Files[0].Contents, []byte(`name        = "my-project"`)) {
			t.Fatalf("workflow does not contain normalized name:\n%s", plan.Files[0].Contents)
		}
		if _, err := workflow.Decode(string(plan.Files[0].Contents), ""); err != nil {
			t.Fatalf("rendered minimal workflow is invalid: %v", err)
		}
		if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Plan modified target: stat error = %v", err)
		}
	})

	t.Run("records collisions and appends to unrelated gitignore", func(t *testing.T) {
		target := t.TempDir()
		workflowPath := filepath.Join(target, ".agents", "jig", "demo.toml")
		if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(workflowPath, []byte("existing"), 0o644); err != nil {
			t.Fatalf("write workflow: %v", err)
		}
		if err := os.WriteFile(filepath.Join(target, ".gitignore"), []byte("dist"), 0o644); err != nil {
			t.Fatalf("write .gitignore: %v", err)
		}

		plan, err := Plan(Options{Dir: target, Name: "demo"})
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		if len(plan.Files) != 2 || !plan.Files[0].Exists || !plan.Files[1].Exists {
			t.Fatalf("planned files = %#v", plan.Files)
		}
		if got, want := string(plan.Files[1].Contents), "\n.jig/\n"; got != want {
			t.Fatalf("gitignore append = %q, want %q", got, want)
		}
	})

	t.Run("skips gitignore already covering jig", func(t *testing.T) {
		for _, entry := range []string{".jig/\n", ".jig\n"} {
			t.Run(strings.TrimSpace(entry), func(t *testing.T) {
				target := t.TempDir()
				if err := os.WriteFile(filepath.Join(target, ".gitignore"), []byte(entry), 0o644); err != nil {
					t.Fatalf("write .gitignore: %v", err)
				}
				plan, err := Plan(Options{Dir: target, Name: "demo"})
				if err != nil {
					t.Fatalf("Plan: %v", err)
				}
				if len(plan.Files) != 1 || plan.Files[0].Path != filepath.Join(target, ".agents", "jig", "demo.toml") {
					t.Fatalf("planned files = %#v", plan.Files)
				}
			})
		}
	})

	t.Run("rejects malicious name before planning paths", func(t *testing.T) {
		target := t.TempDir()
		_, err := Plan(Options{Dir: target, Name: "../escape"})
		if err == nil || !strings.Contains(err.Error(), "single path segment") {
			t.Fatalf("Plan traversal error = %v", err)
		}
	})
}

func TestAppendJigIgnore(t *testing.T) {
	tests := []struct {
		name     string
		existing string
		want     string
	}{
		{name: "empty", want: ".jig/\n"},
		{name: "adds newline", existing: "dist", want: "dist\n.jig/\n"},
		{name: "preserves newline", existing: "dist\n", want: "dist\n.jig/\n"},
		{name: "slash entry exists", existing: ".jig/\n", want: ".jig/\n"},
		{name: "bare entry exists", existing: "build\n.jig\n", want: "build\n.jig\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := string(AppendJigIgnore([]byte(test.existing)))
			if got != test.want {
				t.Fatalf("AppendJigIgnore(%q) = %q, want %q", test.existing, got, test.want)
			}
		})
	}
}

func TestApply(t *testing.T) {
	t.Run("writes the complete plan and verifies it", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "demo")
		plan, err := Plan(Options{Dir: target, Name: "demo"})
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}

		result, err := plan.Apply(false)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if len(result.Files) != len(plan.Files) {
			t.Fatalf("written files = %d, want %d", len(result.Files), len(plan.Files))
		}
		for i, written := range result.Files {
			if written.Path != plan.Files[i].Path || written.Overwritten {
				t.Fatalf("written file %d = %#v, want created %s", i, written, plan.Files[i].Path)
			}
			info, err := os.Stat(written.Path)
			if err != nil {
				t.Fatalf("stat %s: %v", written.Path, err)
			}
			if got := info.Mode().Perm(); got != 0o644 {
				t.Fatalf("mode for %s = %o, want 644", written.Path, got)
			}
		}
		if err := Verify(result); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	})

	t.Run("returns completed writes with a later error", func(t *testing.T) {
		target := t.TempDir()
		first := filepath.Join(target, "first.txt")
		blocked := filepath.Join(target, "blocked")
		if err := os.Mkdir(blocked, 0o755); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
		plan := &WritePlan{
			TargetDir:    target,
			WorkflowPath: first,
			Files: []PlannedFile{
				{Path: first, Contents: []byte("complete\n")},
				{Path: blocked, Contents: []byte("cannot replace a directory\n"), Exists: true},
			},
		}

		result, err := plan.Apply(true)
		if err == nil {
			t.Fatal("Apply unexpectedly succeeded")
		}
		if len(result.Files) != 1 || result.Files[0].Path != first {
			t.Fatalf("partial result = %#v, want first completed path", result)
		}
		if _, err := os.Stat(first); err != nil {
			t.Fatalf("completed file was not written: %v", err)
		}
	})
}

func TestVerifyReportsWorkflowPath(t *testing.T) {
	target := t.TempDir()
	workflowPath := filepath.Join(target, ".agents", "jig", "broken.toml")
	plan := &WritePlan{
		TargetDir:    target,
		WorkflowPath: workflowPath,
		Files: []PlannedFile{{
			Path:     workflowPath,
			Contents: []byte("[workflow]\nname ="),
		}},
	}
	result, err := plan.Apply(false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	err = Verify(result)
	if err == nil || !strings.Contains(err.Error(), workflowPath) {
		t.Fatalf("Verify error = %v, want workflow path", err)
	}
}

func plannedPaths(files []PlannedFile) []string {
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = file.Path
	}
	return paths
}
