package monitor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"jig/internal/engine"
	domainreview "jig/internal/review"
	"jig/internal/step"
	reviewworkspace "jig/internal/tui/review"
)

func monitorWithReviewWorkspace(t *testing.T) Model {
	t.Helper()
	m := newMonitorWithSteps(t)
	content := "The scope assessment is one long paragraph that wraps visually in the terminal."
	m, _ = m.Update(EngineEventMsg{Event: engine.ReviewRequest{
		RunID:   "run-1",
		StepID:  "a",
		RoundID: "g000-i000",
		Choices: []string{"approve", "revise"},
		Documents: []domainreview.Document{{
			ID:        "scope",
			Label:     "Scope assessment",
			Source:    "scope.md",
			Format:    "markdown",
			Content:   content,
			SHA256:    domainreview.Digest(content),
			LineCount: 1,
		}},
	}})
	m.focus = focusGate
	if entry, ok := m.activeEntry(); !ok || entry.workspace == nil {
		t.Fatal("review request did not create a workspace")
	}
	return m
}

func TestReviewWorkspaceComposerReceivesGateKeysAndIsVisible(t *testing.T) {
	m := monitorWithReviewWorkspace(t)
	m, _ = m.Update(key("enter"))
	if footer := ansiStrip(m.footerView()); !strings.Contains(footer, "j/k move block") || !strings.Contains(footer, "s view") || !strings.Contains(footer, "S finish review") || strings.Contains(footer, "1-9 decision") {
		t.Fatalf("workspace footer exposes the wrong controls: %q", footer)
	}
	if view := ansiStrip(m.View()); strings.Count(view, "Review required") != 1 || strings.Contains(view, "Documents\n") && strings.Contains(view, "Steps") {
		t.Fatalf("workspace is still nested over the Monitor panels:\n%s", view)
	}
	m, _ = m.Update(key("r"))
	if !m.inputQueue[0].workspace.Reviewed("scope") {
		t.Fatal("workspace key did not mark the document reviewed")
	}
	m, _ = m.Update(key("c"))
	if got := m.inputQueue[0].workspace.Mode(); got != reviewworkspace.ModeComposeComment {
		t.Fatalf("c set mode = %v, want compose", got)
	}

	m, _ = m.Update(key("x"))
	if view := ansiStrip(m.View()); !strings.Contains(view, "x") {
		t.Fatalf("typed comment is not visible in workspace:\n%s", view)
	}

	beforeKind := m.inputQueue[0].workspace.Draft().View.CommentKind
	m, _ = m.Update(key("tab"))
	if m.focus != focusGate {
		t.Fatalf("tab escaped the workspace to focus %v", m.focus)
	}
	afterKind := m.inputQueue[0].workspace.Draft().View.CommentKind
	if afterKind == beforeKind {
		t.Fatalf("tab did not cycle comment kind from %q", beforeKind)
	}
}

func TestReviewWorkspaceEscapeCancelsEditorBeforeBlurringGate(t *testing.T) {
	m := monitorWithReviewWorkspace(t)
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(key("c"))
	m, _ = m.Update(key("esc"))
	if m.focus != focusGate {
		t.Fatalf("editor escape moved focus to %v, want gate", m.focus)
	}
	if got := m.inputQueue[0].workspace.Mode(); got != reviewworkspace.ModeBrowse {
		t.Fatalf("editor escape left mode %v, want browse", got)
	}
	m, _ = m.Update(key("esc"))
	if m.focus != focusGate || strings.Contains(ansiStrip(m.gateOverlay()), "[ PREVIEW ]") {
		t.Fatalf("browse escape should return to the compact gate: focus=%v", m.focus)
	}
}

func TestReviewWorkspaceSummaryShowsDecisionsAndSubmissionHelp(t *testing.T) {
	m := monitorWithReviewWorkspace(t)
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(key("S"))

	view := ansiStrip(m.View())
	for _, want := range []string{"Decision", "[1] approve", "[2] revise"} {
		if !strings.Contains(view, want) {
			t.Fatalf("review summary missing %q:\n%s", want, view)
		}
	}
	footer := ansiStrip(m.footerView())
	for _, want := range []string{"1-9 choose decision", "enter submit review"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("review summary footer missing %q: %q", want, footer)
		}
	}
}

