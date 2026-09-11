package shared

import (
	"testing"

	"charm.land/lipgloss/v2"
)

func TestToolStatusIconStateMapping(t *testing.T) {
	tests := []struct {
		name       string
		state      ToolDisplayState
		wantGlyph  string
		wantStyle  lipgloss.Style
		wantAnyKey bool
	}{
		{name: "success generic", state: ToolDisplaySuccess, wantGlyph: IconStatusSuccess, wantStyle: Theme.Card.BorderSuccess},
		{name: "error", state: ToolDisplayError, wantGlyph: IconStatusError, wantStyle: Theme.Card.BorderError},
		{name: "running", state: ToolDisplayRunning, wantGlyph: IconStatusRunning, wantStyle: Theme.Card.BorderRunning},
		{name: "unknown use", state: ToolDisplayUnknownUse, wantGlyph: IconStatusWarning, wantStyle: Theme.Card.BorderWarning},
		{name: "unknown result", state: ToolDisplayUnknownResult, wantGlyph: IconStatusWarning, wantStyle: Theme.Card.BorderWarning},
		{name: "invalid state falls back to pending", state: ToolDisplayState(99), wantGlyph: IconStatusPending, wantStyle: Theme.Card.BorderPending},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			glyph, style := ToolStatusIcon(tt.state, "")
			if glyph != tt.wantGlyph {
				t.Fatalf("glyph = %q, want %q", glyph, tt.wantGlyph)
			}
			if got, want := style.GetForeground(), tt.wantStyle.GetForeground(); got != want {
				t.Fatalf("style foreground = %v, want %v", got, want)
			}
		})
	}
}

func TestToolStatusIconRunningAndPendingShareGlyph(t *testing.T) {
	// CC-4 anti-jitter: the running/pending transition must not swap glyphs.
	running, _ := ToolStatusIcon(ToolDisplayRunning, "read")
	invalid, _ := ToolStatusIcon(ToolDisplayState(99), "read")
	if running != invalid {
		t.Fatalf("running glyph = %q must equal pending fallback glyph = %q", running, invalid)
	}
}

func TestToolStatusIconSignatureGlyphOnSuccessKnownKind(t *testing.T) {
	tests := []struct {
		kind      string
		wantGlyph string
	}{
		{kind: "read", wantGlyph: IconToolRead},
		{kind: "edit", wantGlyph: IconToolEdit},
		{kind: "write", wantGlyph: IconToolWrite},
		{kind: "notebookedit", wantGlyph: IconToolEdit},
		{kind: "glob", wantGlyph: IconToolSearch},
		{kind: "grep", wantGlyph: IconToolSearch},
		{kind: "bash", wantGlyph: IconToolShell},
		{kind: "websearch", wantGlyph: IconToolWeb},
		{kind: "webfetch", wantGlyph: IconToolWeb},
		{kind: "task", wantGlyph: IconToolAgent},
		{kind: "todowrite", wantGlyph: IconToolTodo},
		{kind: "todoread", wantGlyph: IconToolTodo},
		{kind: "askuserquestion", wantGlyph: IconToolAsk},
		{kind: "skill", wantGlyph: IconToolAgent},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			glyph, _ := ToolStatusIcon(ToolDisplaySuccess, tt.kind)
			if glyph != tt.wantGlyph {
				t.Fatalf("success glyph for kind %q = %q, want %q", tt.kind, glyph, tt.wantGlyph)
			}
		})
	}
}

func TestToolStatusIconSuccessUnknownKindFallsBackToGeneric(t *testing.T) {
	glyph, _ := ToolStatusIcon(ToolDisplaySuccess, "somefuturekind")
	if glyph != IconStatusSuccess {
		t.Fatalf("unknown kind on success = %q, want generic %q", glyph, IconStatusSuccess)
	}
	empty, _ := ToolStatusIcon(ToolDisplaySuccess, "")
	if empty != IconStatusSuccess {
		t.Fatalf("empty kind on success = %q, want generic %q", empty, IconStatusSuccess)
	}
}

func TestToolStatusIconRunningIgnoresKind(t *testing.T) {
	// CC-4: running always shows the pending/running glyph regardless of
	// which kind the exchange will eventually settle to. Only settlement
	// swaps in the signature glyph.
	base, _ := ToolStatusIcon(ToolDisplayRunning, "")
	for _, kind := range []string{"read", "edit", "bash", "unknown"} {
		got, _ := ToolStatusIcon(ToolDisplayRunning, kind)
		if got != base {
			t.Fatalf("running glyph for kind %q = %q, want %q", kind, got, base)
		}
	}
}

func TestToolStatusIconSignatureGlyphsAreSingleCell(t *testing.T) {
	// Cards enforce their width to the cell; a wide (emoji-presentation)
	// signature glyph would break the frame. Reject any that measures >1
	// so slice 14's preset table can substitute in a wider variant later.
	glyphs := []struct {
		name  string
		glyph string
	}{
		{"IconStatusSuccess", IconStatusSuccess},
		{"IconStatusError", IconStatusError},
		{"IconStatusRunning", IconStatusRunning},
		{"IconStatusPending", IconStatusPending},
		{"IconStatusWarning", IconStatusWarning},
		{"IconToolRead", IconToolRead},
		{"IconToolEdit", IconToolEdit},
		{"IconToolWrite", IconToolWrite},
		{"IconToolSearch", IconToolSearch},
		{"IconToolShell", IconToolShell},
		{"IconToolWeb", IconToolWeb},
		{"IconToolAgent", IconToolAgent},
		{"IconToolTodo", IconToolTodo},
		{"IconToolAsk", IconToolAsk},
	}
	for _, g := range glyphs {
		if got := lipgloss.Width(g.glyph); got != 1 {
			t.Fatalf("%s (%q) width = %d, want 1", g.name, g.glyph, got)
		}
	}
}
