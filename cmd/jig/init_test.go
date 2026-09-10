package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jig/internal/headless"
	"jig/internal/scaffold"
	"jig/internal/workflow"
)

func TestInit(t *testing.T) {
	t.Run("scaffolds a valid workflow", func(t *testing.T) {
		target := t.TempDir()
		var stdout, stderr bytes.Buffer
		code := initMain([]string{"--dir", target, "--name", "My-Flow"}, &stdout, &stderr)
		if code != headless.ExitOK {
			t.Fatalf("initMain exit = %d, stderr = %q", code, stderr.String())
		}

		workflowPath := filepath.Join(target, ".agents", "jig", "my-flow.toml")
		for _, path := range []string{workflowPath, filepath.Join(target, ".gitignore")} {
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("expected scaffold path %s: %v", path, err)
			}
		}
		loaded, err := workflow.Load(workflowPath)
		if err != nil {
			t.Fatalf("workflow.Load: %v", err)
		}
		if loaded.Meta.Name != "my-flow" {
			t.Fatalf("workflow name = %q, want my-flow", loaded.Meta.Name)
		}
		if stderr.Len() != 0 {
			t.Fatalf("stderr = %q, want empty", stderr.String())
		}
	})

	t.Run("surfaces post-write validation failure", func(t *testing.T) {
		original := initPlan
		defer func() { initPlan = original }()

		target := t.TempDir()
		workflowPath := filepath.Join(target, ".agents", "jig", "broken.toml")
		initPlan = func(scaffold.Options) (*scaffold.WritePlan, error) {
			return &scaffold.WritePlan{
				TargetDir:    target,
				WorkflowPath: workflowPath,
				Files: []scaffold.PlannedFile{{
					Path:     workflowPath,
					Contents: []byte("[workflow]\nname ="),
				}},
			}, nil
		}

		var stdout, stderr bytes.Buffer
		code := initMain([]string{"--dir", target, "--name", "broken"}, &stdout, &stderr)
		if code != headless.ExitFailed {
			t.Fatalf("initMain exit = %d, want %d", code, headless.ExitFailed)
		}
		if !strings.Contains(stderr.String(), workflowPath) || !strings.Contains(stderr.String(), "parse workflow") {
			t.Fatalf("stderr = %q, want loader error with workflow path", stderr.String())
		}
		if !strings.Contains(stdout.String(), "created "+workflowPath) {
			t.Fatalf("stdout = %q, want completed write", stdout.String())
		}
	})
}

