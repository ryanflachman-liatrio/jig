package shared

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestRuleGallery captures a deterministic gallery of Rule outputs at
// every width the omp-transcript-parity slice 12 boundary banner will
// consume in practice, so a reviewer can confirm the ruled form, the
// centered label register, and the narrow-panel degradation without
// running the TUI. The gallery is written only when
// JIG_UI_SNAPSHOT_DIR is set; ordinary test runs skip it.
func TestRuleGallery(t *testing.T) {
	dir := os.Getenv("JIG_UI_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set JIG_UI_SNAPSHOT_DIR to capture the boundary-banner gallery")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	labels := []string{
		"reset 2",
		"iteration 2",
		"iteration 12",
		"retry 1",
		"retry 5",
	}
	widths := []int{20, 40, 60, 80, 100}

	var b strings.Builder
	fmt.Fprintln(&b, "Slice 12 boundary-banner gallery")
	fmt.Fprintln(&b, "Widths measured via lipgloss.Width. Every row must equal its declared width or degrade to a bare label.")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "Review widths (transcript inner content): %v\n\n", widths)

	for _, label := range labels {
		fmt.Fprintf(&b, "label=%q\n", label)
		for _, w := range widths {
			row := Rule(w, label)
			plain := stripSGR(row)
			fmt.Fprintf(&b, "  width=%3d cells=%3d plain=%q\n", w, lipgloss.Width(row), plain)
		}
		fmt.Fprintln(&b)
	}

	// Degradation frontier: exercise the width one cell above and one
	// cell below the ruled/bare-label boundary so a reviewer can see
	// exactly where the fallback fires without cross-checking the
	// helper's arithmetic.
	fmt.Fprintln(&b, "Degradation frontier (label='iteration 2', min ruled = 15 cells):")
	for _, w := range []int{14, 15, 16} {
		row := Rule(w, "iteration 2")
		fmt.Fprintf(&b, "  width=%3d cells=%3d plain=%q\n", w, lipgloss.Width(row), stripSGR(row))
	}
	fmt.Fprintln(&b)

	// Empty-label fill: exercised by Rule when a caller wants a bare
	// divider (no consumer inside slice 12, but the helper supports
	// it so slice 13's live-header separator can reuse this file).
	fmt.Fprintln(&b, "Empty label (bare divider):")
	for _, w := range []int{20, 60} {
		row := Rule(w, "")
		fmt.Fprintf(&b, "  width=%3d cells=%3d plain=%q\n", w, lipgloss.Width(row), stripSGR(row))
	}

	if err := os.WriteFile(filepath.Join(dir, "slice-12-boundary-banner-gallery.txt"),
		[]byte(strings.TrimRight(b.String(), "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
