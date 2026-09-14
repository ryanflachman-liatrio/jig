package monitor

import (
	"strings"
	"testing"
	"time"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

// FR-10.1/FR-10.2: an under-threshold thinking block renders its full
// markdown content inline with no collapse marker, matching how an
// under-threshold user text item already renders unconditionally.
func TestThinkingUnderThresholdRendersFullyExpanded(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockThinking, Text: "**considering** the options"}}},
	}})
	if m.chatItems[0].oversized {
		t.Fatalf("under-threshold thinking item must not be flagged oversized")
	}
	if itemHasDetail(m.chatItems[0]) {
		t.Fatalf("under-threshold thinking item must not carry a collapse marker")
	}
	raw := m.itemTranscriptBody()
	plain := stripANSI(raw)
	if !strings.Contains(plain, "considering") || !strings.Contains(plain, "options") {
		t.Fatalf("full reasoning content missing from rendered body:\n%s", plain)
	}
	if strings.Contains(plain, shared.CollapsedMarker) || strings.Contains(plain, shared.ExpandedMarker) {
		t.Fatalf("under-threshold thinking item rendered a collapse marker:\n%s", plain)
	}
}

// FR-10.1: the rendered thinking output carries the italic escape sequence
// and the muted foreground styling sourced from Theme.Chat.Thinking, applied
// through the dedicated thinkingRenderer variant (not the plain renderer).
func TestThinkingRendersItalicMutedStyling(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockThinking, Text: "quiet reasoning prose"}}},
	}})
	raw := m.itemTranscriptBody()
	if !strings.Contains(raw, "\x1b[3") {
		t.Fatalf("rendered thinking output missing italic SGR escape:\n%q", raw)
	}
	rendered := m.renderThinkingMarkdown(m.chatItems[0].primary.key, "quiet reasoning prose")
	if !strings.Contains(rendered, "\x1b[3") {
		t.Fatalf("thinkingRenderer output missing italic SGR escape:\n%q", rendered)
	}
}

// FR-10.3: an oversized thinking block renders the shared collapse-summary
// row when collapsed and full prose when expanded, and its expansion state
// persists across a reload of the same step (setChatPage), matching how
// chatItemExpand already persists for other item kinds.
func TestThinkingOversizedCollapseExpandPersistsAcrossReload(t *testing.T) {
	body := strings.Repeat("reasoning ", (chatTextCollapseBytes/len("reasoning "))+1)
	entries := []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockThinking, Text: body}}},
	}
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: entries})
	if !m.chatItems[0].oversized {
		t.Fatalf("expected thinking item over %d bytes to be flagged oversized", chatTextCollapseBytes)
	}
	if !itemHasDetail(m.chatItems[0]) {
		t.Fatalf("oversized thinking item must carry a collapse marker")
	}

	collapsed := stripANSI(m.itemTranscriptBody())
	if strings.Contains(collapsed, "reasoning reasoning") {
		t.Fatalf("collapsed oversized thinking item leaked full content:\n%.200s", collapsed)
	}

	key := m.chatItems[0].key
	m.chatItemExpand[key] = true
	expanded := stripANSI(m.itemTranscriptBody())
	if !strings.Contains(expanded, "reasoning reasoning") {
		t.Fatalf("expanded oversized thinking item did not render full content:\n%.200s", expanded)
	}

	// Reload the same step (setChatPage): the operator's expansion choice
	// must survive, the same way it does for other item kinds.
	m.setChatPage(transcript.Page{Entries: entries})
	if !m.chatItemExpand[key] {
		t.Fatalf("expansion state did not persist across a reload of the same step")
	}
	reloaded := stripANSI(m.itemTranscriptBody())
	if !strings.Contains(reloaded, "reasoning reasoning") {
		t.Fatalf("reloaded oversized thinking item lost its expanded content:\n%.200s", reloaded)
	}
}

