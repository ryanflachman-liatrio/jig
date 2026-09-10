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
