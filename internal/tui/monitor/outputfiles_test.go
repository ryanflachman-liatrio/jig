package monitor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"jig/internal/datastore"
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
			name: "all_three",
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
			expectedCount: 3,
			assertions: func(t *testing.T, files map[string]outputFile) {
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
			expectedCount: 2,
			assertions: func(t *testing.T, files map[string]outputFile) {
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
			expectedCount: 3,
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
			expectedCount: 2,
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

// seedCanonical writes a valid output.md and output.json under runDir's step dir.
func seedCanonical(t *testing.T, runDir string) {
	t.Helper()
	mdPath := datastore.OutputPath(runDir, "plan")
	if err := os.MkdirAll(filepath.Dir(mdPath), 0o755); err != nil {
		t.Fatalf("mkdir step dir: %v", err)
	}
	if err := os.WriteFile(mdPath, []byte("md"), 0o644); err != nil {
		t.Fatalf("write output.md: %v", err)
	}
	if err := os.WriteFile(datastore.OutputJSONPath(runDir, "plan"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write output.json: %v", err)
	}
}
