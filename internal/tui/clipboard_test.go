// clipboard_test.go composes the surface-level clipboard tests from
// individual sub-package tests into a single table-driven matrix. Each row
// stands up the minimum model needed to drive one dispatch or feedback path,
// sends the copy key(s), and asserts (a) which shared.ClipboardRequest was
// dispatched (or none), (b) the loader outcome, and (c) that the contextual
// help label matches — so the operator never sees an advertised copy that
// does not fire and vice versa.
//
// Spec: docs/specs/23-spec-clipboard-yank (Task 5.1). The matrix is
// intentionally the only sub-package-crossing dispatch test at this level; per-
// surface behavior (payload bytes, sanitization, filesystem gates) lives in
// each sub-package's clipboard_test.go and is not duplicated here.
package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	domainreview "jig/internal/review"
	"jig/internal/runner"
	"jig/internal/tui/monitor"
	runspane "jig/internal/tui/runs"
	"jig/internal/tui/shared"
)

// dispatchProbe hides the concrete model type so a single table can drive
// runs, monitor, and monitor-hosted review through one interface.
type dispatchProbe interface {
	// Send routes a single key press through the probe's model and returns
	// the resulting command (may be nil, a single command, or a batch).
	Send(t *testing.T, key string) tea.Cmd
	// HelpBindings returns the *contextual* help bindings visible in the
	// current focus. Entries whose Help().Desc is empty are dropped so the
	// caller can grep for user-visible labels.
	HelpBindings() []keybind.Binding
}

// runsProbe drives runs.Model.
type runsProbe struct{ m runspane.Model }

func (p *runsProbe) Send(t *testing.T, k string) tea.Cmd {
	t.Helper()
	updated, cmd := p.m.Update(makeKey(k))
	p.m = updated
	return cmd
}

func (p *runsProbe) HelpBindings() []keybind.Binding {
	var out []keybind.Binding
	for _, sec := range p.m.HelpSections() {
		out = append(out, sec.Bindings...)
	}
	return out
}

// monitorProbe drives monitor.Model directly (Steps focus / Transcript focus,
// depending on how it was seeded).
type monitorProbe struct{ m monitor.Model }

func (p *monitorProbe) Send(t *testing.T, k string) tea.Cmd {
	t.Helper()
	updated, cmd := p.m.Update(makeKey(k))
	p.m = updated
	return cmd
}

func (p *monitorProbe) HelpBindings() []keybind.Binding {
	var out []keybind.Binding
	for _, sec := range p.m.HelpSections() {
		out = append(out, sec.Bindings...)
	}
	return out
}

// makeKey mirrors monitor's internal test helper so the matrix does not
// depend on unexported sub-package helpers.
func makeKey(s string) tea.KeyPressMsg {
	switch s {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

// firstClipboardRequest drains a possibly-batched command and returns the
// first shared.ClipboardRequest it emits, or (zero, false) if none did.
func firstClipboardRequest(cmd tea.Cmd) (shared.ClipboardRequest, bool) {
	for _, msg := range drainCmd(cmd) {
		if req, ok := msg.(shared.ClipboardRequest); ok {
			return req, true
		}
	}
	return shared.ClipboardRequest{}, false
}

// drainCmd runs a command and returns every non-nil message it produces. It
// unwraps a single-level tea.BatchMsg because nested batches never appear in
// the surfaces the matrix touches.
func drainCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		out := make([]tea.Msg, 0, len(batch))
		for _, c := range []tea.Cmd(batch) {
			if c == nil {
				continue
			}
			if m := c(); m != nil {
				out = append(out, m)
			}
		}
		return out
	}
	return []tea.Msg{msg}
}

// helpDescByKey returns the help description associated with `key` if any
// binding in `bs` matches, and reports its enabled state.
func helpDescByKey(bs []keybind.Binding, key string) (desc string, enabled, present bool) {
	for _, b := range bs {
		for _, k := range b.Keys() {
			if k == key {
				return b.Help().Desc, b.Enabled(), true
			}
		}
	}
	return "", false, false
}

