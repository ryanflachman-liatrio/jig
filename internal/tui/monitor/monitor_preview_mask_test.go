package monitor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ryanflachman-liatrio/jig/internal/config"
	"github.com/ryanflachman-liatrio/jig/internal/toolcall"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

// Synthetic, fabricated secrets only.
const (
	fakePreviewKey    = "sk-test" + "FAKEfake0123FAKEfake4567"
	fakePreviewSecret = "Q7vX2mK9pL4rT8wZ1nB6cH3jF5dG0sYa"
)

func previewMaskMonitor(t *testing.T, width int, entries []transcript.Entry) Model {
	t.Helper()
	m := newMonitorWithSteps(t)
	if width > 0 {
		m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
	}
	m.chatStep = "a"
	m.compactToolGroups = false
	m.setChatPage(transcript.Page{Entries: entries})
	return m
}

func TestCollapsedPreviewsMaskSecrets(t *testing.T) {
	t.Run("error hint", func(t *testing.T) {
		exchange := toolExchange("a", "bash", map[string]any{"command": "curl api"}, 0, 0, 0, true)
		exchange[1].Blocks[0].Tool.Content = []toolcall.Content{{Type: "text", Text: "401: invalid key " + fakePreviewKey + "\nmore"}}
		m := previewMaskMonitor(t, 0, toolGroupEntries(exchange))
		if got := toolErrorHint(&m, m.chatItems[0]); got != "401: invalid key <redacted>" {
			t.Fatalf("toolErrorHint = %q", got)
		}
	})
	t.Run("bash summary", func(t *testing.T) {
		input, _ := json.Marshal(map[string]any{"command": "export FOO_TOKEN=" + fakePreviewSecret})
		got := summarizeActivity(&toolcall.Activity{Kind: "bash", Input: input}, 80).detail
		if got != "export FOO_TOKEN=<redacted>" {
			t.Fatalf("detail = %q", got)
		}
	})
	t.Run("same-kind group row", func(t *testing.T) {
		entries := toolGroupEntries(
			toolExchange("a", "bash", map[string]any{"command": "echo " + fakePreviewKey}, 0, 0, 0, false),
			toolExchange("b", "bash", map[string]any{"command": "echo ok"}, 0, 0, 0, false),
		)
		group := groupToolTranscriptItems(buildTranscriptItems(entries, false), entries, true)[0]
		if rows := compactToolGroupRows(group, entries); rows[0].text != "echo <redacted>" {
			t.Fatalf("rows = %+v", rows)
		}
	})
	t.Run("explore row", func(t *testing.T) {
		entries := toolGroupEntries(
			toolExchange("a", "grep", map[string]any{"pattern": fakePreviewKey}, 0, 0, 0, false),
			readExchange("b", "internal/alpha.go", 0, 0, 0),
		)
		group := groupToolTranscriptItems(buildTranscriptItems(entries, false), entries, true)[0]
		if rows := compactToolGroupRows(group, entries); rows[0].text != "<redacted>" {
			t.Fatalf("rows = %+v", rows)
		}
	})
	t.Run("read tree", func(t *testing.T) {
		entries := toolGroupEntries(
			readExchange("a", "/tmp/"+fakePreviewKey+"-a.go", 0, 0, 0),
			readExchange("b", "/tmp/"+fakePreviewKey+"-b.go", 0, 0, 0),
		)
		group := groupToolTranscriptItems(buildTranscriptItems(entries, false), entries, true)[0]
		rows := compactToolGroupRows(group, entries)
		if len(rows) != 2 {
			t.Fatalf("rows = %+v, want two distinct targets", rows)
		}
		for _, row := range rows {
			if strings.Contains(row.text, fakePreviewKey) {
				t.Fatalf("read row leaked secret: %+v", row)
			}
		}
		m := previewMaskMonitor(t, 0, entries)
		m.compactToolGroups = true
		m.setChatPage(transcript.Page{Entries: entries})
		if body := ansi.Strip(m.itemTranscriptBody()); strings.Contains(body, fakePreviewKey) || !strings.Contains(body, "<redacted>") {
			t.Fatalf("rendered read tree:\n%s", body)
		}
	})
}