func TestInitRejectsUnsafeName(t *testing.T) {
	tests := []string{"../escape", "a/b", ".", ""}
	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			target := t.TempDir()
			var stdout, stderr bytes.Buffer
			code := initMain([]string{"--dir", target, "--name", name}, &stdout, &stderr)
			if code != headless.ExitUsage {
				t.Fatalf("initMain exit = %d, want %d; stderr = %q", code, headless.ExitUsage, stderr.String())
			}
			entries, err := os.ReadDir(target)
			if err != nil {
				t.Fatalf("ReadDir: %v", err)
			}
			if len(entries) != 0 {
				t.Fatalf("unsafe name created files: %v", entries)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestInitSuccessOutput(t *testing.T) {
	target := t.TempDir()
	plan, err := scaffold.Plan(scaffold.Options{Dir: target, Name: "demo"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := initMain([]string{"--dir", target, "--name", "demo"}, &stdout, &stderr)
	if code != headless.ExitOK {
		t.Fatalf("initMain exit = %d, stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != len(plan.Files)+3 {
		t.Fatalf("stdout lines = %v, want %d created + 3 hints", lines, len(plan.Files))
	}
	for i, file := range plan.Files {
		if want := "created " + file.Path; lines[i] != want {
			t.Fatalf("stdout line %d = %q, want %q", i, lines[i], want)
		}
	}
	for _, want := range []string{"jig validate " + plan.WorkflowPath, "jig run " + plan.WorkflowPath, "jig doctor"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	}
}

func TestPrintHelpIncludesInit(t *testing.T) {
	var output bytes.Buffer
	printHelpTo(&output)
	if !strings.Contains(output.String(), "init") {
		t.Fatalf("help output = %q, want init command", output.String())
	}
}

func TestInitGitignore(t *testing.T) {
	tests := []struct {
		name     string
		existing string
	}{
		{name: "absent"},
		{name: "unrelated", existing: "dist/\n"},
		{name: "already ignored", existing: "dist/\n.jig/\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := t.TempDir()
			if test.existing != "" {
				if err := os.WriteFile(filepath.Join(target, ".gitignore"), []byte(test.existing), 0o644); err != nil {
					t.Fatalf("write .gitignore: %v", err)
				}
			}
			var stdout, stderr bytes.Buffer
			if code := initMain([]string{"--dir", target, "--name", "demo"}, &stdout, &stderr); code != headless.ExitOK {
				t.Fatalf("initMain exit = %d, stderr = %q", code, stderr.String())
			}
			contents, err := os.ReadFile(filepath.Join(target, ".gitignore"))
			if err != nil {
				t.Fatalf("read .gitignore: %v", err)
			}
			count := 0
			for _, line := range strings.Split(string(contents), "\n") {
				if strings.TrimSpace(line) == ".jig/" {
					count++
				}
			}
			if count != 1 {
				t.Fatalf(".gitignore = %q, want exactly one .jig/ line", contents)
			}
		})
	}
}

func TestInitCollision(t *testing.T) {
	newScaffold := func(t *testing.T) string {
		t.Helper()
		target := t.TempDir()
		var stdout, stderr bytes.Buffer
		if code := initMain([]string{"--dir", target, "--name", "demo"}, &stdout, &stderr); code != headless.ExitOK {
			t.Fatalf("initial initMain exit = %d, stderr = %q", code, stderr.String())
		}
		return target
	}

	t.Run("refuses collisions without writing", func(t *testing.T) {
		target := newScaffold(t)
		workflowPath := filepath.Join(target, ".agents", "jig", "demo.toml")
		before, err := os.ReadFile(workflowPath)
		if err != nil {
			t.Fatal(err)
		}

		var stdout, stderr bytes.Buffer
		code := initMain([]string{"--dir", target, "--name", "demo"}, &stdout, &stderr)
		if code != headless.ExitFailed {
			t.Fatalf("initMain exit = %d, want %d", code, headless.ExitFailed)
		}
		if stdout.Len() != 0 || !strings.Contains(stderr.String(), workflowPath) || !strings.Contains(stderr.String(), "--force") {
			t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
		}
		after, err := os.ReadFile(workflowPath)
		if err != nil || string(after) != string(before) {
			t.Fatalf("workflow changed after collision: %q, err = %v", after, err)
		}
	})

	t.Run("force overwrites and preserves unrelated files", func(t *testing.T) {
		target := newScaffold(t)
		unrelated := filepath.Join(target, ".agents", "jig", "keep.txt")
		if err := os.WriteFile(unrelated, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		code := initMain([]string{"--dir", target, "--name", "demo", "--force"}, &stdout, &stderr)
		if code != headless.ExitOK || stderr.Len() != 0 {
			t.Fatalf("initMain exit = %d, stderr = %q", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "overwrote "+filepath.Join(target, ".agents", "jig", "demo.toml")) {
			t.Fatalf("stdout = %q, want overwritten workflow", stdout.String())
		}
		if got, err := os.ReadFile(unrelated); err != nil || string(got) != "keep" {
			t.Fatalf("unrelated file = %q, err = %v", got, err)
		}
	})

	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "dry run", args: []string{"--dry-run"}},
		{name: "dry run dominates force", args: []string{"--dry-run", "--force"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := newScaffold(t)
			workflowPath := filepath.Join(target, ".agents", "jig", "demo.toml")
			before, err := os.ReadFile(workflowPath)
			if err != nil {
				t.Fatal(err)
			}
			args := append([]string{"--dir", target, "--name", "demo"}, test.args...)
			var stdout, stderr bytes.Buffer
			code := initMain(args, &stdout, &stderr)
			if code != headless.ExitOK || stderr.Len() != 0 {
				t.Fatalf("initMain exit = %d, stderr = %q", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), "would overwrite "+workflowPath) {
				t.Fatalf("stdout = %q, want preview", stdout.String())
			}
			after, err := os.ReadFile(workflowPath)
			if err != nil || string(after) != string(before) {
				t.Fatalf("workflow changed after dry run: %q, err = %v", after, err)
			}
		})
	}

	t.Run("dry run creates nothing in an empty target", func(t *testing.T) {
		target := t.TempDir()
		workflowPath := filepath.Join(target, ".agents", "jig", "demo.toml")
		var stdout, stderr bytes.Buffer
		code := initMain([]string{"--dir", target, "--name", "demo", "--dry-run"}, &stdout, &stderr)
		if code != headless.ExitOK || stderr.Len() != 0 {
			t.Fatalf("initMain exit = %d, stderr = %q", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "would create "+workflowPath) {
			t.Fatalf("stdout = %q, want create preview", stdout.String())
		}
		if _, err := os.Stat(workflowPath); !os.IsNotExist(err) {
			t.Fatalf("dry run created workflow: stat error = %v", err)
		}
		if _, err := os.Stat(filepath.Join(target, ".gitignore")); !os.IsNotExist(err) {
			t.Fatalf("dry run created gitignore: stat error = %v", err)
		}
	})
}

func TestInitListTemplates(t *testing.T) {
	target := filepath.Join(t.TempDir(), "missing", "nested")
	var stdout, stderr bytes.Buffer
	code := initMain([]string{"--dir", target, "--list-templates"}, &stdout, &stderr)
	if code != headless.ExitOK || stderr.Len() != 0 {
		t.Fatalf("initMain exit = %d, stderr = %q", code, stderr.String())
	}
	for _, template := range scaffold.All() {
		if !strings.Contains(stdout.String(), template.Name) || !strings.Contains(stdout.String(), template.Description) {
			t.Fatalf("stdout = %q, missing template %#v", stdout.String(), template)
		}
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("list templates created target: stat error = %v", err)
	}
}