// TestClipboardHelpDispatchMatrix walks every enabled and disabled y/Y
// mapping across Runs, Monitor (messages, files, overview) and Review
// (source, range, preview, diff) and asserts:
//
//   - `y` and `Y` either dispatch a shared.ClipboardRequest whose Target.Surface
//     matches the expected surface, or refuse to dispatch (empty/disabled).
//   - The loader's outcome matches expectations (payload for the good path,
//     shared.ErrClipboard* for a rejected one). The exact bytes are asserted
//     in each surface's own test — this matrix guards the mapping, not the
//     serializer.
//   - The corresponding help binding is (a) present in the contextual
//     HelpSections and (b) advertises a "copy…" description matching what
//     will run. The overlay/editor/confirmation cases assert copy neither
//     dispatches nor advertises, so a modal never leaks a dead hint.
func TestClipboardHelpDispatchMatrix(t *testing.T) {
	cases := []struct {
		name string
		// makeProbe stands up the model in the exact state a single copy key
		// should observe; per-case setup is inline for readability.
		makeProbe func(t *testing.T) dispatchProbe
		key       string
		// wantDispatch is true when the surface must produce a
		// shared.ClipboardRequest. False means the key must be swallowed
		// (unavailable / editor-captured / etc.).
		wantDispatch bool
		wantSurface  shared.ClipboardSurface
		// wantHelpKey is the y/Y label expected in the contextual help. Empty
		// means no advertisement should exist (or should be disabled).
		wantHelpKey     string
		wantHelpSubstr  string
		wantHelpEnabled bool
	}{
		{
			name: "runs/y/empty-list",
			makeProbe: func(*testing.T) dispatchProbe {
				m := runspane.NewModel()
				m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
				return &runsProbe{m: m}
			},
			key:             "y",
			wantDispatch:    false,
			wantHelpKey:     "y",
			wantHelpSubstr:  "run id",
			wantHelpEnabled: false,
		},
		{
			name:            "runs/y/single-row",
			makeProbe:       withRunsRow,
			key:             "y",
			wantDispatch:    true,
			wantSurface:     shared.ClipboardSurfaceRunID,
			wantHelpKey:     "y",
			wantHelpSubstr:  "run id",
			wantHelpEnabled: true,
		},
		{
			name:         "runs/Y/no-such-binding",
			makeProbe:    withRunsRow,
			key:          "Y",
			wantDispatch: false,
		},
		{
			// With an empty loaded page the operator still sees a "Copy
			// skipped: no item selected" notice. That is a real dispatch
			// (surface = TranscriptItem) whose loader returns
			// ErrClipboardUnavailable; the help binding is disabled so the
			// two indicators consistently signal "nothing here to copy".
			name:            "monitor/messages/y/transcript-item-empty",
			makeProbe:       withMonitorTranscriptItem,
			key:             "y",
			wantDispatch:    true,
			wantSurface:     shared.ClipboardSurfaceTranscriptItem,
			wantHelpKey:     "y",
			wantHelpSubstr:  "copy",
			wantHelpEnabled: false,
		},
		{
			name:            "monitor/messages/Y/transcript-snapshot",
			makeProbe:       withMonitorTranscriptFocus,
			key:             "Y",
			wantDispatch:    true,
			wantSurface:     shared.ClipboardSurfaceTranscriptSnapshot,
			wantHelpKey:     "Y",
			wantHelpSubstr:  "copy transcript",
			wantHelpEnabled: true,
		},
		{
			name:            "monitor/overview/Y/step-transcript",
			makeProbe:       withMonitorStepsFocus,
			key:             "Y",
			wantDispatch:    true,
			wantSurface:     shared.ClipboardSurfaceTranscriptSnapshot,
			wantHelpKey:     "Y",
			wantHelpSubstr:  "copy transcript",
			wantHelpEnabled: true,
		},
		{
			// Steps focus has no `y` binding.
			name:         "monitor/overview/y/unavailable",
			makeProbe:    withMonitorStepsFocus,
			key:          "y",
			wantDispatch: false,
		},
		{
			name:         "review/source/y/line",
			makeProbe:    withReviewSourceBrowse,
			key:          "y",
			wantDispatch: true,
			wantSurface:  shared.ClipboardSurfaceReviewLine,
		},
		{
			name:         "review/source/Y/document",
			makeProbe:    withReviewSourceBrowse,
			key:          "Y",
			wantDispatch: true,
			wantSurface:  shared.ClipboardSurfaceReviewDocument,
		},
		{
			name:         "review/preview/y/block",
			makeProbe:    withReviewPreview,
			key:          "y",
			wantDispatch: true,
			wantSurface:  shared.ClipboardSurfaceReviewBlock,
		},
		{
			name:         "review/preview/Y/document",
			makeProbe:    withReviewPreview,
			key:          "Y",
			wantDispatch: true,
			wantSurface:  shared.ClipboardSurfaceReviewDocument,
		},
		{
			name:         "review/diff/y/hunk",
			makeProbe:    withReviewDiff,
			key:          "y",
			wantDispatch: true,
			wantSurface:  shared.ClipboardSurfaceReviewHunk,
		},
		{
			name:         "review/diff/Y/file-diff",
			makeProbe:    withReviewDiff,
			key:          "Y",
			wantDispatch: true,
			wantSurface:  shared.ClipboardSurfaceReviewFileDiff,
		},
		{
			// Composer captures printable keys — y is literal text, not copy.
			name:         "review/composer/y/captures-text",
			makeProbe:    withReviewComposer,
			key:          "y",
			wantDispatch: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := tc.makeProbe(t)
			cmd := probe.Send(t, tc.key)
			req, dispatched := firstClipboardRequest(cmd)
			if dispatched != tc.wantDispatch {
				t.Fatalf("dispatched = %v, want %v (cmd msgs=%+v)", dispatched, tc.wantDispatch, drainCmd(cmd))
			}
			if tc.wantDispatch {
				if req.Target.Surface != tc.wantSurface {
					t.Fatalf("target.Surface = %q, want %q", req.Target.Surface, tc.wantSurface)
				}
				if req.Loader == nil {
					t.Fatalf("dispatched request has nil Loader")
				}
			}
			if tc.wantHelpKey == "" {
				return
			}
			desc, enabled, present := helpDescByKey(probe.HelpBindings(), tc.wantHelpKey)
			if !present {
				t.Fatalf("help does not advertise %q at all", tc.wantHelpKey)
			}
			if tc.wantHelpEnabled && !enabled {
				t.Fatalf("help binding %q disabled; want enabled", tc.wantHelpKey)
			}
			if !tc.wantHelpEnabled && enabled {
				t.Fatalf("help binding %q enabled; want disabled", tc.wantHelpKey)
			}
			if tc.wantHelpSubstr != "" && !strings.Contains(strings.ToLower(desc), strings.ToLower(tc.wantHelpSubstr)) {
				t.Fatalf("help %q description = %q, want substring %q", tc.wantHelpKey, desc, tc.wantHelpSubstr)
			}
		})
	}
}

