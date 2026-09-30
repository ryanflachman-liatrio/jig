package monitor

// Spec 05 Unit 2 visual proof: a Monitor frame with synthetic finalized items
// and the live block streaming below them, ending in the typing cursor. The
// .ansi/.html captures and a stripped .txt are written to JIG_UI_SNAPSHOT_DIR.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

func TestTranscriptLiveBlockVisualProof(t *testing.T) {
	dir := os.Getenv("JIG_UI_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set JIG_UI_SNAPSHOT_DIR to capture the live-block Monitor frame")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	m := newLiveModel(t, liveEntries(3))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	for _, d := range []string{"Reviewing the synthetic change set.\n", "Next I will check ", "the **half-typed", " markdown"} {
		m = sendDelta(m, "a", d)
	}
	m = flushFrame(m)
	view := m.View()

	plain := ansi.Strip(view)
	if !strings.Contains(plain, "the **half-typed markdown"+shared.LiveCursor) {
		t.Fatalf("frame is missing the live text and cursor:\n%s", plain)
	}
	if !strings.Contains(plain, "finalized note 03") {
		t.Fatalf("frame is missing the finalized items:\n%s", plain)
	}
	base := "05-task-02-live-block"
	for name, data := range map[string]string{
		base + ".ansi": view,
		base + ".html": terminalHTML(view),
		base + ".txt":  plain,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
