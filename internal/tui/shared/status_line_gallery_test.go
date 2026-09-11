package shared

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestStatusLineGallery captures a deterministic gallery of every state ×
// kind × slot combination this slice ships (FR-02.1 through FR-02.10).
// Widths are recorded alongside each row so a reviewer can confirm that
// slice 14's future preset table can substitute wider glyphs without
// changing the row grammar. The gallery is written only when
// JIG_UI_SNAPSHOT_DIR is set; ordinary test runs skip this test.
func TestStatusLineGallery(t *testing.T) {
	dir := os.Getenv("JIG_UI_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set JIG_UI_SNAPSHOT_DIR to capture the status-line gallery")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	states := []struct {
		name  string
		state ToolDisplayState
	}{
		{"success", ToolDisplaySuccess},
		{"error", ToolDisplayError},
		{"running", ToolDisplayRunning},
		{"warning-use", ToolDisplayUnknownUse},
		{"warning-result", ToolDisplayUnknownResult},
	}
	kinds := []struct {
		kind  string
		title string
		desc  string
	}{
		{"read", "Read", "cmd/jig/main.go"},
		{"edit", "Edit", "internal/tui/shared/status_line.go"},
		{"write", "Write", "internal/tui/monitor/monitor_transcript_items_view.go"},
		{"grep", "Search", "IconStatus"},
		{"bash", "Run", "go test ./..."},
		{"websearch", "Search web", "synthetic release notes"},
		{"webfetch", "Fetch", "example.invalid"},
		{"task", "Explore", "monitor state transitions"},
		{"todowrite", "Update", "3 tasks"},
		{"askuserquestion", "Ask", "How should this behave?"},
		{"", "Custom Tool", "synthetic argument"},
	}
	widths := []int{40, 60, 90}

	var b strings.Builder
	fmt.Fprintln(&b, "Slice 02 status-line gallery")
	fmt.Fprintln(&b, "Widths recorded via lipgloss.Width. Empty kind falls back to the generic success glyph on settled success.")
	fmt.Fprintln(&b)

	// Widths are recorded per row; the widths slice documents the review
	// sizes the enclosing card frame will be asked to fit (slice 01's
	// truncation happens outside this primitive). The gallery is
	// width-independent because RenderStatusLine never truncates (FR-02.4).
	fmt.Fprintf(&b, "Review widths (card frame): %v\n\n", widths)
	for _, st := range states {
		for _, k := range kinds {
			glyph, iconStyle := ToolStatusIcon(st.state, k.kind)
			line := RenderStatusLine(StatusLine{
				Icon:        glyph,
				IconStyle:   iconStyle,
				Title:       k.title,
				Description: k.desc,
				Meta:        gallerySlotMeta(st.state),
			})
			plain := stripSGR(line)
			fmt.Fprintf(&b, "state=%-14s kind=%-15s width=%2d plain=%q\n", st.name, k.kind, lipgloss.Width(line), plain)
		}
		fmt.Fprintln(&b)
	}

	if err := os.WriteFile(filepath.Join(dir, "25-task-1-status-line-gallery.txt"), []byte(strings.TrimRight(b.String(), "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gallerySlotMeta(state ToolDisplayState) []string {
	if state == ToolDisplayError {
		return []string{"synthetic hint: permission denied"}
	}
	return nil
}

// stripSGR removes ANSI CSI sequences so gallery rows are recorded as plain
// text next to their measured cell width. This avoids leaking terminal
// escapes into a text artifact that reviewers may open in a plain editor.
func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' && s[j] != 'K' {
				j++
			}
			if j < len(s) {
				i = j
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