func TestReviewWorkspaceSubmitsNarrowDecisionWithComment(t *testing.T) {
	m := monitorWithReviewWorkspace(t)
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(key("r"))
	m, _ = m.Update(key("c"))
	m, _ = m.Update(key("x"))
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(key("S"))
	m, _ = m.Update(key("2"))
	m, cmd := m.Update(key("enter"))

	var submission ReviewSubmissionMsg
	for _, msg := range runBatch(cmd) {
		next, nextCmd := m.Update(msg)
		m = next
		if nextCmd == nil {
			continue
		}
		if routed, ok := nextCmd().(ReviewSubmissionMsg); ok {
			submission = routed
		}
	}
	if submission.Submission.Verdict != "revise" {
		t.Fatalf("submitted decision = %q, want revise", submission.Submission.Verdict)
	}
	if len(submission.Submission.Comments) != 1 || submission.Submission.Comments[0].Body != "x" {
		t.Fatalf("submitted comments = %#v, want one comment", submission.Submission.Comments)
	}
}

func TestReviewWorkspaceStartsCompactAndReplacesMonitorBodyWhenOpened(t *testing.T) {
	m := monitorWithReviewWorkspace(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 180, Height: 45})
	compact := ansiStrip(m.gateOverlay())
	if strings.Contains(compact, "╭─ Documents") || !strings.Contains(compact, "enter") {
		t.Fatalf("review gate is not compact before opening:\n%s", compact)
	}

	m, _ = m.Update(key("enter"))
	view := ansiStrip(m.View())
	if !strings.Contains(view, "Scope assessment") || !strings.Contains(view, "[ PREVIEW ]") {
		t.Fatalf("dedicated workspace did not open:\n%s", view)
	}
	if strings.Contains(view, "Transcript") || strings.Count(view, "Review required") != 1 {
		t.Fatalf("workspace was layered over the Monitor instead of replacing its body:\n%s", view)
	}
	if width, height := lipgloss.Width(m.View()), lipgloss.Height(m.View()); width > 180 || height > 45 {
		t.Fatalf("workspace exceeds terminal: got %dx%d, max 180x45", width, height)
	}
}

func TestReviewWorkspaceRoutesViewToggleAndFitsTargetSizes(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 40}, {Width: 80, Height: 24}} {
		m := monitorWithReviewWorkspace(t)
		m, _ = m.Update(size)
		m, _ = m.Update(key("enter"))
		if view := ansiStrip(m.View()); !strings.Contains(view, "[ PREVIEW ]") {
			t.Fatalf("%dx%d workspace did not default to preview:\n%s", size.Width, size.Height, view)
		}
		m, _ = m.Update(key("s"))
		if view := ansiStrip(m.View()); !strings.Contains(view, "[ SOURCE ]") {
			t.Fatalf("%dx%d s did not reach child workspace:\n%s", size.Width, size.Height, view)
		}
		section := m.gateHelpSection()
		foundView := false
		for _, binding := range section.Bindings {
			help := binding.Help()
			if help.Key == "s" && help.Desc == "view" {
				foundView = true
			}
		}
		if !foundView {
			t.Fatalf("%dx%d global help does not reflect child view binding", size.Width, size.Height)
		}
		if width, height := lipgloss.Width(m.View()), lipgloss.Height(m.View()); width > size.Width || height > size.Height {
			t.Fatalf("workspace exceeds %dx%d: got %dx%d", size.Width, size.Height, width, height)
		}
	}
}

func TestReplayedReviewIsExplicitlyReadOnly(t *testing.T) {
	content := "Historical scope assessment"
	m := New("old-run").WithJournal([]engine.Event{
		engine.RunStarted{RunID: "old-run", Workflow: "sdd-spec", Steps: []string{"scope_gate"}},
		engine.StepStatus{RunID: "old-run", StepID: "scope_gate", To: step.StatusAwaitingReview},
		engine.ReviewRequest{
			RunID: "old-run", StepID: "scope_gate", RoundID: "g000-i000",
			Choices: []string{"proceed", "narrow"},
			Documents: []domainreview.Document{{
				ID: "scope", Label: "Scope assessment", Format: "markdown",
				Content: content, SHA256: domainreview.Digest(content), LineCount: 1,
			}},
		},
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.focus = focusGate

	if view := ansiStrip(m.gateOverlay()); !strings.Contains(view, "original jig scheduler") || !strings.Contains(view, "press R to resume") {
		t.Fatalf("replayed review does not explain why it cannot be submitted:\n%s", view)
	}
	m, _ = m.Update(key("enter"))
	if m.reviewOpen {
		t.Fatal("replayed review opened an editable workspace")
	}
}
