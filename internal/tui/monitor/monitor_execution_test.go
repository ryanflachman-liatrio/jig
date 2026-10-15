package monitor

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/ryanflachman-liatrio/jig/internal/toolcall"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

func TestExecutionSummaryFallbacks(t *testing.T) {
	for _, tt := range []struct {
		name, summary, title, command, want string
	}{
		{"description", "Check tests", "go test ./...", "go test ./...", "Check tests"},
		{"descriptive title", "", "Check tests", "go test ./...", "Check tests"},
		{"generic title", "", "Terminal", "go test ./...", "go test ./..."},
		{"multiline command title", "", "`echo first\necho last`", "echo first\necho last", "echo first …"},
		{"terminal controls", "\x1b[31mCheck\x1b[0m\n tests\a", "Bash", "false", "Check tests"},
		{"no input", "", "Run tests", "", "Run tests"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input, _ := json.Marshal(map[string]string{"command": tt.command})
			a := &toolcall.Activity{Kind: "execute", Summary: tt.summary, Title: tt.title, Input: input}
			if got := summarizeActivity(a, 120).detail; got != tt.want {
				t.Fatalf("summary = %q, want %q", got, tt.want)
			}
		})
	}
	for _, width := range []int{0, 1, 12, 80, 200} {
		got := executionSummary(&toolcall.Activity{Summary: strings.Repeat("界", 200)}, width)
		limit := 80
		if width > 0 {
			limit = min(limit, width)
		}
		if lipgloss.Width(got) > limit || !strings.HasSuffix(got, shared.EllipsisGlyph) {
			t.Fatalf("width %d: unbounded summary %q", width, got)
		}
	}
}

func TestExecutionViewsKeepSummaryShortAndCommandInspectable(t *testing.T) {
	command := "printf 'first'\n" + strings.Repeat("echo synthetic-line\n", 35) + "echo final-line"
	entries := toolExchange("run", "bash", map[string]any{"command": command, "cwd": "/tmp/synthetic"}, 0, 0, 0, false)
	entries[1].Blocks[0].Tool.Input = append(json.RawMessage(nil), entries[0].Blocks[0].Tool.Input...)
	for i := range entries {
		entries[i].Blocks[0].Tool.Summary = "Check tests"
		entries[i].Blocks[0].Tool.Title = "`" + command + "`"
	}
	entries[1].Blocks[0].Tool.Output = json.RawMessage(`"tests passed"`)
	for _, compact := range []bool{false, true} {
		for _, expanded := range []bool{false, true} {
			for _, width := range []int{36, 80} {
				t.Run(fmt.Sprintf("compact_%t_expanded_%t_width_%d", compact, expanded, width), func(t *testing.T) {
					m := newMonitorWithSteps(t)
					m.chatStep = "a"
					m.transcriptInnerW = width
					m.compactToolGroups = compact
					m.setChatPage(transcript.Page{Entries: entries})
					m.chatItemExpandAll = expanded
					body := stripANSI(m.itemTranscriptBody())
					if !strings.Contains(body, "Check tests") {
						t.Fatalf("missing summary:\n%s", body)
					}
					if expanded {
						for _, want := range []string{"Command:", "printf 'first'", "echo final-line", "Arguments:", "cwd", "Output:", "tests passed", "more lines"} {
							if !strings.Contains(body, want) {
								t.Errorf("missing %q:\n%s", want, body)
							}
						}
						if strings.Contains(body, `"command"`) || strings.Contains(body, `\n`) {
							t.Fatalf("command still rendered as JSON:\n%s", body)
						}
					} else if strings.Contains(body, "printf") || strings.Contains(body, "Command:") {
						t.Fatalf("collapsed view includes command:\n%s", body)
					}
					for _, row := range strings.Split(body, "\n") {
						if lipgloss.Width(row) > width {
							t.Fatalf("row exceeds %d cells: %q", width, row)
						}
					}
					msg := m.copyTranscriptItemCmd()().(shared.ClipboardRequest)
					copied := msg.Loader()
					encoded, _ := json.Marshal(command)
					if copied.Err != nil || !strings.Contains(copied.Payload, string(encoded)) {
						t.Fatalf("full input not preserved in copy: %v", copied.Err)
					}
				})
			}
		}
	}
}

func TestExecutionInputFallbackAndControls(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 80
	for _, raw := range []string{`{"command":["sh","-c","pwd"]}`, `{"command":42}`, `{"other":"value"}`, `{"command":`} {
		var b strings.Builder
		m.writeToolActivityDetails(&b, &toolcall.Activity{Kind: "execute", Input: json.RawMessage(raw)}, true, detailAnchorHead)
		if !strings.Contains(b.String(), "Input:") || strings.Contains(b.String(), "Command:") {
			t.Fatalf("unknown shape lost generic input view: %s", b.String())
		}
	}
	input, _ := json.Marshal(map[string]string{"command": "echo first\n\x1b[31mecho second\x1b[0m\a"})
	var b strings.Builder
	m.writeExecutionInput(&b, &toolcall.Activity{Kind: "execute", Input: input})
	plain := stripANSI(b.String())
	if !strings.Contains(plain, "echo first\n") || !strings.Contains(plain, "echo second") || strings.ContainsAny(plain, "\a\x1b") {
		t.Fatalf("decoded command not safe or multiline: %q", plain)
	}
}
