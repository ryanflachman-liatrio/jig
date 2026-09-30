package monitor

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

// Spec 05 Unit 2: the open step's pending stepOutput renders as a live block
// under the finalized items. Every fixture string is synthetic.

func liveEntries(n int) []transcript.Entry {
	entries := make([]transcript.Entry, n)
	for i := range entries {
		entries[i] = transcript.Entry{
			Seq:    i + 1,
			Role:   transcript.RoleAssistant,
			Blocks: []transcript.Block{{Type: transcript.BlockText, Text: fmt.Sprintf("finalized note %02d", i+1)}},
		}
	}
	return entries
}

// newLiveModel opens step a with the given finalized entries and marks it
// running through the engine event path.
func newLiveModel(t *testing.T, entries []transcript.Entry) Model {
	t.Helper()
	m := newTranscriptClickModel(t, entries)
	m = sendEvent(m, engine.StepStatus{RunID: "run-1", StepID: "a", To: step.StatusRunning})
	return m
}

func sendEvent(m Model, ev engine.Event) Model {
	m, _ = m.Update(EngineEventMsg{Event: ev})
	return m
}

func sendDelta(m Model, stepID, delta string) Model {
	return sendEvent(m, engine.StepOutput{RunID: "run-1", StepID: stepID, Delta: delta})
}

func flushFrame(m Model) Model {
	m, _ = m.Update(TickMsg(time.Now()))
	return m
}

func strippedBody(m Model) string {
	return ansi.Strip(m.chatBody())
}

func TestTranscriptLiveBlockRendersAgentDeltas(t *testing.T) {
	m := newLiveModel(t, liveEntries(2))
	for _, d := range []string{"# not a heading ", "streaming **partial", " reply"} {
		m = sendDelta(m, "a", d)
	}
	m = flushFrame(m)

	body := strings.TrimRight(strippedBody(m), "\n")
	want := "    # not a heading streaming **partial reply" + shared.LiveCursor
	if !strings.HasSuffix(body, want) {
		t.Fatalf("body does not end with verbatim live text + cursor %q:\n%s", want, body)
	}
	if !strings.Contains(ansi.Strip(m.chatVP.View()), shared.LiveCursor) {
		t.Fatal("live block not in the viewport after a frame")
	}
	for i, line := range strings.Split(m.chatBody(), "\n") {
		if w := ansi.StringWidth(line); w > m.transcriptInnerW {
			t.Fatalf("line %d width %d exceeds panel width %d", i, w, m.transcriptInnerW)
		}
	}

	// A long unbroken delta wraps to the panel width.
	m = sendDelta(m, "a", " "+strings.Repeat("wrapword", 40))
	m = flushFrame(m)
	for i, line := range strings.Split(m.chatBody(), "\n") {
		if w := ansi.StringWidth(line); w > m.transcriptInnerW {
			t.Fatalf("wrapped line %d width %d exceeds panel width %d", i, w, m.transcriptInnerW)
		}
	}
}

