package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/harness"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func TestCaptureStream_ReadableOutputAndAttemptEvidence(t *testing.T) {
	for _, structuredOnly := range []bool{false, true} {
		name := "prose"
		if structuredOnly {
			name = "structured documents"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			body := "# Findings\n\n" + strings.Repeat("Detailed synthetic evidence.\n", 500)
			obj := map[string]any{"status": "succeeded", "summary": "Research complete"}
			want := body
			if structuredOnly {
				obj["report"] = body
				want = "## Report\n\n" + body + "\n\n"
			}
			raw, _ := json.Marshal(obj)
			text := "```json\n" + string(raw) + "\n```"
			if !structuredOnly {
				text = body + "\n" + text
				want = strings.TrimRight(body, "\n")
			}
			req := engine.StepRequest{
				Step: &workflow.Step{Output: "report.md"}, ExecutionDir: t.TempDir(),
				TranscriptPath: filepath.Join(dir, "transcript.jsonl"),
				Generation:     2, Iteration: 3, Attempt: 1,
			}
			res, err := captureStream(scriptChan(
				harness.Event{Type: harness.EventTextDelta, Text: "incomplete preview"},
				harness.Event{Type: harness.EventText, Text: text},
				harness.Event{Type: harness.EventAssistantEnd},
				harness.Event{Type: harness.EventResult, Structured: raw},
			), req, &captureReporter{}, time.Now(), "")
			if err != nil || res.Status != step.StatusSucceeded {
				t.Fatalf("capture: %+v, %v", res, err)
			}
			prefix := "evidence/generation-002-iteration-003-attempt-001"
			for path, expected := range map[string]string{
				filepath.Join(dir, "output.md"):              want,
				filepath.Join(req.ExecutionDir, "report.md"): want,
				filepath.Join(dir, prefix+".output.md"):      want,
				filepath.Join(dir, "output.json"):            string(raw),
				filepath.Join(dir, prefix+".output.json"):    string(raw),
				filepath.Join(dir, "metadata.md"):            "## Status\n\nsucceeded\n\n## Summary\n\nResearch complete\n\n",
				filepath.Join(dir, prefix+".metadata.md"):    "## Status\n\nsucceeded\n\n## Summary\n\nResearch complete\n\n",
			} {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != expected {
					t.Errorf("%s does not contain expected artifact (read error: %v)", path, err)
				}
			}
			if res.OutputPath != filepath.Join(dir, "output.md") || string(res.Structured) != string(raw) {
				t.Fatalf("downstream result lost output or structured fields: %+v", res)
			}
			r, err := transcript.Open(req.TranscriptPath)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := r.Window(0, 0)
			if err != nil || len(entries) != 1 || entries[0].Blocks[0].Text != text {
				t.Fatalf("finalized transcript changed: %v", err)
			}
		})
	}
}

func TestCaptureStream_ArtifactWriteFailure(t *testing.T) {
	for _, artifact := range []string{"output.md", "output.json", "metadata.md"} {
		t.Run(artifact, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, artifact), 0o755); err != nil {
				t.Fatal(err)
			}
			res, err := captureStream(scriptChan(
				harness.Event{Type: harness.EventText, Text: "Full report"},
				harness.Event{Type: harness.EventAssistantEnd},
				harness.Event{Type: harness.EventResult, Structured: json.RawMessage(`{"summary":"done"}`)},
			), engine.StepRequest{Step: &workflow.Step{}, TranscriptPath: filepath.Join(dir, "transcript.jsonl")}, &captureReporter{}, time.Now(), "")
			if err != nil || res.Status != step.StatusFailed || !strings.Contains(res.Err, "write agent "+artifact) || res.OutputPath != "" {
				t.Fatalf("artifact failure advertised success/output: %+v, %v", res, err)
			}
		})
	}
}

func TestCaptureStream_RemovesStaleDerivedArtifacts(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"output.md", "output.json", "metadata.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("previous attempt"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := captureStream(scriptChan(harness.Event{Type: harness.EventResult}), engine.StepRequest{
		Step: &workflow.Step{}, TranscriptPath: filepath.Join(dir, "transcript.jsonl"),
	}, &captureReporter{}, time.Now(), "")
	if err != nil || res.Status != step.StatusSucceeded || res.OutputPath != "" {
		t.Fatalf("capture: %+v, %v", res, err)
	}
	for _, name := range []string{"output.md", "output.json", "metadata.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("stale %s remains: %v", name, err)
		}
	}
}
