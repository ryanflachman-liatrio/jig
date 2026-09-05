package shared_test

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"jig/internal/tui/shared"
)

func TestFocusTitle(t *testing.T) {
	if got := shared.FocusTitle("Steps", true); got != "[STEPS]" {
		t.Fatalf("focused = %q", got)
	}
	if got := shared.FocusTitle("Steps", false); got != "Steps" {
		t.Fatalf("blurred = %q", got)
	}
	if !shared.IsFocusBadge("[GATE]") || shared.IsFocusBadge("GATE") {
		t.Fatal("IsFocusBadge mismatch")
	}
}

func TestBreadcrumbTitleKeepsFocusBadge(t *testing.T) {
	const maxWidth = 22
	got := shared.BreadcrumbTitle(
		[]string{"a1b2c3d4 · very-long-workflow-name", "[STEPS]"},
		maxWidth,
	)
	if lipgloss.Width(got) > maxWidth {
		t.Fatalf("width = %d > %d: %q", lipgloss.Width(got), maxWidth, got)
	}
	if !strings.Contains(got, "[STEPS]") {
		t.Fatalf("truncated breadcrumb dropped badge: %q", got)
	}
}

func TestRenderEmptyState(t *testing.T) {
	out := shared.RenderEmptyState(shared.EmptyState{
		Title: "No runs yet.",
		Body:  "Start a run from this list.",
		CTA:   "r  start a run",
	})
	for _, want := range []string{"No runs yet.", "Start a run", "r  start a run"} {
		if !strings.Contains(out, want) {
			t.Fatalf("empty state missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(strings.ToLower(out), "detail") {
		t.Fatalf("empty state must not mention Detail:\n%s", out)
	}
}