func TestTranscriptLiveBlockClampsCommandOutput(t *testing.T) {
	t.Run("command rows", func(t *testing.T) {
		m := newLiveModel(t, liveEntries(1))
		m.commandSteps = map[string]bool{"a": true}
		for i := 1; i <= 30; i++ {
			m = sendDelta(m, "a", fmt.Sprintf("line %02d\n", i))
		}
		m = flushFrame(m)
		body := strippedBody(m)
		if !strings.Contains(body, shared.EarlierItems(30-transcriptDetailRows, "line", "lines")) {
			t.Fatalf("missing earlier-lines hint:\n%s", body)
		}
		if strings.Contains(body, "line 18") || !strings.Contains(body, "line 19") || !strings.Contains(body, "line 30"+shared.LiveCursor) {
			t.Fatalf("command block did not keep the newest %d rows:\n%s", transcriptDetailRows, body)
		}
	})
	t.Run("command bytes", func(t *testing.T) {
		m := newLiveModel(t, liveEntries(1))
		m.commandSteps = map[string]bool{"a": true}
		m = sendDelta(m, "a", strings.Repeat("x", transcriptDetailBytes)+"\nlast line\n")
		m = flushFrame(m)
		body := strippedBody(m)
		if !strings.Contains(body, "last line"+shared.LiveCursor) || !strings.Contains(body, "earlier") {
			t.Fatalf("byte clamp lost the tail or the hint:\n%s", body)
		}
	})
	t.Run("agent under byte cap shows every row", func(t *testing.T) {
		m := newLiveModel(t, liveEntries(1))
		for i := 1; i <= 30; i++ {
			m = sendDelta(m, "a", fmt.Sprintf("prose %02d\n", i))
		}
		m = flushFrame(m)
		body := strippedBody(m)
		for i := 1; i <= 30; i++ {
			if !strings.Contains(body, fmt.Sprintf("prose %02d", i)) {
				t.Fatalf("agent row %d missing:\n%s", i, body)
			}
		}
		if strings.Contains(body, "earlier") {
			t.Fatalf("agent block under the byte cap shows a drop hint:\n%s", body)
		}
	})
	t.Run("agent over byte cap keeps a rune-safe tail", func(t *testing.T) {
		m := newLiveModel(t, liveEntries(1))
		m = sendDelta(m, "a", strings.Repeat("héllo wörld\n", chatTextCollapseBytes/10)+"final ünïcode line")
		m = flushFrame(m)
		body := m.chatBody()
		if !strings.Contains(ansi.Strip(body), "final ünïcode line"+shared.LiveCursor) || !strings.Contains(ansi.Strip(body), "earlier") {
			t.Fatalf("agent byte clamp lost the tail or the hint:\n%s", ansi.Strip(body))
		}
		if strings.ContainsRune(body, '\uFFFD') {
			t.Fatal("byte clamp split a multibyte rune")
		}
	})
}

// liveTargets captures every item-level surface the live block must not join.
type liveTargets struct {
	ranges  map[transcriptLineKey]lineRange
	targets []transcriptCursorTarget
	hits    []searchHit
	visible int
	copy    string
}

func captureLiveTargets(t *testing.T, m Model) liveTargets {
	t.Helper()
	m.chatBody() // refresh ranges
	got := liveTargets{
		ranges:  make(map[transcriptLineKey]lineRange, len(m.chatItemLineRanges)),
		targets: append([]transcriptCursorTarget(nil), m.chatCursorTargets...),
	}
	for k, v := range m.chatItemLineRanges {
		got.ranges[k] = v
	}
	sm := m
	sm.searchQuery = "shared term"
	sm.rerunSearch()
	got.hits = sm.searchHits
	fm := m
	fm.filters = transcriptFilters{assistant: true}
	fm.rebuildTranscriptItemState(transcriptItemKey{})
	got.visible = len(fm.chatVisibleItems)
	if cmd := m.copyTranscriptItemCmd(); cmd != nil {
		if req, ok := cmd().(shared.ClipboardRequest); ok {
			got.copy = req.Loader().Payload
		}
	}
	return got
}

func TestTranscriptLiveBlockIsNotATarget(t *testing.T) {
	entries := liveEntries(3)
	entries[1].Blocks[0].Text = "finalized shared term"
	m := newLiveModel(t, entries)
	before := captureLiveTargets(t, m)

	m = sendDelta(m, "a", "live shared term\nsecond live row\nthird live row")
	m = flushFrame(m)
	after := captureLiveTargets(t, m)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("live block changed item targets:\nbefore=%+v\nafter=%+v", before, after)
	}
	if len(after.hits) != 1 {
		t.Fatalf("search hits=%d, want 1 (live text must not match)", len(after.hits))
	}

	lines := strings.Split(strippedBody(m), "\n")
	cursor := m.chatItemCursor
	for i, line := range lines {
		if !strings.Contains(line, "live") && !strings.Contains(line, shared.LiveCursor) {
			continue
		}
		m.ensureTranscriptRangeVisible(lineRange{start: i, end: i})
		m, _ = m.Update(clickTranscriptLine(m, i))
		if m.chatItemCursor != cursor {
			t.Fatalf("click on live row %d moved the item cursor %d→%d", i, cursor, m.chatItemCursor)
		}
	}
}

