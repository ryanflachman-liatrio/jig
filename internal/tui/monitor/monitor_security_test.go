package monitor

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/sentinel"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

func securityNoticeFixture() []sentinel.Finding {
	var findings []sentinel.Finding
	for i := 0; i < 25; i++ {
		findings = append(findings, sentinel.Finding{StepID: "research", Monitor: "operator-allowlist", Severity: sentinel.SeverityLow, Action: sentinel.ActionObserved, Detail: fmt.Sprintf("Remembered approval rule %d", i), Evidence: fmt.Sprintf("operator-config:/fixture/default.rules: entry %d", i)})
	}
	for _, name := range []string{"prompt-injection", "exfil-pattern"} {
		findings = append(findings, sentinel.Finding{StepID: "research", Monitor: "monitor-unavailable/" + name, Severity: sentinel.SeverityLow, Action: sentinel.ActionObserved, Detail: "fixture monitor failure"})
	}
	return findings
}

func TestSecuritySummarySeparatesCoverageWarningsFromDetections(t *testing.T) {
	m := readyDiagModel()
	m.secFindings = securityNoticeFixture()
	view := ansiStrip(m.securityView(5))
	for _, want := range []string{"0 detections", "2 check warnings", "25 remembered approvals", "ctrl+s"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in %s", want, view)
		}
	}
}

func TestSecurityInspectorNavigationKeepsRunFocus(t *testing.T) {
	m := readyDiagModel()
	m.secFindings = securityNoticeFixture()
	m.focus = focusGate
	m, _ = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	view := ansiStrip(m.View())
	if !strings.Contains(view, "Security inspector") {
		t.Fatalf("inspector did not open: %s", view)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if !strings.Contains(ansiStrip(m.View()), "Remembered command approvals") {
		t.Fatal("could not inspect grouped approvals")
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	for i := 0; i < 10; i++ {
		m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	if !strings.Contains(ansiStrip(m.View()), "entry 24") {
		t.Fatal("last grouped approval is inaccessible")
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.focus != focusGate || strings.Contains(ansiStrip(m.View()), "Security inspector") {
		t.Fatal("closing inspector disturbed underlying focus")
	}
}

func TestSecurityInspectorFitsResize(t *testing.T) {
	for _, size := range [][2]int{{100, 30}, {40, 12}, {8, 4}, {1, 1}} {
		m := readyDiagModel()
		m.secFindings = securityNoticeFixture()
		m, _ = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
		m, _ = m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		if view := m.View(); lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
			t.Fatalf("inspector exceeds %v", size)
		}
	}
}

func TestSecurityInspectorLoadsHistoryAndCopiesDetails(t *testing.T) {
	dir := t.TempDir()
	writer, err := sentinel.NewWriter(dir + "/findings.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range securityNoticeFixture() {
		if err := writer.Append(finding); err != nil {
			t.Fatal(err)
		}
	}
	_ = writer.Close()
	m := readyDiagModel()
	m.RunDir = dir
	m, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("opening inspector did not request saved findings")
	}
	m, _ = m.Update(cmd())
	if len(m.secFindings) != 27 {
		t.Fatalf("loaded %d findings", len(m.secFindings))
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m, cmd = m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("copy command missing")
	}
	req, ok := cmd().(shared.ClipboardRequest)
	if !ok || !strings.Contains(req.Loader().Payload, "entry 24") {
		t.Fatal("copy omitted grouped approval details")
	}
	// A late read from another run must not replace the current run's findings.
	m, _ = m.Update(SecurityLoadedMsg{runID: "old-run", runDir: dir})
	if len(m.secFindings) != 27 {
		t.Fatal("stale load replaced current run")
	}
}

func TestSecurityInspectorDoesNotStealTextOrPassMouseThrough(t *testing.T) {
	m := readyDiagModel()
	m.searchOpen = true
	m, _ = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if m.securityOpen {
		t.Fatal("inspector stole input while typing")
	}
	m.searchOpen = false
	m, _ = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if !m.mouseExcluded() {
		t.Fatal("mouse can reach panels under the inspector")
	}
	var found bool
	m.securityOpen = false
	for _, section := range m.PaletteSections() {
		for _, binding := range section.Bindings {
			if binding.Help().Desc == "security inspector" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("inspector absent from command palette")
	}
}

func TestSecurityFindingVisibleWithoutPersistence(t *testing.T) {
	m := readyDiagModel()
	m, _ = m.Update(EngineEventMsg{Event: engine.SecurityFinding{RunID: m.RunID, StepID: "step", Monitor: "monitor-unavailable/prompt-injection", Severity: "low", Action: "observed"}})
	if len(m.secFindings) != 1 {
		t.Fatal("persistence-off finding was lost")
	}
	if !strings.Contains(m.securityDetails(), "Details are unavailable") {
		t.Fatal("missing details should be explicit")
	}
}