// FR-10.4: the running step's active (trailing) thinking item renders an
// animated pulse label, and the frame advances as distinct TickMsg
// timestamps are driven through Update — the tick-driven repaint path
// (TickMsg dirtying the Transcript panel) that makes the pulse visible
// between engine events. FR-10.6: the "reasoning" text label persists at
// every frame.
func TestThinkingPulseAnimatesForRunningTrailingItem(t *testing.T) {
	runDir := writeTranscript(t, "a", []transcript.Entry{{
		Seq: 1, Role: transcript.RoleAssistant,
		Blocks: []transcript.Block{{Type: transcript.BlockThinking, Text: "still working"}},
	}})
	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")
	m, _ = m.Update(EngineEventMsg{Event: engine.StepStatus{RunID: "run-1", StepID: "a", To: step.StatusRunning}})
	m.loadChatTail() // reload now that the step's status flipped to running

	last := m.chatItems[len(m.chatItems)-1]
	if last.kind != transcriptItemThinking || !last.running {
		t.Fatalf("expected trailing thinking item to be marked running, got %+v", last)
	}
	if !m.hasActiveThinkingPulse() {
		t.Fatalf("expected hasActiveThinkingPulse to report true while the trailing item runs")
	}

	base := time.UnixMilli(1_700_000_000_000)
	m, _ = m.Update(TickMsg(base))
	first := stripANSI(m.itemTranscriptBody())
	firstRange, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: last.key}]
	if !ok {
		t.Fatalf("no line range recorded for the running thinking item")
	}

	m, _ = m.Update(TickMsg(base.Add(monitorFrameInterval)))
	second := stripANSI(m.itemTranscriptBody())
	secondRange, ok := m.chatItemLineRanges[transcriptLineKey{itemKey: last.key}]
	if !ok {
		t.Fatalf("line range disappeared for the running thinking item after a tick")
	}

	if first == second {
		t.Fatalf("pulse glyph did not change one frame interval later:\nframe1:\n%s\nframe2:\n%s", first, second)
	}
	if !strings.Contains(first, "reasoning") || !strings.Contains(second, "reasoning") {
		t.Fatalf("reasoning label missing from a pulse frame:\nframe1:\n%s\nframe2:\n%s", first, second)
	}
	// FR-10.5 (equal-width frames) implies the pulse never changes the
	// item's rendered height: only the glyph's identity changes, not its
	// cell width, so chatItemLineRanges must stay stable across frames.
	if firstRange != secondRange {
		t.Fatalf("pulse animation shifted the item's line range: %+v -> %+v", firstRange, secondRange)
	}
}

// FR-10.7: a settled thinking item (step not running, or not the trailing
// item) always renders the plain, non-animated ◇ reasoning label — never a
// pulse frame — across repeated ticks, and does not cause TickMsg to dirty
// the Transcript panel.
func TestThinkingSettledItemNeverAnimates(t *testing.T) {
	runDir := writeTranscript(t, "a", []transcript.Entry{{
		Seq: 1, Role: transcript.RoleAssistant,
		Blocks: []transcript.Block{{Type: transcript.BlockThinking, Text: "already settled"}},
	}})
	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")

	last := m.chatItems[len(m.chatItems)-1]
	if last.running {
		t.Fatalf("settled step's trailing thinking item must not be marked running")
	}
	if m.hasActiveThinkingPulse() {
		t.Fatalf("expected hasActiveThinkingPulse to report false for a settled step")
	}

	base := time.UnixMilli(1_700_000_000_000)
	m, _ = m.Update(TickMsg(base))
	first := stripANSI(m.itemTranscriptBody())
	if m.dirtyChat {
		t.Fatalf("TickMsg dirtied the Transcript panel for a settled thinking item")
	}

	m, _ = m.Update(TickMsg(base.Add(10 * monitorFrameInterval)))
	second := stripANSI(m.itemTranscriptBody())

	if first != second {
		t.Fatalf("settled thinking item's rendering changed across ticks:\nframe1:\n%s\nframe2:\n%s", first, second)
	}
	if !strings.Contains(first, shared.IconThinking+" reasoning") {
		t.Fatalf("settled thinking item missing the plain %q label:\n%s", shared.IconThinking+" reasoning", first)
	}
}
