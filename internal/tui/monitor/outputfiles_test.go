package monitor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"jig/internal/datastore"
	"jig/internal/engine"
)

func TestStepOutputFiles(t *testing.T) {
	tests := []struct {
		name          string
		persistOff    bool
		setup         func(t *testing.T, runDir string) string // returns declaredOutput
		expectedCount int
		assertions    func(t *testing.T, files map[string]outputFile)
	}{
		{
			name: "all_four",
			setup: func(t *testing.T, runDir string) string {
				seedCanonical(t, runDir)
				declDir := filepath.Join(runDir, "decls")
				if err := os.MkdirAll(declDir, 0o755); err != nil {
					t.Fatalf("mkdir decls: %v", err)
				}
				declPath := filepath.Join(declDir, "notes.txt")
				if err := os.WriteFile(declPath, []byte("notes"), 0o644); err != nil {
					t.Fatalf("write notes.txt: %v", err)
				}
				return declPath
			},
			expectedCount: 4,
			assertions: func(t *testing.T, files map[string]outputFile) {
				if f := files["input.md"]; f.kind != kindMarkdown || f.err != nil {
					t.Errorf("input.md: kind=%v err=%v; want kindMarkdown nil", f.kind, f.err)
				}
				if f := files["output.md"]; f.kind != kindMarkdown || f.err != nil {
					t.Errorf("output.md: kind=%v err=%v; want kindMarkdown nil", f.kind, f.err)
				}
				if f := files["output.json"]; f.kind != kindJSON || f.err != nil {
					t.Errorf("output.json: kind=%v err=%v; want kindJSON nil", f.kind, f.err)
				}
				if f := files["notes.txt"]; f.kind != kindOther || f.err != nil {
					t.Errorf("notes.txt: kind=%v err=%v; want kindOther nil", f.kind, f.err)
				}
			},
		},
		{
			name: "only_md",
			setup: func(t *testing.T, runDir string) string {
				mdPath := datastore.OutputPath(runDir, "plan")
				if err := os.MkdirAll(filepath.Dir(mdPath), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(mdPath, []byte("md"), 0o644); err != nil {
					t.Fatalf("write output.md: %v", err)
				}
				return ""
			},
			expectedCount: 3,
			assertions: func(t *testing.T, files map[string]outputFile) {
				if f := files["input.md"]; f.err == nil {
					t.Errorf("input.md: err=nil; want non-nil (does not exist)")
				}
				if f := files["output.md"]; f.kind != kindMarkdown || f.err != nil {
					t.Errorf("output.md: kind=%v err=%v; want kindMarkdown nil", f.kind, f.err)
				}
				if f := files["output.json"]; f.err == nil {
					t.Errorf("output.json: err=nil; want non-nil (does not exist)")
				}
			},
		},
		{
			name: "declared_dir",
			setup: func(t *testing.T, runDir string) string {
				seedCanonical(t, runDir)
				declDir := filepath.Join(runDir, "decls", "adir")
				if err := os.MkdirAll(declDir, 0o755); err != nil {
					t.Fatalf("mkdir adir: %v", err)
				}
				return declDir
			},
			expectedCount: 4,
			assertions: func(t *testing.T, files map[string]outputFile) {
				if f := files["adir"]; !errors.Is(f.err, errIsDir) {
					t.Errorf("adir: err=%v; want errIsDir", f.err)
				}
			},
		},
		{
			name: "dedup",
			setup: func(t *testing.T, runDir string) string {
				seedCanonical(t, runDir)
				return datastore.OutputPath(runDir, "plan")
			},
			expectedCount: 3,
			assertions: func(t *testing.T, files map[string]outputFile) {
				if f := files["output.md"]; f.kind != kindMarkdown || f.err != nil {
					t.Errorf("output.md: kind=%v err=%v; want kindMarkdown nil", f.kind, f.err)
				}
				if f := files["output.json"]; f.kind != kindJSON || f.err != nil {
					t.Errorf("output.json: kind=%v err=%v; want kindJSON nil", f.kind, f.err)
				}
			},
		},
		{
			name: "diagnostics",
			setup: func(t *testing.T, runDir string) string {
				seedCanonical(t, runDir)
				stepDir := filepath.Dir(datastore.InputPath(runDir, "plan"))
				for name, content := range map[string]string{
					"acp-diagnostics.jsonl":  `{"event":"prompt_started"}` + "\n",
					"acp-adapter.stderr.log": "adapter warning\n",
					"transcript.jsonl":       `{"role":"assistant"}` + "\n",
				} {
					if err := os.WriteFile(filepath.Join(stepDir, name), []byte(content), 0o600); err != nil {
						t.Fatalf("write %s: %v", name, err)
					}
				}
				return ""
			},
			expectedCount: 5,
			assertions: func(t *testing.T, files map[string]outputFile) {
				if f := files["acp-diagnostics.jsonl"]; f.kind != kindJSONL || f.err != nil {
					t.Errorf("acp-diagnostics.jsonl: kind=%v err=%v; want kindJSONL nil", f.kind, f.err)
				}
				if f := files["acp-adapter.stderr.log"]; f.kind != kindLog || f.err != nil {
					t.Errorf("acp-adapter.stderr.log: kind=%v err=%v; want kindLog nil", f.kind, f.err)
				}
				if _, ok := files["transcript.jsonl"]; ok {
					t.Error("transcript.jsonl should stay in the transcript viewer")
				}
			},
		},
		{
			name:          "no_persistence",
			persistOff:    true,
			setup:         func(t *testing.T, runDir string) string { return "" },
			expectedCount: 0,
			assertions:    func(t *testing.T, files map[string]outputFile) {},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runDir := t.TempDir()
			declared := tc.setup(t, runDir)
			if tc.persistOff {
				runDir = ""
			}
			result := stepOutputFiles(runDir, "plan", declared)
			if len(result) != tc.expectedCount {
				t.Fatalf("got %d entries, want %d", len(result), tc.expectedCount)
			}
			files := make(map[string]outputFile, len(result))
			for _, f := range result {
				files[f.name] = f
			}
			tc.assertions(t, files)
		})
	}
}