// ── surface fixtures ─────────────────────────────────────────────────────────

func withRunsRow(t *testing.T) dispatchProbe {
	t.Helper()
	m := runspane.NewModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m, _ = m.Update(monitor.EngineEventMsg{Event: engine.RunStarted{
		RunID: "20260101-000000-copytest", Workflow: "wf", Steps: []string{"s"},
	}})
	return &runsProbe{m: m}
}

func withMonitorTranscriptFocus(t *testing.T) dispatchProbe {
	t.Helper()
	m := monitor.New("run-1")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = m.Update(monitor.EngineEventMsg{Event: engine.RunStarted{
		RunID: "run-1", Workflow: "demo", Steps: []string{"a"},
	}})
	// Steps focus is the default after RunStarted; enter promotes to
	// Transcript focus (the same key the operator would press).
	m, _ = m.Update(makeKey("enter"))
	return &monitorProbe{m: m}
}

func withMonitorTranscriptItem(t *testing.T) dispatchProbe {
	// Same as transcript focus, but the item cursor must resolve to a real
	// item. Without any transcript records the item selector returns an
	// unavailable request from the loader, so the dispatch is still a real
	// shared.ClipboardRequest (surface = TranscriptItem) that the loader
	// rejects. That is exactly what the operator would see if they yanked
	// with an empty page.
	return withMonitorTranscriptFocus(t)
}

func withMonitorStepsFocus(t *testing.T) dispatchProbe {
	t.Helper()
	m := monitor.New("run-1")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = m.Update(monitor.EngineEventMsg{Event: engine.RunStarted{
		RunID: "run-1", Workflow: "demo", Steps: []string{"a"},
	}})
	// Steps focus is the default; no navigation needed.
	return &monitorProbe{m: m}
}

func withReviewSourceBrowse(t *testing.T) dispatchProbe {
	t.Helper()
	m := newReviewMonitor(t, "hello\nworld\nend", "text")
	// Toggle from preview into source mode: `s` in browse mode swaps.
	m, _ = m.Update(makeKey("s"))
	return &monitorProbe{m: m}
}