func TestTranscriptLiveBlockScrollFollowAndPause(t *testing.T) {
	tests := []struct {
		name   string
		follow bool
	}{
		{"follow keeps bottom", true},
		{"paused keeps offset", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newLiveModel(t, liveEntries(40))
			m.chatAutoScroll = tt.follow
			m.refreshPanels()
			if !tt.follow {
				m.chatVP.SetYOffset(5)
			}
			offset := m.chatVP.YOffset()
			for i := range 20 {
				m = sendDelta(m, "a", fmt.Sprintf("streamed %02d\n", i))
				m = flushFrame(m)
			}
			if tt.follow && !m.chatVP.AtBottom() {
				t.Fatalf("follow: viewport not at bottom (offset %d)", m.chatVP.YOffset())
			}
			if !tt.follow && m.chatVP.YOffset() != offset {
				t.Fatalf("paused: YOffset %d→%d", offset, m.chatVP.YOffset())
			}
		})
	}
}

func TestTranscriptLiveBlockRepaintsOnFrame(t *testing.T) {
	m := newLiveModel(t, liveEntries(2))
	// Arm the frame loop so the next delta is coalesced, not painted on the
	// leading edge.
	m.ticking = true
	content := m.chatVP.View()
	m = sendDelta(m, "a", "coalesced delta")
	if !m.dirtyChat {
		t.Fatal("delta for the open step did not mark the Transcript dirty")
	}
	if m.chatVP.View() != content {
		t.Fatal("viewport repainted per delta instead of on the frame")
	}
	m = flushFrame(m)
	if !strings.Contains(ansi.Strip(m.chatVP.View()), "coalesced delta") {
		t.Fatal("frame tick did not repaint the live block")
	}

	m.ticking = false
	m.dirtyChat = false
	m.ticking = true
	m = sendDelta(m, "b", "other step delta")
	if m.dirtyChat {
		t.Fatal("delta for a non-open step marked the Transcript dirty")
	}

	m.ticking = false
	m = sendDelta(m, "a", " more")
	if cmd := m.EnsureFrame(); cmd == nil && !m.ticking {
		t.Fatal("no frame scheduled after a delta")
	}
}

func TestTranscriptLiveBlockReplacesWaitingEmptyState(t *testing.T) {
	m := newLiveModel(t, nil)
	m.RunDir = t.TempDir()
	if body := strippedBody(m); !strings.Contains(body, "Waiting for step to start") {
		t.Fatalf("premise: running step with no output shows the waiting state:\n%s", body)
	}
	m = sendDelta(m, "a", "first live words")
	m = flushFrame(m)
	body := strippedBody(m)
	if strings.Contains(body, "Waiting for step to start") {
		t.Fatalf("waiting state still shown with live output:\n%s", body)
	}
	if !strings.Contains(body, "first live words"+shared.LiveCursor) {
		t.Fatalf("live block missing from the empty transcript:\n%s", body)
	}
}

// Spec 05 Unit 3: the live block hands off to the finalized entry and is the
// only home for live output.

// newDiskLiveModel is newLiveModel backed by an on-disk transcript, so a
// StepMessage re-read picks up entries appended mid-test.
func newDiskLiveModel(t *testing.T, entries []transcript.Entry) (Model, string) {
	t.Helper()
	runDir := writeTranscript(t, "a", entries)
	m := newMonitorWithSteps(t)
	m.RunDir = runDir
	m = enterChatStep(t, m, "a")
	m = sendEvent(m, engine.StepStatus{RunID: "run-1", StepID: "a", To: step.StatusRunning})
	return m, runDir
}

func TestTranscriptLiveHandoffOnStepMessage(t *testing.T) {
	m, runDir := newDiskLiveModel(t, liveEntries(2))
	m.chatAutoScroll = true
	m = sendDelta(m, "a", "hello ")
	m = sendDelta(m, "a", "world")
	m = flushFrame(m)
	if !strings.Contains(strippedBody(m), "hello world"+shared.LiveCursor) {
		t.Fatalf("premise: live block missing:\n%s", strippedBody(m))
	}

	seq := appendTranscriptEntry(t, runDir, "a", transcript.Entry{
		Role:   transcript.RoleAssistant,
		Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "hello world"}},
	})
	m = sendEvent(m, engine.StepMessage{RunID: "run-1", StepID: "a", Seq: seq})
	m = flushFrame(m)

	body := strippedBody(m)
	if n := strings.Count(body, "hello world"); n != 1 {
		t.Fatalf("finalized text appears %d times, want 1:\n%s", n, body)
	}
	if strings.Contains(body, shared.LiveCursor) {
		t.Fatalf("live cursor survived the handoff:\n%s", body)
	}
}