func TestMaskedPreviewSurvivesNarrowWidth(t *testing.T) {
	body := fakePreviewKey[len("sk-test"):]
	for _, width := range []int{20, 30, 40, 80} {
		for name, exchange := range map[string][]transcript.Entry{
			"bash":    toolExchange("a", "bash", map[string]any{"command": "export FOO_TOKEN=" + fakePreviewKey}, 0, 0, 0, false),
			"unknown": toolExchange("a", "futuretool", map[string]any{"api": fakePreviewKey}, 0, 0, 0, false),
		} {
			m := previewMaskMonitor(t, width, toolGroupEntries(exchange))
			out := ansi.Strip(m.itemTranscriptBody())
			for n := 4; n <= len(body); n++ {
				if strings.Contains(out, body[:n]) {
					t.Fatalf("%s at width %d leaked %q:\n%s", name, width, body[:n], out)
				}
			}
		}
	}
	if got := formatArgsInline(map[string]json.RawMessage{"api": json.RawMessage(`"` + fakePreviewKey + `"`)}, 14); strings.Contains(got, body[:4]) {
		t.Fatalf("formatArgsInline leaked a prefix: %q", got)
	}
}

func TestExpandedAndCopyStayRaw(t *testing.T) {
	exchange := toolExchange("a", "bash", map[string]any{"command": "export FOO_TOKEN=" + fakePreviewSecret}, 0, 0, 0, false)
	exchange[1].Blocks[0].Tool.Output, _ = json.Marshal(map[string]any{"text": "FOO_TOKEN=" + fakePreviewSecret})
	entries := toolGroupEntries(exchange)
	m := previewMaskMonitor(t, 120, entries)
	m.RunDir = t.TempDir()
	m.focus = focusTranscript
	if strings.Contains(ansi.Strip(m.itemTranscriptBody()), fakePreviewSecret) {
		t.Fatal("collapsed row should be masked")
	}
	m, _ = m.Update(key("enter"))
	if !m.chatItemExpand[m.chatItems[0].key] {
		t.Fatal("enter did not expand the exchange")
	}
	if !strings.Contains(ansi.Strip(m.itemTranscriptBody()), fakePreviewSecret) {
		t.Fatalf("expanded body lost the raw value:\n%s", ansi.Strip(m.itemTranscriptBody()))
	}
	payload := m.copyTranscriptItemCmd()().(shared.ClipboardRequest).Loader()
	if payload.Err != nil || !strings.Contains(payload.Payload, fakePreviewSecret) {
		t.Fatalf("copy payload lost the raw value: err=%v", payload.Err)
	}
}

func TestArgPreviewNotDoubleMasked(t *testing.T) {
	got := formatArgsInline(map[string]json.RawMessage{"api_key": json.RawMessage(`"` + fakePreviewKey + `"`)}, 80)
	if got != "api_key=<redacted>" || strings.Count(got, "<redacted>") != 1 {
		t.Fatalf("formatArgsInline = %q", got)
	}
}

func TestMaskedBashRowCapture(t *testing.T) {
	dir := os.Getenv("JIG_UI_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set JIG_UI_SNAPSHOT_DIR to capture the masked bash row")
	}
	entries := toolGroupEntries(
		toolExchange("a", "bash", map[string]any{"command": "export FOO_TOKEN=" + fakePreviewSecret}, 0, 0, 0, false),
		toolExchange("b", "edit", map[string]any{"file_path": "config.go"}, 0, 0, 0, false),
	)
	m := newMonitorWithSteps(t).WithTUIConfig(config.TUIConfig{})
	m.RunDir = writeTranscript(t, "a", entries)
	m = enterChatStep(t, m, "a")
	body := proofPlainText(m.chatBody())
	if strings.Contains(body, fakePreviewSecret) {
		t.Fatal("capture leaked the fake secret")
	}
	capture := fmt.Sprintf("Terminal: %dx%d; default config\n\n```text\n%s```\n", m.width, m.height, body)
	if err := os.WriteFile(filepath.Join(dir, "06-task-04-masked-bash.md"), []byte(capture), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownToolTitleMasked(t *testing.T) {
	got := summarizeActivity(&toolcall.Activity{Title: "mcp__srv__" + fakePreviewKey}, 80).action
	if got != "<redacted>" {
		t.Fatalf("action = %q, want <redacted>", got)
	}
}