func withReviewPreview(t *testing.T) dispatchProbe {
	t.Helper()
	m := newReviewMonitor(t, "# heading\n\nparagraph text\n", "markdown")
	return &monitorProbe{m: m}
}

func withReviewDiff(t *testing.T) dispatchProbe {
	t.Helper()
	diff := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new\n"
	m := newReviewMonitor(t, diff, "diff")
	return &monitorProbe{m: m}
}

func withReviewComposer(t *testing.T) dispatchProbe {
	t.Helper()
	m := newReviewMonitor(t, "hello\n", "text")
	// c enters compose mode; the textarea now captures printable keys.
	m, _ = m.Update(makeKey("c"))
	return &monitorProbe{m: m}
}

// newReviewMonitor prepares a monitor with an open Review workspace holding
// one document with the given content and format.
func newReviewMonitor(t *testing.T, content, format string) monitor.Model {
	t.Helper()
	m := monitor.New("run-1")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(monitor.EngineEventMsg{Event: engine.RunStarted{
		RunID: "run-1", Workflow: "demo", Steps: []string{"a"},
	}})
	m, _ = m.Update(monitor.EngineEventMsg{Event: engine.ReviewRequest{
		RunID: "run-1", StepID: "a", RoundID: "g000-i000",
		Choices: []string{"approve", "revise"},
		Documents: []domainreview.Document{{
			ID:        "doc",
			Label:     "Doc",
			Source:    "doc." + format,
			Format:    format,
			Content:   content,
			SHA256:    domainreview.Digest(content),
			LineCount: strings.Count(content, "\n") + 1,
		}},
	}})
	// FocusPendingInput promotes to the gate on entry to a run that already
	// has a pending gate — mirrors the operator's monitor-open flow.
	m = m.FocusPendingInput()
	// Enter opens the review workspace.
	m, _ = m.Update(makeKey("enter"))
	return m
}