func TestTranscriptLiveClearedWhenStepStopsRunning(t *testing.T) {
	for _, to := range []step.Status{step.StatusFailed, step.StatusSucceeded, step.StatusStopped} {
		t.Run(string(to), func(t *testing.T) {
			m := newLiveModel(t, liveEntries(2))
			m = sendDelta(m, "a", "unfinalized live text")
			m = flushFrame(m)
			m = sendEvent(m, engine.StepStatus{RunID: "run-1", StepID: "a", To: to, Err: "synthetic stop reason"})
			m = flushFrame(m)
			body := strippedBody(m)
			if strings.Contains(body, "unfinalized live text") || strings.Contains(body, shared.LiveCursor) {
				t.Fatalf("live block survived %s:\n%s", to, body)
			}
			chrome := ansi.Strip(strings.Join(m.transcriptChrome(), "\n"))
			if to == step.StatusFailed && !strings.Contains(chrome, "synthetic stop reason") {
				t.Fatalf("failed step has no pinned error row: %q", chrome)
			}
		})
	}
	t.Run("retry", func(t *testing.T) {
		m := newLiveModel(t, liveEntries(2))
		m = sendDelta(m, "a", "failed attempt text")
		m = sendEvent(m, engine.StepStatus{RunID: "run-1", StepID: "a", To: step.StatusFailed, Err: "synthetic stop reason"})
		m = sendEvent(m, engine.StepStatus{RunID: "run-1", StepID: "a", To: step.StatusRunning, Attempt: 1})
		m = flushFrame(m)
		if chrome := ansi.Strip(strings.Join(m.transcriptChrome(), "\n")); strings.Contains(chrome, "synthetic stop reason") {
			t.Fatalf("pinned error row survived the retry: %q", chrome)
		}
		if body := strippedBody(m); strings.Contains(body, shared.LiveCursor) {
			t.Fatalf("live block shown before any new delta:\n%s", body)
		}
		m = sendDelta(m, "a", "retry attempt text")
		m = flushFrame(m)
		body := strippedBody(m)
		if strings.Contains(body, "failed attempt text") || !strings.Contains(body, "retry attempt text"+shared.LiveCursor) {
			t.Fatalf("retry live block wrong:\n%s", body)
		}
	})
}

func TestTranscriptLiveBlockFollowsOpenStep(t *testing.T) {
	m := newLiveModel(t, liveEntries(1))
	m = sendEvent(m, engine.StepStatus{RunID: "run-1", StepID: "b", To: step.StatusRunning})
	m = sendDelta(m, "a", "alpha live text")
	m = sendDelta(m, "b", "bravo live text")
	m = flushFrame(m)
	if body := strippedBody(m); !strings.Contains(body, "alpha live text") || strings.Contains(body, "bravo live text") {
		t.Fatalf("step a open: wrong live text:\n%s", body)
	}
	m.focus = focusSteps
	m = enterChatStep(t, m, "b")
	if body := strippedBody(m); !strings.Contains(body, "bravo live text") || strings.Contains(body, "alpha live text") {
		t.Fatalf("step b open: wrong live text:\n%s", body)
	}
}

func TestStepsPanelHasNoLiveTail(t *testing.T) {
	m := newLiveModel(t, liveEntries(1))
	m = sendDelta(m, "a", "steps panel must not show this")
	m = flushFrame(m)
	list := ansi.Strip(m.listBody())
	if strings.Contains(list, "steps panel must not show this") {
		t.Fatalf("Steps panel still renders the live tail:\n%s", list)
	}
	if !strings.Contains(list, shared.IconRunning) || !strings.Contains(list, "running") {
		t.Fatalf("Steps panel lost the running status row:\n%s", list)
	}
}
