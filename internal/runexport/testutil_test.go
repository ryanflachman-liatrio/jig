package runexport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"jig/internal/engine"
	"jig/internal/toolcall"
	"jig/internal/transcript"
	"jig/internal/workflow"
)

const fixtureRunID = "run-1"

func makeRunStore(t *testing.T) (root, runDir string) {
	t.Helper()
	root = t.TempDir()
	runDir = filepath.Join(root, "runs", fixtureRunID)
	if err := os.MkdirAll(filepath.Join(runDir, "steps", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"journal.jsonl": "{\"sequence\":1,\"private\":\"SYNTHETIC_TOKEN_DO_NOT_SHARE\"}\n",
		"workflow.json": "{\"toml\":\"private workflow source\"}",
		filepath.Join("steps", "agent", "transcript.jsonl"): "{\"role\":\"assistant\",\"content\":\"private transcript\"}\n",
	} {
		if err := os.WriteFile(filepath.Join(runDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, runDir
}

func payloadHash(t *testing.T, runDir string) string {
	t.Helper()
	h := sha256.New()
	for _, name := range []string{"journal.jsonl", "workflow.json", filepath.Join("steps", "agent", "transcript.jsonl")} {
		data, err := os.ReadFile(filepath.Join(runDir, name))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// --- Rich fixtures for tasks 2.0-4.0: synthetic-only identifiers/secrets. ---

// syntheticSecrets are every seeded private value a disclosure scan must find
// absent from a default (structural) archive, and present only in sanitized/
// aliased form in a text-mode archive. Every shape is a fabricated
// placeholder, never a real credential.
var syntheticSecrets = []string{
	"AKIAFAKEFAKEFAKEFAKE",                         // AWS-key-shaped
	"ghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE0123", // GitHub-token-shaped (36+ chars after ghp_)
	"zZq9xP2vLk7mNw3RtY8sB1cFhJ4dGkQe6uXo0aWnC5",   // high-entropy-shaped
}

const (
	fixtureRunName      = "prod-incident-482"
	fixtureWorkflowName = "export-fixture"
	fixtureSourceDir    = "/home/fixture-operator/project/workflows"
)

// journalKindFor mirrors engine's private eventKind switch so tests can build
// well-formed envelopes without exporting that mapping from the engine
// package. Kept in sync manually; a mismatch would surface immediately as a
// decode failure in the fixture-consuming tests.
func journalKindFor(e engine.Event) string {
	switch e.(type) {
	case engine.RunStarted:
		return "run_started"
	case engine.RunFinished:
		return "run_finished"
	case engine.StepStatus:
		return "step_status"
	case engine.StepMessage:
		return "step_message"
	case engine.GateResult:
		return "gate_result"
	case engine.RouteSelected:
		return "route_selected"
	case engine.StepsReset:
		return "steps_reset"
	case engine.FanOutExpanded:
		return "fan_out_expanded"
	case engine.RunError:
		return "run_error"
	default:
		return "unknown"
	}
}

func marshalTestEnvelope(t *testing.T, seq int, ts time.Time, e engine.Event) []byte {
	t.Helper()
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	env := struct {
		Seq  int             `json:"seq"`
		Ts   time.Time       `json:"ts"`
		Kind string          `json:"kind"`
		Data json.RawMessage `json:"data"`
	}{seq, ts, journalKindFor(e), data}
	out, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func writeJSONLFile(t *testing.T, path string, lines [][]byte) {
	t.Helper()
	var buf []byte
	for _, l := range lines {
		buf = append(buf, l...)
		buf = append(buf, '\n')
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
}

// fixtureSteps is the static workflow used by every rich fixture: a command
// step, an agent step depending on it, and a skipped review step. The
// "family#0"/"family#1" runtime fan-out children are declared only in the
// journal (via FanOutExpanded), matching how a real dynamic foreach expands.
func fixtureWorkflowSteps() []workflow.Step {
	return []workflow.Step{
		{ID: "fetch", Type: workflow.StepCommand},
		{ID: "agent-1", Type: workflow.StepAgent, Backend: workflow.BackendClaude, Transport: workflow.TransportSDK, DependsOn: []string{"fetch"}},
		{ID: "review-1", Type: workflow.StepReview, When: "false"},
	}
}

// writeWorkflowSnapshot renders workflow.json in the exact shape
// engine.DecodeWorkflowSnapshot expects (mirrors the private
// engine.workflowSnapshot wire struct).
func writeWorkflowSnapshot(t *testing.T, runDir string) {
	t.Helper()
	toml := "# synthetic fixture workflow, never parsed by export\n"
	sum := sha256.Sum256([]byte(toml))
	steps := fixtureWorkflowSteps()
	snap := struct {
		SourcePath    string            `json:"source_path,omitempty"`
		BaseDir       string            `json:"base_dir,omitempty"`
		SHA256        string            `json:"sha256"`
		TOML          string            `json:"toml"`
		ModuleSources []json.RawMessage `json:"module_sources,omitempty"`
		Meta          workflow.Meta     `json:"meta"`
		Defaults      workflow.Defaults `json:"defaults"`
		PublicSteps   []workflow.Step   `json:"public_steps,omitempty"`
		ExpandedSteps []workflow.Step   `json:"expanded_steps,omitempty"`
	}{
		SourcePath:    filepath.Join(fixtureSourceDir, "export-fixture.toml"),
		BaseDir:       fixtureSourceDir,
		SHA256:        hex.EncodeToString(sum[:]),
		TOML:          toml,
		Meta:          workflow.Meta{Name: fixtureWorkflowName, Version: "1.0.0"},
		PublicSteps:   steps,
		ExpandedSteps: steps,
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "workflow.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// fixtureLiveHomeDir returns the actual test-runner home directory so the
// fixture can demonstrate the real "current home-directory prefix" boundary
// (spec FR-09) rather than a directory export could never discover. A path
// seen only in the original run's transcript from a *different* machine is
// explicitly out of scope (spec's stated residual risk).
func fixtureLiveHomeDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory available to exercise home-path sanitization")
	}
	return home
}

func fixtureBaseTime() time.Time {
	return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
}

// fixtureJournalLines builds the full accepted journal for the intact
// fixture: a command step, a retried agent step, a fan-out family with two
// children, route/reset provenance, a gate result, and a skipped review step.
func fixtureJournalLines(t *testing.T) [][]byte {
	t.Helper()
	base := fixtureBaseTime()
	at := func(offsetSeconds int) time.Time { return base.Add(time.Duration(offsetSeconds) * time.Second) }
	var lines [][]byte
	seq := 0
	add := func(offset int, e engine.Event) {
		seq++
		lines = append(lines, marshalTestEnvelope(t, seq, at(offset), e))
	}
	add(0, engine.RunStarted{RunID: fixtureRunName, Workflow: fixtureWorkflowName, Steps: []string{"fetch", "agent-1", "review-1"}})
	add(1, engine.StepStatus{RunID: fixtureRunName, StepID: "fetch", From: "pending", To: "running", Attempt: 1})
	add(2, engine.StepStatus{RunID: fixtureRunName, StepID: "fetch", From: "running", To: "succeeded", Attempt: 1})
	add(3, engine.StepStatus{RunID: fixtureRunName, StepID: "agent-1", From: "pending", To: "running", Attempt: 1})
	add(4, engine.StepStatus{RunID: fixtureRunName, StepID: "agent-1", From: "running", To: "failed", Attempt: 1, Err: "synthetic failure containing " + syntheticSecrets[0]})
	add(5, engine.RouteSelected{RunID: fixtureRunName, StepID: "agent-1", RouteIndex: 1, When: "failed", Goto: "agent-1", Iteration: 1, Max: 3})
	add(6, engine.StepStatus{RunID: fixtureRunName, StepID: "agent-1", From: "failed", To: "running", Attempt: 2, Iteration: 1})
	cost := 0.42
	add(7, engine.StepStatus{RunID: fixtureRunName, StepID: "agent-1", From: "running", To: "succeeded", Attempt: 2, Iteration: 1, Cost: &cost, Tokens: 1234})
	add(8, engine.StepMessage{RunID: fixtureRunName, StepID: "agent-1", Seq: 5, Iteration: 1})
	add(9, engine.FanOutExpanded{
		SchemaVersion: engine.FanOutExpandedVersion, RunID: fixtureRunName, FamilyID: "family",
		ManifestDigest: "deadbeef", Instances: []engine.FanOutInstanceDescriptor{
			{InstanceID: "family#0", Index: 0, ItemSHA256: "aa"},
			{InstanceID: "family#1", Index: 1, ItemSHA256: "bb"},
		},
	})
	add(10, engine.StepStatus{RunID: fixtureRunName, StepID: "family#0", From: "pending", To: "succeeded", Attempt: 1})
	add(11, engine.StepStatus{RunID: fixtureRunName, StepID: "family#1", From: "pending", To: "succeeded", Attempt: 1})
	add(12, engine.StepsReset{RunID: fixtureRunName, Target: "fetch", Closure: []string{"fetch", "agent-1"}, RewindTo: "deadbeefcafebabe"})
	add(13, engine.GateResult{RunID: fixtureRunName, StepID: "review-1", Passed: true})
	add(14, engine.StepStatus{RunID: fixtureRunName, StepID: "review-1", From: "pending", To: "skipped"})
	add(15, engine.RunFinished{RunID: fixtureRunName, Failed: false})
	return lines
}

func textEntry(seq int, ts time.Time, iteration, attempt int, role transcript.Role, blocks ...transcript.Block) transcript.Entry {
	return transcript.Entry{Seq: seq, Ts: ts.Format(time.RFC3339), Iteration: iteration, Attempt: attempt, Role: role, Blocks: blocks}
}

// fixtureTranscripts returns transcript.jsonl content for every step
// directory the intact fixture creates, seeded with every syntheticSecrets
// shape, the fixture's home/source paths, and a thinking block that must
// never survive into a text-mode export.
func fixtureTranscripts(t *testing.T) map[string][]byte {
	t.Helper()
	base := fixtureBaseTime()
	home := fixtureLiveHomeDir(t)
	fetchLines := []transcript.Entry{
		textEntry(1, base.Add(1*time.Second), 0, 1, transcript.RoleSystem,
			transcript.Block{Type: transcript.BlockText, Text: "command output near " + home + " leaked " + syntheticSecrets[0]}),
	}
	agentLines := []transcript.Entry{
		textEntry(1, base.Add(3*time.Second), 0, 1, transcript.RoleAssistant,
			transcript.Block{Type: transcript.BlockText, Text: "investigating " + fixtureRunName + " in " + fixtureWorkflowName + ": " + syntheticSecrets[1]},
			transcript.Block{Type: transcript.BlockThinking, Text: "internal reasoning that must never be exported: " + syntheticSecrets[2]},
		),
		textEntry(2, base.Add(4*time.Second), 1, 2, transcript.RoleAssistant,
			transcript.Block{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{
				ID: "toolcall-1", Title: "Bash", Status: "running",
				Input: json.RawMessage(`{"command":"echo ` + syntheticSecrets[0] + ` from ` + home + `"}`),
			}},
		),
		textEntry(3, base.Add(5*time.Second), 1, 2, transcript.RoleUser,
			transcript.Block{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{
				ID: "toolcall-1", Title: "Bash", Status: "completed",
				Content: []toolcall.Content{{Type: "text", Text: "result contains " + syntheticSecrets[2]}},
			}},
		),
		textEntry(4, base.Add(6*time.Second), 1, 2, transcript.RoleAssistant,
			transcript.Block{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{
				ID: "toolcall-2", Title: "Edit", Status: "completed",
				Content: []toolcall.Content{{Type: "diff", Diff: &toolcall.Diff{
					Path:    filepath.Join(fixtureSourceDir, "file.go"),
					OldText: strPtr("old content with " + syntheticSecrets[0]),
					NewText: "new content, agent-1 fixed it",
				}}},
			}},
		),
		textEntry(5, base.Add(7*time.Second), 1, 2, transcript.RoleResult,
			transcript.Block{Type: transcript.BlockText, Text: "attempt 2 succeeded"}),
	}
	familyLine := []transcript.Entry{
		textEntry(1, base.Add(9*time.Second), 0, 1, transcript.RoleSystem, transcript.Block{Type: transcript.BlockText, Text: "child step ran"}),
	}
	encode := func(entries []transcript.Entry) []byte {
		var lines [][]byte
		for _, e := range entries {
			data, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, data)
		}
		var buf []byte
		for _, l := range lines {
			buf = append(buf, l...)
			buf = append(buf, '\n')
		}
		return buf
	}
	return map[string][]byte{
		"fetch":    encode(fetchLines),
		"agent-1":  encode(agentLines),
		"family#0": encode(familyLine),
		"family#1": encode(familyLine),
	}
}

func strPtr(s string) *string { return &s }

// buildIntactFixture writes a complete, gap-free synthetic run store: a
// well-formed journal, workflow snapshot, and a transcript for every step the
// journal reports as dispatched (so a structural export of it is complete,
// not partial). Returns the persistence root and run directory.
func buildIntactFixture(t *testing.T) (root, runDir string) {
	t.Helper()
	root = t.TempDir()
	runDir = filepath.Join(root, "runs", fixtureRunName)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSONLFile(t, filepath.Join(runDir, "journal.jsonl"), fixtureJournalLines(t))
	writeWorkflowSnapshot(t, runDir)
	for stepID, data := range fixtureTranscripts(t) {
		dir := filepath.Join(runDir, "steps", stepID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "transcript.jsonl"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, runDir
}
