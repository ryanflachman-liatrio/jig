package runexport

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readZip extracts every member of the archive at path into a name->bytes map.
func readZip(t *testing.T, path string) map[string][]byte {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer zr.Close()
	out := make(map[string][]byte)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open member %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read member %s: %v", f.Name, err)
		}
		out[f.Name] = data
	}
	return out
}

func exportFixture(t *testing.T, includeText bool) (string, map[string][]byte, Manifest, RunSummary) {
	t.Helper()
	root, _ := buildIntactFixture(t)
	dest := filepath.Join(t.TempDir(), "bundle.zip")
	result, err := Export(context.Background(), Options{Root: root, RunID: fixtureRunName, Destination: dest, IncludeText: includeText})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if result.Destination != dest {
		t.Fatalf("Result.Destination = %q, want %q", result.Destination, dest)
	}
	members := readZip(t, dest)
	var manifest Manifest
	if err := json.Unmarshal(members[manifestMember], &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	var run RunSummary
	if err := json.Unmarshal(members[runMember], &run); err != nil {
		t.Fatalf("decode run.json: %v", err)
	}
	return dest, members, manifest, run
}

func TestStructuralArchiveMemberContract(t *testing.T) {
	dest, members, manifest, run := exportFixture(t, false)
	wantMembers := []string{readmeMember, manifestMember, runMember, eventsMember}
	for _, name := range wantMembers {
		if _, ok := members[name]; !ok {
			t.Fatalf("archive %s missing member %q; got %v", dest, name, memberNames(members))
		}
	}
	if _, ok := members[transcriptMember]; ok {
		t.Fatalf("structural archive must omit %s", transcriptMember)
	}
	if manifest.FormatVersion != formatVersion || manifest.RedactionPolicyVersion != redactionPolicyVersion {
		t.Fatalf("manifest versions = %+v", manifest)
	}
	if manifest.ContentMode != ModeStructural {
		t.Fatalf("content_mode = %q, want %q", manifest.ContentMode, ModeStructural)
	}
	if manifest.Completeness != "complete" {
		t.Fatalf("completeness = %q, gaps = %+v, want complete", manifest.Completeness, manifest.Gaps)
	}
	if run.RunAlias != runAlias || run.WorkflowAlias != workflowAlias {
		t.Fatalf("run/workflow alias = %q/%q, want %s/%s", run.RunAlias, run.WorkflowAlias, runAlias, workflowAlias)
	}
	if !run.StateAuthoritative || run.State != "succeeded" {
		t.Fatalf("run state = %q authoritative=%t, want succeeded/true", run.State, run.StateAuthoritative)
	}
	if run.TotalCostUSD == nil || *run.TotalCostUSD != 0.42 {
		t.Fatalf("total cost = %v, want 0.42", run.TotalCostUSD)
	}
	if len(run.Steps) == 0 {
		t.Fatal("run.json has no steps")
	}
	for _, s := range run.Steps {
		if !strings.HasPrefix(s.Alias, "step-") {
			t.Fatalf("step alias %q is not an alias", s.Alias)
		}
	}
	// Every manifest member digest must be present and non-empty, and must
	// exclude the manifest itself (its own bytes are unknown while building).
	seen := map[string]bool{}
	for _, m := range manifest.Members {
		seen[m.Name] = true
		if m.SHA256 == "" || m.Bytes == 0 {
			t.Fatalf("member %s has empty digest/size: %+v", m.Name, m)
		}
	}
	if seen[manifestMember] {
		t.Fatal("manifest.json must not list its own digest")
	}
}

func TestTextModeArchiveIncludesSanitizedTranscript(t *testing.T) {
	_, structuralMembers, _, _ := exportFixture(t, false)
	if _, ok := structuralMembers[transcriptMember]; ok {
		t.Fatal("structural export unexpectedly included transcript.jsonl")
	}
	dest, members, manifest, _ := exportFixture(t, true)
	data, ok := members[transcriptMember]
	if !ok {
		t.Fatalf("text-mode archive %s missing %s", dest, transcriptMember)
	}
	if manifest.ContentMode != ModeSanitizedText {
		t.Fatalf("content_mode = %q, want %q", manifest.ContentMode, ModeSanitizedText)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 {
		t.Fatal("transcript.jsonl has no records")
	}
	var sawThinkingOmission, sawToolAlias bool
	for _, line := range lines {
		var rec TranscriptRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode transcript record: %v", err)
		}
		for _, blk := range rec.Blocks {
			if blk.Type == "thinking" {
				sawThinkingOmission = true
				if blk.Text != "" || blk.Omitted == "" {
					t.Fatalf("thinking block leaked content: %+v", blk)
				}
			}
			if blk.ToolAlias != "" {
				sawToolAlias = true
			}
		}
	}
	if !sawThinkingOmission {
		t.Fatal("fixture's thinking block was not projected as an omission marker")
	}
	if !sawToolAlias {
		t.Fatal("no tool alias was allocated for the fixture's tool_use/tool_result blocks")
	}
	if manifest.Counters.Omissions["thinking_content"] == 0 {
		t.Fatalf("manifest omission counters missing thinking_content: %+v", manifest.Counters)
	}
}

func memberNames(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestClosedProjectionExcludesPrivateData(t *testing.T) {
	for _, mode := range []struct {
		name        string
		includeText bool
	}{{"structural", false}, {"text", true}} {
		t.Run(mode.name, func(t *testing.T) {
			dest, members, _, _ := exportFixture(t, mode.includeText)
			raw, err := os.ReadFile(dest)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range syntheticSecrets {
				if bytes.Contains(raw, []byte(secret)) {
					t.Fatalf("archive bytes (headers/comments included) contain seeded secret %q", secret)
				}
			}
			for name, data := range members {
				for _, needle := range []string{fixtureRunName, fixtureWorkflowName, fixtureLiveHomeDir(t), fixtureSourceDir} {
					if bytes.Contains(data, []byte(needle)) {
						t.Fatalf("member %s in %s mode contains original identifier/path %q", name, mode.name, needle)
					}
				}
				if !mode.includeText {
					for _, secret := range syntheticSecrets {
						if bytes.Contains(data, []byte(secret)) {
							t.Fatalf("structural member %s contains seeded secret %q", name, secret)
						}
					}
				}
			}
			if mode.includeText {
				// Text mode must have fully replaced every seeded secret with
				// a fixed marker, never a partial four-character preview.
				transcriptData := members[transcriptMember]
				for _, secret := range syntheticSecrets {
					if bytes.Contains(transcriptData, []byte(secret)) {
						t.Fatalf("sanitized transcript still contains seeded secret %q", secret)
					}
				}
				if !bytes.Contains(transcriptData, []byte("[REDACTED:")) {
					t.Fatal("sanitized transcript has no redaction markers; sanitizer may not have run")
				}
			}
		})
	}
}

func TestArchivePublicationNoOverwrite(t *testing.T) {
	root, _ := buildIntactFixture(t)
	dest := filepath.Join(t.TempDir(), "bundle.zip")
	if err := os.WriteFile(dest, []byte("competing content"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Export(context.Background(), Options{Root: root, RunID: fixtureRunName, Destination: dest})
	if err == nil {
		t.Fatal("Export succeeded against a pre-existing destination")
	}
	data, readErr := os.ReadFile(dest)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "competing content" {
		t.Fatalf("competing destination was overwritten: %q", data)
	}
}

func TestExportNoUsableEvidenceFailsWithoutArchive(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", fixtureRunName)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "bundle.zip")
	_, err := Export(context.Background(), Options{Root: root, RunID: fixtureRunName, Destination: dest})
	if err == nil {
		t.Fatal("Export succeeded with no journal or transcript evidence")
	}
	if _, statErr := os.Lstat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("no-evidence export created a destination: %v", statErr)
	}
}
