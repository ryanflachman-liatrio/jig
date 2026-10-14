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
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
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

// TestExportStepBackend verifies the exported step summary's backend comes
// from the step's resolved agent persisted in the run snapshot.
func TestExportStepBackend(t *testing.T) {
	_, _, _, run := exportFixture(t, false)
	for _, st := range run.Steps {
		if st.Type != "agent" {
			continue
		}
		if st.Backend != "codex" {
			t.Fatalf("agent step backend = %q, want codex from the resolved agent", st.Backend)
		}
		return
	}
	t.Fatal("no agent step in exported run summary")
}

func TestAuthoringAssetsExcludedFromExport(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "PRIVATE_PROVENANCE_MARKER")
	files := map[string]string{
		"authored.sh":     "echo PRIVATE_SCRIPT_BYTES_MARKER\n",
		"skill/SKILL.md":  "---\nname: synthetic\ndescription: Export fixture\n---\nPRIVATE_PROMPT_BYTES_MARKER\n",
		"skill/helper.sh": "PRIVATE_HELPER_BYTES_MARKER\n",
		"schema.json":     `{"type":"object","properties":{"result":{"type":"string","description":"PRIVATE_SCHEMA_BYTES_MARKER"}}}`,
		"template.md":     "PRIVATE_TEMPLATE_BYTES_MARKER\n",
		"workflow.toml": `[workflow]
name='authoring-export'
version='1'
[[step]]
id='worker'
type='command'
script='authored.sh'
output_type={enum=['go','skip']}
[[step]]
id='unused'
type='agent'
skill='skill'
schema_file='schema.json'
output_template='template.md'
depends_on=['worker']
when="worker == 'skip'"
`,
	}
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	wf, err := workflow.Load(filepath.Join(dir, "workflow.toml"))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), ".jig")
	manager := engine.NewManager(authoringExportExecutor{}, root)
	r, err := manager.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	if r.Wait().Failed {
		t.Fatal("fixture failed")
	}
	rd := manager.RunDir(r.ID)
	entry := transcript.Entry{Seq: 1, Ts: fixtureBaseTime().Format(time.RFC3339), Role: transcript.RoleSystem, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "benign synthetic output"}}}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rd, "steps/worker"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rd, "steps/worker/transcript.jsonl"), append(encoded, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rd, "scratch.txt"), []byte("PRIVATE_SCRATCH_MARKER"), 0600); err != nil {
		t.Fatal(err)
	}
	markers := []string{"PRIVATE_PROVENANCE_MARKER", "PRIVATE_SCRIPT_BYTES_MARKER", "PRIVATE_PROMPT_BYTES_MARKER", "PRIVATE_HELPER_BYTES_MARKER", "PRIVATE_SCHEMA_BYTES_MARKER", "PRIVATE_TEMPLATE_BYTES_MARKER", "PRIVATE_SCRATCH_MARKER"}
	snapshotPath := filepath.Join(rd, "workflow.json")
	original, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture must contain private snapshot values, not just private blobs.
	if !bytes.Contains(original, []byte("PRIVATE_PROMPT_BYTES_MARKER")) || !bytes.Contains(original, []byte("PRIVATE_PROVENANCE_MARKER")) {
		t.Fatal("missing private snapshot seeds")
	}
	for _, sourceState := range []string{"intact", "unavailable_captures", "hostile_reference"} {
		t.Run(sourceState, func(t *testing.T) {
			if sourceState == "unavailable_captures" {
				if err := os.RemoveAll(filepath.Join(rd, "authoring")); err != nil {
					t.Fatal(err)
				}
			}
			if sourceState == "hostile_reference" {
				var snap map[string]json.RawMessage
				if err := json.Unmarshal(original, &snap); err != nil {
					t.Fatal(err)
				}
				var reference map[string]any
				if err := json.Unmarshal(snap["authoring"], &reference); err != nil {
					t.Fatal(err)
				}
				reference["path"] = filepath.Join(dir, "PRIVATE_OUTSIDE_READ_MARKER")
				snap["authoring"], err = json.Marshal(reference)
				if err != nil {
					t.Fatal(err)
				}
				// The unsupported reference invalidates the locked payload;
				// display decoding must reject it without attempting path IO.
				data, err := json.Marshal(snap)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(snapshotPath, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			inv, err := collectInventory(rd)
			if err != nil {
				t.Fatal(err)
			}
			for _, rec := range inv.files {
				if strings.Contains(rec.name, "authoring") || rec.name == "scratch.txt" {
					t.Fatalf("expanded selected-source inventory: %s", rec.name)
				}
			}
			pinned, err := os.OpenRoot(rd)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(snapshotPath)
			if err != nil {
				t.Fatal(err)
			}
			budget := newBudget(maxTotalInput)
			decoded, _, gap := readWorkflowSnapshot(pinned, inv, budget)
			pinned.Close()
			if budget.remaining != maxTotalInput-int64(len(data)) {
				t.Fatal("snapshot reader followed an unselected source")
			}
			if sourceState == "hostile_reference" {
				if decoded != nil || gap == nil {
					t.Fatal("hostile path accepted")
				}
			} else if decoded == nil || gap != nil {
				t.Fatalf("capture storage affected pure decode: %+v", gap)
			}
			for _, includeText := range []bool{false, true} {
				destination := filepath.Join(t.TempDir(), "export.zip")
				if _, err := Export(t.Context(), Options{Root: root, RunID: r.ID, Destination: destination, IncludeText: includeText}); err != nil {
					t.Fatal(err)
				}
				raw, err := os.ReadFile(destination)
				if err != nil {
					t.Fatal(err)
				}
				members := readZip(t, destination)
				var manifest Manifest
				if err := json.Unmarshal(members[manifestMember], &manifest); err != nil {
					t.Fatal(err)
				}
				wantCompleteness := "complete"
				if sourceState == "hostile_reference" {
					wantCompleteness = "partial"
				}
				if manifest.Completeness != wantCompleteness {
					t.Fatalf("completeness=%s gaps=%+v", manifest.Completeness, manifest.Gaps)
				}
				for _, marker := range append(markers, "PRIVATE_OUTSIDE_READ_MARKER") {
					if bytes.Contains(raw, []byte(marker)) {
						t.Fatalf("raw ZIP disclosed %s", marker)
					}
					for name, data := range members {
						if strings.Contains(name, marker) || bytes.Contains(data, []byte(marker)) {
							t.Fatalf("ZIP member %s disclosed %s", name, marker)
						}
					}
				}
				var summary RunSummary
				if err := json.Unmarshal(members[runMember], &summary); err != nil {
					t.Fatal(err)
				}
				if summary.State != "succeeded" || !summary.StateAuthoritative || len(summary.Steps) == 0 {
					t.Fatalf("lost useful run evidence: %+v", summary)
				}
				if includeText && !bytes.Contains(members[transcriptMember], []byte("benign synthetic output")) {
					t.Fatal("lost allowlisted transcript")
				}
				t.Logf("%s text=%t: scanned %d ZIP members and raw bytes; seven private capture/scratch markers absent; selected snapshot bytes only", sourceState, includeText, len(members))
			}
		})
	}
}

type authoringExportExecutor struct{}

func (authoringExportExecutor) Execute(context.Context, engine.StepRequest, engine.Reporter) (*step.Result, error) {
	return &step.Result{Status: step.StatusSucceeded, Verdict: "go"}, nil
}
