package monitor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	domainreview "jig/internal/review"
	reviewworkspace "jig/internal/tui/review"
	"jig/internal/tui/shared"
)

// waitForRequest drains a batched command and returns the first
// shared.ClipboardRequest it produces. Ignores nil results and
// DraftChangedMsg so the caller can assert precise routing.
func waitForRequest(t *testing.T, cmd tea.Cmd) shared.ClipboardRequest {
	t.Helper()
	if cmd == nil {
		t.Fatal("nil cmd")
	}
	msgs := runBatch(cmd)
	for _, msg := range msgs {
		if req, ok := msg.(shared.ClipboardRequest); ok {
			return req
		}
	}
	// Some cmds return a single value; check the single-message path too.
	msg := cmd()
	if req, ok := msg.(shared.ClipboardRequest); ok {
		return req
	}
	t.Fatalf("no ClipboardRequest in %#v", msgs)
	return shared.ClipboardRequest{}
}

// missingDraftMsg drains a batched command and returns true if the batch
// contains a DraftChangedMsg (which copy should NEVER trigger).
func batchHasDraftChange(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	for _, msg := range runBatch(cmd) {
		if _, ok := msg.(reviewworkspace.DraftChangedMsg); ok {
			return true
		}
	}
	return false
}

func TestReviewWorkspaceCopyItemForwardsRequestWithoutDraft(t *testing.T) {
	m := monitorWithReviewWorkspace(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(key("enter"))
	// Toggle to source mode so `y` copies a line (Markdown default is preview).
	m, _ = m.Update(key("s"))
	beforeDraft := m.inputQueue[0].workspace.Draft()
	m, cmd := m.Update(key("y"))
	if batchHasDraftChange(cmd) {
		t.Fatal("copy item emitted DraftChangedMsg — draft would have been persisted")
	}
	req := waitForRequest(t, cmd)
	if req.Target.Surface != shared.ClipboardSurfaceReviewLine {
		t.Fatalf("target surface = %q, want %q", req.Target.Surface, shared.ClipboardSurfaceReviewLine)
	}
	// Draft state must not have moved.
	afterDraft := m.inputQueue[0].workspace.Draft()
	if afterDraft.View.CursorLine != beforeDraft.View.CursorLine ||
		afterDraft.View.ActiveDocumentID != beforeDraft.View.ActiveDocumentID {
		t.Fatalf("copy mutated draft view: before=%+v after=%+v", beforeDraft.View, afterDraft.View)
	}
}

func TestReviewWorkspaceCopyAllForwardsRequestWithoutDraft(t *testing.T) {
	m := monitorWithReviewWorkspace(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(key("enter"))
	beforeDraft := m.inputQueue[0].workspace.Draft()
	m, cmd := m.Update(key("Y"))
	if batchHasDraftChange(cmd) {
		t.Fatal("copy all emitted DraftChangedMsg — draft would have been persisted")
	}
	req := waitForRequest(t, cmd)
	if req.Target.Surface != shared.ClipboardSurfaceReviewDocument {
		t.Fatalf("target surface = %q, want %q", req.Target.Surface, shared.ClipboardSurfaceReviewDocument)
	}
	afterDraft := m.inputQueue[0].workspace.Draft()
	if afterDraft.View.CursorLine != beforeDraft.View.CursorLine {
		t.Fatalf("copy mutated draft view: before=%+v after=%+v", beforeDraft.View, afterDraft.View)
	}
}

func TestReviewWorkspaceCopyRespectsComposerCapture(t *testing.T) {
	m := monitorWithReviewWorkspace(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(key("c"))
	if got := m.inputQueue[0].workspace.Mode(); got != reviewworkspace.ModeComposeComment {
		t.Fatalf("mode = %v, want compose", got)
	}
	m, cmd := m.Update(key("y"))
	// While composing, "y" is literal comment text — NOT a copy dispatch.
	for _, msg := range runBatch(cmd) {
		if _, ok := msg.(shared.ClipboardRequest); ok {
			t.Fatal("copy fired while composer captured text")
		}
	}
	body := m.inputQueue[0].workspace.Draft().Comments
	if len(body) != 0 {
		t.Fatalf("compose was submitted unexpectedly: %#v", body)
	}
	// Composer text should have received the y keystroke.
	view := ansiStrip(m.View())
	if !strings.Contains(view, "New comment") {
		t.Fatalf("composer overlay missing:\n%s", view)
	}
}

func TestReviewWorkspaceCopyOnDiffCurrentFile(t *testing.T) {
	m := monitorWithDiffReviewWorkspace(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(key("enter"))
	// Cursor is on hunk 1's header line 4 by default.
	m, cmd := m.Update(key("Y"))
	if batchHasDraftChange(cmd) {
		t.Fatal("copy all on diff emitted DraftChangedMsg")
	}
	req := waitForRequest(t, cmd)
	if req.Target.Surface != shared.ClipboardSurfaceReviewFileDiff {
		t.Fatalf("target = %#v, want file diff", req.Target)
	}
	payload := req.Loader()
	if payload.Err != nil {
		t.Fatalf("payload err = %v", payload.Err)
	}
	if !strings.HasPrefix(payload.Payload, "diff --git a/file.txt") {
		t.Fatalf("payload = %q, want full file diff", payload.Payload)
	}
}

func TestReviewWorkspaceCopyOnDiffCurrentHunk(t *testing.T) {
	m := monitorWithDiffReviewWorkspace(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(key("enter"))
	m, cmd := m.Update(key("y"))
	if batchHasDraftChange(cmd) {
		t.Fatal("copy item on diff emitted DraftChangedMsg")
	}
	req := waitForRequest(t, cmd)
	if req.Target.Surface != shared.ClipboardSurfaceReviewHunk {
		t.Fatalf("target = %#v, want hunk", req.Target)
	}
	payload := req.Loader()
	if payload.Err != nil {
		t.Fatalf("payload err = %v", payload.Err)
	}
	if !strings.HasPrefix(payload.Payload, "@@ ") || !strings.Contains(payload.Payload, "-old one") {
		t.Fatalf("hunk payload = %q", payload.Payload)
	}
}

func TestReviewWorkspaceCopyClosedGateIsNotDispatched(t *testing.T) {
	m := monitorWithReviewWorkspace(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	// Do NOT open the workspace. Pressing y/Y at the compact gate should not
	// dispatch a review copy (there is no workspace focus for it to bind to).
	_, cmd := m.Update(key("y"))
	for _, msg := range runBatch(cmd) {
		if _, ok := msg.(shared.ClipboardRequest); ok {
			t.Fatal("copy fired on closed review gate")
		}
	}
}

func TestReviewWorkspaceCopyDoesNotEmitSubmission(t *testing.T) {
	m := monitorWithReviewWorkspace(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(key("enter"))
	_, cmd := m.Update(key("Y"))
	// The batch may contain a ClipboardRequest but must not contain a
	// SubmissionMsg or a ReviewSubmissionMsg.
	for _, msg := range runBatch(cmd) {
		switch msg.(type) {
		case reviewworkspace.SubmissionMsg, ReviewSubmissionMsg:
			t.Fatalf("copy emitted %T", msg)
		}
	}
}

func TestReviewWorkspaceCopyHelpBindingsAppearInGateSection(t *testing.T) {
	m := monitorWithReviewWorkspace(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(key("enter"))
	got := map[string]string{}
	for _, b := range m.gateHelpSection().Bindings {
		got[b.Help().Key] = b.Help().Desc
	}
	if desc, ok := got["y"]; !ok || !strings.Contains(desc, "copy") {
		t.Fatalf("y binding = %q, want a copy label", got["y"])
	}
	if desc, ok := got["Y"]; !ok || !strings.Contains(desc, "copy") {
		t.Fatalf("Y binding = %q, want a copy label", got["Y"])
	}
}

// TestReviewWorkspaceCopyBetweenTwoDocumentsUsesCurrent verifies target label
// tracks the active document after navigation. A second navigation between
// documents must not carry the previous document's identity.
func TestReviewWorkspaceCopyBetweenTwoDocumentsUsesCurrent(t *testing.T) {
	m := newMonitorWithSteps(t)
	first := "one\ntwo\nthree"
	second := "alpha\nbeta"
	m, _ = m.Update(EngineEventMsg{Event: engine.ReviewRequest{
		RunID: "run-1", StepID: "a", RoundID: "g000-i000",
		Choices: []string{"approve", "revise"},
		Documents: []domainreview.Document{
			{ID: "d1", Label: "Doc One", Source: "one.txt", Format: "text", Content: first, SHA256: domainreview.Digest(first)},
			{ID: "d2", Label: "Doc Two", Source: "two.txt", Format: "text", Content: second, SHA256: domainreview.Digest(second)},
		},
	}})
	m.focus = focusGate
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(key("enter"))
	// Move to second document.
	m, _ = m.Update(key("}"))
	_, cmd := m.Update(key("Y"))
	req := waitForRequest(t, cmd)
	if !strings.Contains(req.Target.Label, "Doc Two") {
		t.Fatalf("copy target = %q, want Doc Two", req.Target.Label)
	}
}
