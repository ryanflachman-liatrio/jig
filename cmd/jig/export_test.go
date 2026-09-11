package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportUsageAndHelp(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"run-1"}, // missing --destination
		{"run-1", "extra", "--destination", "x.zip"}, // arity
	} {
		var stdout, stderr bytes.Buffer
		code := exportMain(args, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("exportMain(%v) exit = %d, want 2; stderr=%s", args, code, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("exportMain(%v) wrote to stdout on a usage error: %q", args, stdout.String())
		}
		if !strings.Contains(stderr.String(), "usage: jig export") {
			t.Fatalf("exportMain(%v) stderr missing usage text: %q", args, stderr.String())
		}
	}
}

func TestExportHelpDocumentsContract(t *testing.T) {
	if !strings.Contains(exportUsage, "structural") || !strings.Contains(exportUsage, "--include-text") {
		t.Fatalf("export usage missing content-mode documentation: %s", exportUsage)
	}
	if !strings.Contains(exportUsage, "local-only") {
		t.Fatalf("export usage missing local-only guarantee: %s", exportUsage)
	}
	if strings.Contains(strings.ToLower(exportUsage), "overwrite") && !strings.Contains(exportUsage, "never overwrite") {
		t.Fatalf("export usage should state it never overwrites a destination: %s", exportUsage)
	}
}

func TestExportFlagsAfterRunID(t *testing.T) {
	root := t.TempDir()
	// No run store exists; this exercises argument parsing/reordering only —
	// the usage refusal on missing root/run is proof the flags parsed.
	dest := filepath.Join(t.TempDir(), "bundle.zip")
	var stdout, stderr bytes.Buffer
	code := exportMain([]string{"run-1", "--root", root, "--destination", dest}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exportMain with flags after RUN_ID = %d, want 2 (empty root refusal); stderr=%s", code, stderr.String())
	}
}

func TestExportStructuralSuccessEndToEnd(t *testing.T) {
	root, runDir := buildCLIFixtureRunStore(t)
	_ = runDir
	dest := filepath.Join(t.TempDir(), "bundle.zip")
	var stdout, stderr bytes.Buffer
	code := exportMain([]string{"cli-run-1", "--root", root, "--destination", dest}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exportMain = %d, want 0; stderr=%s", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != dest {
		t.Fatalf("stdout = %q, want exactly the destination %q", got, dest)
	}
	if _, err := zip.OpenReader(dest); err != nil {
		t.Fatalf("published file is not a valid zip: %v", err)
	}
}

func TestExportDestinationCollisionRefusal(t *testing.T) {
	root, _ := buildCLIFixtureRunStore(t)
	dest := filepath.Join(t.TempDir(), "bundle.zip")
	if err := os.WriteFile(dest, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := exportMain([]string{"cli-run-1", "--root", root, "--destination", dest}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exportMain against an existing destination = %d, want 2; stderr=%s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout should be empty on refusal: %q", stdout.String())
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "existing" {
		t.Fatalf("competing destination was modified: %q", data)
	}
}

// buildCLIFixtureRunStore writes the minimal run store this package's CLI
// tests need: a journal with RunStarted/StepStatus/RunFinished and a matching
// step transcript, using only synthetic content.
func buildCLIFixtureRunStore(t *testing.T) (root, runDir string) {
	t.Helper()
	root = t.TempDir()
	runDir = filepath.Join(root, "runs", "cli-run-1")
	if err := os.MkdirAll(filepath.Join(runDir, "steps", "fetch"), 0o755); err != nil {
		t.Fatal(err)
	}
	journal := `{"seq":1,"ts":"2030-01-01T00:00:00Z","kind":"run_started","data":{"RunID":"cli-run-1","Workflow":"cli-fixture","Steps":["fetch"]}}
{"seq":2,"ts":"2030-01-01T00:00:01Z","kind":"step_status","data":{"RunID":"cli-run-1","StepID":"fetch","From":"pending","To":"running","Attempt":1}}
{"seq":3,"ts":"2030-01-01T00:00:02Z","kind":"step_status","data":{"RunID":"cli-run-1","StepID":"fetch","From":"running","To":"succeeded","Attempt":1}}
{"seq":4,"ts":"2030-01-01T00:00:03Z","kind":"run_finished","data":{"RunID":"cli-run-1","Failed":false}}
`
	if err := os.WriteFile(filepath.Join(runDir, "journal.jsonl"), []byte(journal), 0o600); err != nil {
		t.Fatal(err)
	}
	transcriptLine := `{"seq":1,"ts":"2030-01-01T00:00:01Z","role":"system","blocks":[{"type":"text","text":"fetch ran"}]}
`
	if err := os.WriteFile(filepath.Join(runDir, "steps", "fetch", "transcript.jsonl"), []byte(transcriptLine), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, runDir
}
