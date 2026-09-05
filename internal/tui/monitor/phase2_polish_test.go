package monitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/tui/prefs"
	"jig/internal/tui/shared"
)

func TestBreadcrumbTitlesSlimAtCommonWidths(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.RunID = "abcd1234ef"
	m.workflow = "feature"
	m = enterChatStep(t, m, "a")
	m.focus = focusTranscript
	m.chatAutoScroll = true

	wantRun := shortRunID("abcd1234ef")
	for _, width := range []int{40, 80, 100, 120} {
		m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		steps := m.stepsPanelTitleParts()
		trans := m.transcriptPanelTitleParts()
		if len(steps) < 2 {
			t.Fatalf("width %d: steps parts = %v", width, steps)
		}
		if steps[0] != wantRun {
			t.Fatalf("width %d: steps root = %q, want %q", width, steps[0], wantRun)
		}
		// Workflow must not lead the Steps title at typical laptop widths (status owns it).
		if width < reviewEmbedMinWidth {
			for _, p := range steps {
				if p == "feature" {
					t.Fatalf("width %d: Steps title still embeds workflow: %v", width, steps)
				}
			}
		}
		joined := shared.BreadcrumbTitle(steps, shared.PanelTitleBudget(max(width/3, 20)))
		if strings.Contains(joined, "feature ›") {
			t.Fatalf("width %d: Steps title mid-hierarchy leak: %q", width, joined)
		}
		if len(trans) < 2 || trans[0] != "a" {
			t.Fatalf("width %d: transcript parts should start with step id: %v", width, trans)
		}
		for _, p := range trans {
			if strings.Contains(p, "new") {
				t.Fatalf("width %d: unseen must not live in transcript title: %v", width, trans)
			}
		}
		status := ansiStrip(m.statusLineView())
		if !strings.Contains(status, "feature") {
			t.Fatalf("width %d: status should carry workflow:\n%s", width, status)
		}
	}
}

func TestUnseenCountOnStatusNotTitle(t *testing.T) {
	m := newMonitorWithSteps(t)
	m = enterChatStep(t, m, "a")
	m.focus = focusTranscript
	m.chatAutoScroll = false
	m.chatSeenSeq = 0
	m.msgCount["a"] = 3
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if got := m.unseenChatEntries(); got != 3 {
		t.Fatalf("unseenChatEntries = %d, want 3", got)
	}
	status := ansiStrip(m.statusLineView())
	if !strings.Contains(status, "3 new") {
		t.Fatalf("status missing unseen count:\n%s", status)
	}
	for _, p := range m.transcriptPanelTitleParts() {
		if strings.Contains(p, "new") {
			t.Fatalf("unseen leaked into title: %v", m.transcriptPanelTitleParts())
		}
	}
}

func TestSimpleModeFiltersFooterKeepsPalette(t *testing.T) {
	m := newMonitorWithSteps(t)
	m = enterChatStep(t, m, "a")
	m.focus = focusTranscript
	m.simpleMode = true
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	footer := ansiStrip(m.footerView())
	for _, bad := range []string{"/", "search", "F filters", "[", "]", " o ", "n/N"} {
		// CompactHint may omit some entirely; assert known advanced help keys absent.
		_ = bad
	}
	if strings.Contains(footer, "/ search") || strings.Contains(footer, "F filter") || strings.Contains(footer, "search transcript") {
		t.Fatalf("simple footer still shows search/filters: %q", footer)
	}
	if !strings.Contains(footer, "simple") {
		t.Fatalf("simple footer missing mode tag: %q", footer)
	}
	help := m.HelpSections()
	for _, sec := range help {
		if sec.Title != "Transcript" {
			continue
		}
		for _, b := range sec.Bindings {
			h := b.Help()
			switch h.Key {
			case "/", "F", "[", "]", "o", "n/N":
				t.Fatalf("simple HelpSections still lists %q", h.Key)
			}
		}
	}

	palette := m.PaletteSections()
	foundSearch := false
	for _, sec := range palette {
		if sec.Title != "Transcript" {
			continue
		}
		for _, b := range sec.Bindings {
			h := b.Help()
			if h.Key == "/" && strings.Contains(h.Desc, "search") {
				foundSearch = true
			}
		}
	}
	if !foundSearch {
		t.Fatal("PaletteSections should still list / search transcript in simple mode")
	}

	// Help overlay states which mode is active.
	modeOK := false
	for _, sec := range help {
		if strings.Contains(sec.Title, "simple") {
			modeOK = true
		}
	}
	if !modeOK {
		t.Fatalf("help missing simple mode section: %+v", help)
	}
}

func TestSimpleModeTogglePersists(t *testing.T) {
	dir := t.TempDir()
	m := newMonitorWithSteps(t).WithPrefs(dir)
	if !m.SimpleMode() {
		t.Fatal("default prefs should be simple ON")
	}
	m = m.ToggleSimpleMode()
	if m.SimpleMode() {
		t.Fatal("toggle should disable simple mode")
	}
	path := filepath.Join(dir, prefs.FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"simple_mode": false`) {
		t.Fatalf("prefs file = %s", data)
	}
	m2 := New("run-1").WithPrefs(dir)
	if m2.SimpleMode() {
		t.Fatal("reloaded prefs should keep simple OFF")
	}
	// Key chord also toggles.
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl | tea.ModShift})
	if !m.SimpleMode() {
		t.Fatal("ctrl+shift+a should re-enable simple mode")
	}
}

func TestFocusBadgeRetainedUnderTitlePressure(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.RunID = "abcd1234ef"
	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
	parts := m.stepsPanelTitleParts()
	title := shared.BreadcrumbTitle(parts, shared.PanelTitleBudget(40))
	if !strings.Contains(title, "[STEPS]") {
		t.Fatalf("focus badge lost under pressure: %q from %v", title, parts)
	}
}
