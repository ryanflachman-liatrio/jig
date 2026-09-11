package monitor

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
)

func monitorClick(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func TestMonitorMouseSelectsVariableHeightRows(t *testing.T) {
	t.Run("ordinary and fan-out child", func(t *testing.T) {
		m := newMonitorWithFamily(t, 2)
		m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 35})
		m.expanded["b"] = true
		m.refreshPanels()
		panels := m.mousePanels()
		ranges := m.stepRowRanges()
		child := 2
		m.focus = focusTranscript
		m, cmd := m.Update(monitorClick(panels.steps.x, panels.steps.y+ranges[child].start-m.vp.YOffset()))
		if cmd != nil || m.focus != focusSteps || m.cursor != child || m.cursorStepID() != m.familyChildren["b"][0] {
			t.Fatalf("child click focus/cursor/step/cmd = %v/%d/%q/%v", m.focus, m.cursor, m.cursorStepID(), cmd)
		}
	})

	t.Run("file row", func(t *testing.T) {
		m := newMonitorWithSteps(t)
		m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 35})
		m.expanded["a"] = true
		m.stepFiles["a"] = []outputFile{{name: "proof.txt", path: "/synthetic/proof.txt", kind: kindOther}}
		m.refreshPanels()
		panels := m.mousePanels()
		ranges := m.stepRowRanges()
		m, cmd := m.Update(monitorClick(panels.steps.x, panels.steps.y+ranges[1].start))
		if cmd != nil || m.cursor != 1 || m.selKind != "file" || m.selFile != "/synthetic/proof.txt" {
			t.Fatalf("file click cursor/kind/file/cmd = %d/%q/%q/%v", m.cursor, m.selKind, m.selFile, cmd)
		}
	})

	t.Run("nonzero viewport offset", func(t *testing.T) {
		m := New("run-many")
		steps := make([]string, 20)
		for i := range steps {
			steps[i] = fmt.Sprintf("step-%02d", i)
		}
		m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 12})
		m, _ = m.Update(EngineEventMsg{Event: engine.RunStarted{RunID: "run-many", Workflow: "demo", Steps: steps}})
		m.vp.SetYOffset(8)
		panels := m.mousePanels()
		m, _ = m.Update(monitorClick(panels.steps.x, panels.steps.y))
		if m.cursor != 4 {
			t.Fatalf("click at viewport line 8 selected row %d, want 4", m.cursor)
		}
	})
}

func TestMonitorMouseRoutesWheelAndTranscriptFocus(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 18})
	panels := m.mousePanels()
	m.focus = focusTranscript
	m, _ = m.Update(tea.MouseWheelMsg{X: panels.steps.x, Y: panels.steps.y, Button: tea.MouseWheelDown})
	if m.cursor != 2 || m.focus != focusTranscript {
		t.Fatalf("Steps wheel cursor/focus = %d/%v, want 2/transcript", m.cursor, m.focus)
	}

	m.chatVP.SetContent(strings.Repeat("line\n", 100))
	m.chatVP.GotoTop()
	m.chatAutoScroll = false
	m.focus = focusSteps
	m, _ = m.Update(tea.MouseWheelMsg{X: panels.transcript.x, Y: panels.transcript.y, Button: tea.MouseWheelDown})
	if m.chatVP.YOffset() != 3 || m.focus != focusSteps {
		t.Fatalf("Transcript wheel offset/focus = %d/%v, want 3/steps; panels=%+v size=%dx%d", m.chatVP.YOffset(), m.focus, panels, m.width, m.height)
	}
	offset := m.chatVP.YOffset()
	m, _ = m.Update(monitorClick(panels.transcript.x, panels.transcript.y))
	if m.focus != focusTranscript || m.chatVP.YOffset() != offset || m.cursor != 2 {
		t.Fatalf("Transcript click changed more than focus: focus=%v offset=%d cursor=%d", m.focus, m.chatVP.YOffset(), m.cursor)
	}
}

func TestMonitorMouseNarrowOnlyTargetsRenderedPanel(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m.focus = focusSteps
	panels := m.mousePanels()
	if !panels.stepsVisible || panels.transcriptVisible {
		t.Fatalf("narrow Steps visibility = %+v", panels)
	}
	m.chatVP.SetContent(strings.Repeat("line\n", 100))
	m, _ = m.Update(tea.MouseWheelMsg{X: 40, Y: panels.steps.y, Button: tea.MouseWheelDown})
	if m.cursor != 2 || m.chatVP.YOffset() != 0 {
		t.Fatalf("hidden transcript received input: cursor=%d offset=%d", m.cursor, m.chatVP.YOffset())
	}
}

func TestMonitorMouseUnsupportedAndInvalidAreNoOps(t *testing.T) {
	m := newMonitorWithSteps(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	before := m.cursor
	for _, msg := range []tea.MouseMsg{
		tea.MouseMotionMsg{X: 1, Y: 1},
		tea.MouseReleaseMsg{X: 1, Y: 1, Button: tea.MouseLeft},
		tea.MouseClickMsg{X: 1, Y: 3, Button: tea.MouseRight},
		tea.MouseClickMsg{X: 1, Y: 3, Button: tea.MouseLeft, Mod: tea.ModShift},
		tea.MouseWheelMsg{X: 1, Y: 3, Button: tea.MouseWheelLeft},
		tea.MouseWheelMsg{X: -1, Y: 3, Button: tea.MouseWheelDown},
		tea.MouseWheelMsg{X: 120, Y: 3, Button: tea.MouseWheelDown},
	} {
		m, _ = m.Update(msg)
	}
	if m.cursor != before {
		t.Fatalf("unsupported input moved cursor from %d to %d", before, m.cursor)
	}
}

func TestMonitorMouseExcludedInteractionSurfaces(t *testing.T) {
	base := newMonitorWithSteps(t)
	base, _ = base.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	panels := base.mousePanels()
	event := tea.MouseWheelMsg{X: panels.steps.x, Y: panels.steps.y, Button: tea.MouseWheelDown}

	cases := []struct {
		name  string
		setup func(Model) Model
	}{
		{name: "helpchat", setup: func(m Model) Model { m.helpOpen = true; return m }},
		{name: "diagnostics", setup: func(m Model) Model { m.showDiagnostics = true; return m }},
		{name: "review workspace", setup: func(m Model) Model { m.reviewOpen = true; return m }},
		{name: "transcript search", setup: func(m Model) Model { m.searchOpen = true; return m }},
		{name: "focused gate", setup: func(m Model) Model {
			m.inputQueue = append(m.inputQueue, pendingInputEntry{kind: inputKindFinalMerge})
			m.focus = focusGate
			return m
		}},
		{name: "prompt editor", setup: func(m Model) Model {
			m.inputQueue = append(m.inputQueue, pendingInputEntry{kind: inputKindPrompt})
			m.focus = focusGate
			return m
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.setup(base)
			before := m.cursor
			m, cmd := m.Update(event)
			if cmd != nil || m.cursor != before {
				t.Fatalf("excluded mouse changed cursor/cmd = %d/%v", m.cursor, cmd)
			}
		})
	}

	t.Run("pending but unfocused gate allows panel navigation", func(t *testing.T) {
		m := base
		m.inputQueue = append(m.inputQueue, pendingInputEntry{kind: inputKindFinalMerge})
		m.focus = focusSteps
		m, _ = m.Update(event)
		if m.cursor != 2 {
			t.Fatalf("pending unfocused gate blocked Steps wheel: cursor=%d", m.cursor)
		}
	})
}