func TestStepMessageDiscoversLiveStepFiles(t *testing.T) {
	runDir := t.TempDir()
	m := New("run-1")
	m.RunDir = runDir
	m.stepFiles["plan"] = stepOutputFiles(runDir, "plan", "")

	inputPath := datastore.InputPath(runDir, "plan")
	if err := os.MkdirAll(filepath.Dir(inputPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, []byte("# Effective prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	diagnosticsPath := filepath.Join(filepath.Dir(inputPath), "acp-diagnostics.jsonl")
	if err := os.WriteFile(diagnosticsPath, []byte(`{"event":"prompt_started"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	m, _ = m.handleEngineEvent(engine.StepMessage{RunID: "run-1", StepID: "plan", Seq: 1})
	files := make(map[string]outputFile, len(m.stepFiles["plan"]))
	for _, file := range m.stepFiles["plan"] {
		files[file.path] = file
	}
	if file := files[inputPath]; file.err != nil || file.kind != kindMarkdown {
		t.Errorf("input.md: kind=%v err=%v; want kindMarkdown nil", file.kind, file.err)
	}
	if file := files[diagnosticsPath]; file.err != nil || file.kind != kindJSONL {
		t.Errorf("acp-diagnostics.jsonl: kind=%v err=%v; want kindJSONL nil", file.kind, file.err)
	}
}

func TestCreateOutputFilesUsesShortestUniqueLabels(t *testing.T) {
	root := t.TempDir()
	paths := []string{
		filepath.Join(root, "alpha", "report.json"),
		filepath.Join(root, "beta", "report.json"),
		filepath.Join(root, "notes.md"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	files := createOutputFiles(paths)
	got := make(map[string]string, len(files))
	for _, file := range files {
		got[file.path] = file.label
	}
	want := map[string]string{
		paths[0]: "alpha/report.json",
		paths[1]: "beta/report.json",
		paths[2]: "notes.md",
	}
	for path, label := range want {
		if got[path] != label {
			t.Errorf("label for %s = %q, want %q", path, got[path], label)
		}
	}
}

func seedCanonical(t *testing.T, runDir string) {
	t.Helper()
	mdPath := datastore.OutputPath(runDir, "plan")
	if err := os.MkdirAll(filepath.Dir(mdPath), 0o755); err != nil {
		t.Fatalf("mkdir step dir: %v", err)
	}
	if err := os.WriteFile(mdPath, []byte("md"), 0o644); err != nil {
		t.Fatalf("write output.md: %v", err)
	}
	if err := os.WriteFile(datastore.InputPath(runDir, "plan"), []byte("prompt"), 0o644); err != nil {
		t.Fatalf("write input.md: %v", err)
	}
	if err := os.WriteFile(datastore.OutputJSONPath(runDir, "plan"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write output.json: %v", err)
	}
}