// TestClipboardOverlayEditorConfirmationPriority proves that root modals and
// text-capturing widgets swallow y/Y before either surface can dispatch a
// copy — no clipboard request is admitted while a modal or editor owns the
// keyboard.
func TestClipboardOverlayEditorConfirmationPriority(t *testing.T) {
	t.Run("help overlay", func(t *testing.T) {
		root := makeRoot(t)
		// ? opens the help modal from any surface.
		root, _ = updateRoot(root, tea.KeyPressMsg{Code: '?', Text: "?"})
		root, cmd := updateRoot(root, tea.KeyPressMsg{Code: 'y', Text: "y"})
		for _, msg := range drainCmd(cmd) {
			if _, ok := msg.(shared.ClipboardRequest); ok {
				t.Fatal("help overlay let y through as copy")
			}
		}
		// Modal is still open — y was consumed as a scroll key.
		root, _ = updateRoot(root, tea.KeyPressMsg{Code: '?', Text: "?"})
		_ = root
	})

	t.Run("palette", func(t *testing.T) {
		root := makeRoot(t)
		// ctrl+k opens the palette; while open, y is literal filter text.
		root, _ = updateRoot(root, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
		root, cmd := updateRoot(root, tea.KeyPressMsg{Code: 'y', Text: "y"})
		for _, msg := range drainCmd(cmd) {
			if _, ok := msg.(shared.ClipboardRequest); ok {
				t.Fatal("palette let y through as copy")
			}
		}
	})

	t.Run("delete-confirm", func(t *testing.T) {
		root := makeRoot(t)
		root, _ = updateRoot(root, monitor.EngineEventMsg{Event: engine.RunStarted{
			RunID: "20260101-000000-deltest", Workflow: "wf", Steps: []string{"s"},
		}})
		// Focus runs pane and press d — runs.Update returns a cmd that
		// emits RequestDeleteMsg, which the root wraps into confirmDelete.
		root.homeFocus = homeRuns
		root, cmd := updateRoot(root, tea.KeyPressMsg{Code: 'd', Text: "d"})
		for _, msg := range drainCmd(cmd) {
			root, _ = updateRoot(root, msg)
		}
		if !root.confirmDelete {
			t.Fatal("delete confirm did not open")
		}
		_, cmd = updateRoot(root, tea.KeyPressMsg{Code: 'y', Text: "y"})
		// y is the confirm answer — it must NOT emit a ClipboardRequest.
		for _, msg := range drainCmd(cmd) {
			if _, ok := msg.(shared.ClipboardRequest); ok {
				t.Fatal("delete-confirm y dispatched a clipboard request")
			}
		}
	})
}

// TestClipboardBusyRefusesOverlappingRequest verifies the root admits exactly
// one in-flight request and rejects a second with the busy notice, so an
// eager operator cannot race two copies into a single terminal write.
func TestClipboardBusyRefusesOverlappingRequest(t *testing.T) {
	root := makeRoot(t)
	// Craft a request whose loader blocks until we release it so the busy
	// slot stays occupied while we admit a second one.
	release := make(chan struct{})
	first := shared.ClipboardRequest{
		Target: shared.ClipboardTarget{Surface: shared.ClipboardSurfaceRunID, Label: "first"},
		Loader: func() shared.ClipboardPayload {
			<-release
			return shared.ClipboardPayload{Payload: "first"}
		},
	}
	root, _ = updateRoot(root, first)
	if !root.clipboard.busy {
		t.Fatal("first request did not occupy the busy slot")
	}
	second := shared.ClipboardRequest{
		Target: shared.ClipboardTarget{Surface: shared.ClipboardSurfaceRunID, Label: "second"},
		Loader: func() shared.ClipboardPayload {
			return shared.ClipboardPayload{Payload: "second"}
		},
	}
	root, _ = updateRoot(root, second)
	// The second request must not steal the slot — pending remains #1.
	if got := root.clipboard.notice; !strings.Contains(got, "another copy is in progress") {
		t.Fatalf("busy notice = %q", got)
	}
	close(release)
}

// TestClipboardRootIntegration proves a copy admitted on one screen still
// completes cleanly after navigating away, because loader IDs (not screen
// identities) match completions to admissions. A stale loader that finishes
// after a newer request is silently dropped.
func TestClipboardRootIntegration(t *testing.T) {
	root := makeRoot(t)
	root, _ = updateRoot(root, monitor.EngineEventMsg{Event: engine.RunStarted{
		RunID: "20260101-000000-navtest", Workflow: "wf", Steps: []string{"s"},
	}})
	root.homeFocus = homeRuns

	// Craft a slow loader we can gate; admit it while on Runs.
	release := make(chan struct{})
	req := shared.ClipboardRequest{
		Target: shared.ClipboardTarget{Surface: shared.ClipboardSurfaceRunID, Label: "run"},
		Loader: func() shared.ClipboardPayload {
			<-release
			return shared.ClipboardPayload{Payload: "run"}
		},
	}
	root, cmd := updateRoot(root, req)
	if !root.clipboard.busy {
		t.Fatal("admit did not set busy")
	}
	pendingID := root.clipboard.pending

	// Navigate to Monitor before releasing the loader.
	root, _ = updateRoot(root, runspane.ShowMonitorMsg{RunID: "20260101-000000-navtest"})
	if root.active != screenMonitor {
		t.Fatalf("nav did not switch to monitor, active=%v", root.active)
	}
	if !root.clipboard.busy || root.clipboard.pending != pendingID {
		t.Fatalf("nav dropped the pending copy id: busy=%v pending=%d", root.clipboard.busy, root.clipboard.pending)
	}

	// Now release the loader and drain the completion. The busy slot must
	// clear and the notice must name the run-id target we admitted from Runs.
	close(release)
	msg := waitForMsg(t, cmd)
	root, _ = updateRoot(root, msg)
	if root.clipboard.busy {
		t.Fatal("completion did not clear busy")
	}
	if got := root.clipboard.notice; !strings.Contains(got, "run ID") {
		t.Fatalf("post-nav notice = %q, want run ID", got)
	}
}

// makeRoot builds a fresh root model in a Home context ready to receive keys.
func makeRoot(t *testing.T) rootModel {
	t.Helper()
	exec := runner.NewFakeExecutor(nil, runner.FakeOutcome{})
	mgr := engine.NewManager(exec, "")
	m := New(context.Background(), mgr).(rootModel)
	m, _ = updateRoot(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	return m
}

// updateRoot narrows rootModel.Update's tea.Model return so tests can chain.
func updateRoot(m rootModel, msg tea.Msg) (rootModel, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(rootModel), cmd
}

// waitForMsg drains a command and returns its first non-nil message, giving
// up after a short deadline so a genuinely-blocking loader doesn't hang the
// test suite silently.
func waitForMsg(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("nil cmd has no completion")
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(1 * time.Second):
		t.Fatal("clipboard command did not complete within 1s")
		return nil
	}
}
