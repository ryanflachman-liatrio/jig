package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestExportEndToEnd drives the real CLI entry point against temporary
// intact and damaged run stores, produces structural and text bundles,
// inspects them only with standard ZIP/JSON readers, and checks exact
// stdout/stderr/exit behavior plus source-hash preservation. It is the
// combined acceptance evidence for spec 23 (run-share-export): no live
// workflow validation, model, backend, credential, or network call is used
// anywhere in this test.
func TestExportEndToEnd(t *testing.T) {
	root, runDir := buildCLIFixtureRunStore(t)
	before := hashRunDir(t, runDir)

	t.Run("structural export", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "structural.zip")
		var stdout, stderr bytes.Buffer
		code := exportMain([]string{"cli-run-1", "--root", root, "--destination", dest}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
		}
		if got := trimmed(stdout.String()); got != dest {
			t.Fatalf("stdout = %q, want %q", got, dest)
		}
		members := readZipMembers(t, dest)
		if _, ok := members["transcript.jsonl"]; ok {
			t.Fatal("structural export must not include transcript.jsonl")
		}
		assertNoDisclosure(t, dest, members)
	})

	t.Run("text export", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "text.zip")
		var stdout, stderr bytes.Buffer
		code := exportMain([]string{"cli-run-1", "--root", root, "--destination", dest, "--include-text"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
		}
		if !bytes.Contains(stderr.Bytes(), []byte("best-effort")) {
			t.Fatalf("stderr missing best-effort text-mode notice: %q", stderr.String())
		}
		members := readZipMembers(t, dest)
		if _, ok := members["transcript.jsonl"]; !ok {
			t.Fatal("text export must include transcript.jsonl")
		}
		assertNoDisclosure(t, dest, members)
	})

	t.Run("damaged run still exports partial evidence", func(t *testing.T) {
		// A separate copy of the fixture is damaged here so the shared
		// runDir used by the other subtests, and the before/after source
		// hash check below, are never touched by this test's own mutation.
		damagedRoot, damagedRunDir := buildCLIFixtureRunStore(t)
		journalPath := filepath.Join(damagedRunDir, "journal.jsonl")
		data, err := os.ReadFile(journalPath)
		if err != nil {
			t.Fatal(err)
		}
		// Truncate the final record's trailing newline to simulate a torn
		// journal tail (a crash mid-append), which must yield a partial,
		// non-authoritative archive rather than a failure.
		torn := bytes.TrimRight(data, "\n")
		torn = torn[:len(torn)-5]
		if err := os.WriteFile(journalPath, torn, 0o600); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(t.TempDir(), "partial.zip")
		var stdout, stderr bytes.Buffer
		code := exportMain([]string{"cli-run-1", "--root", damagedRoot, "--destination", dest}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
		}
		members := readZipMembers(t, dest)
		var manifest map[string]any
		if err := json.Unmarshal(members["manifest.json"], &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest["completeness"] != "partial" {
			t.Fatalf("completeness = %v, want partial", manifest["completeness"])
		}
	})

	if after := hashRunDir(t, runDir); after != before {
		t.Fatalf("run store bytes changed across three export invocations: before=%s after=%s", before, after)
	}
}

func trimmed(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func readZipMembers(t *testing.T, path string) map[string][]byte {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open %s as zip: %v", path, err)
	}
	defer zr.Close()
	out := make(map[string][]byte)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = data
	}
	return out
}

func assertNoDisclosure(t *testing.T, archivePath string, members map[string][]byte) {
	t.Helper()
	raw, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"cli-run-1", "cli-fixture"} {
		if bytes.Contains(raw, []byte(needle)) {
			t.Fatalf("archive bytes contain original identifier %q", needle)
		}
		for name, data := range members {
			if bytes.Contains(data, []byte(needle)) {
				t.Fatalf("member %s contains original identifier %q", name, needle)
			}
		}
	}
}

func hashRunDir(t *testing.T, runDir string) string {
	t.Helper()
	var buf bytes.Buffer
	for _, rel := range []string{"journal.jsonl", "workflow.json", filepath.Join("steps", "fetch", "transcript.jsonl")} {
		data, err := os.ReadFile(filepath.Join(runDir, rel))
		if err != nil {
			continue
		}
		buf.Write(data)
	}
	return trimmed(string(buf.Bytes()))
}
