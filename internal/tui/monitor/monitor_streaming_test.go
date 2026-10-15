package monitor

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
)

func TestMonitorStreamAppearsOnlyInTranscript(t *testing.T) {
	for _, persisted := range []bool{false, true} {
		name := "persistence off"
		if persisted {
			name = "existing transcript"
		}
		t.Run(name, func(t *testing.T) {
			m := newMonitorWithSteps(t)
			if persisted {
				m.RunDir = writeTranscript(t, "a", []transcript.Entry{{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "Previous response"}}}})
			}
			m = enterChatStep(t, m, "a")
			m.steps[m.index["a"]].status = step.StatusRunning
			m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			for _, delta := range []string{"Live response", " continues"} {
				m, _ = m.Update(EngineEventMsg{Event: engine.StepOutput{RunID: "run-1", StepID: "a", Delta: delta}})
				m, _ = m.Update(TickMsg(time.Now()))
				if got := stripANSI(m.vp.View()); strings.Contains(got, "Live response") {
					t.Errorf("response leaked into Steps: %s", got)
				}
				if got := stripANSI(m.chatVP.View()); !strings.Contains(got, "Live response") {
					t.Errorf("response missing from Transcript: %s", got)
				}
			}
			if got := stripANSI(m.chatVP.View()); !strings.Contains(got, "continues") {
				t.Errorf("next delta missing: %s", got)
			}
			if persisted {
				appendTranscriptEntry(t, m.RunDir, "a", transcript.Entry{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "Live response continues"}}})
				m, _ = m.Update(EngineEventMsg{Event: engine.StepMessage{RunID: "run-1", StepID: "a", Seq: 2}})
				m, _ = m.Update(TickMsg(time.Now()))
				if got := stripANSI(m.chatVP.View()); strings.Count(got, "Live response") != 1 {
					t.Errorf("finalized response must replace preview: %s", got)
				}
			}
		})
	}
}

func TestMonitorStreamRespectsSelectedContent(t *testing.T) {
	m := newMonitorWithSteps(t)
	m = enterChatStep(t, m, "a")
	m, _ = m.Update(EngineEventMsg{Event: engine.StepOutput{RunID: "run-1", StepID: "b", Delta: "Other step response"}})
	m, _ = m.Update(EngineEventMsg{Event: engine.StepOutput{RunID: "stale-run", StepID: "a", Delta: "Stale response"}})
	if got := stripANSI(m.chatBody()); strings.Contains(got, "response") {
		t.Fatalf("unrelated output in selected transcript: %s", got)
	}
	m, _ = m.Update(EngineEventMsg{Event: engine.StepOutput{RunID: "run-1", StepID: "a", Delta: "Selected response"}})
	m.chatPage.HasLater = true
	if got := stripANSI(m.chatBody()); strings.Contains(got, "Selected response") {
		t.Fatalf("live output appended to historical page: %s", got)
	}
	m.chatPage.HasLater = false
	m.selKind, m.selFile = "file", "missing-file"
	if got := stripANSI(m.chatBody()); strings.Contains(got, "Selected response") {
		t.Fatalf("live output replaced file view: %s", got)
	}
}

func TestMonitorStreamSurvivesResizeAndPersistenceOffFinalization(t *testing.T) {
	m := newMonitorWithSteps(t)
	m = enterChatStep(t, m, "a")
	for _, width := range []int{120, 60, 20} {
		m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		m, _ = m.Update(EngineEventMsg{Event: engine.StepOutput{RunID: "run-1", StepID: "a", Delta: "Hello"}})
		m, _ = m.Update(TickMsg(time.Now()))
		if got := stripANSI(m.chatVP.View()); !strings.Contains(got, "Hello") {
			t.Fatalf("live output missing at width %d: %s", width, got)
		}
		m, _ = m.Update(EngineEventMsg{Event: engine.StepMessage{RunID: "run-1", StepID: "a", Seq: 1}})
		if got := m.chatBody(); strings.Contains(got, "Hello") {
			t.Fatalf("finalization retained preview: %s", got)
		}
	}
}

func TestMonitorStreamKeepsLongLiteralResponse(t *testing.T) {
	m := enterChatStep(t, newMonitorWithSteps(t), "a")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	var want strings.Builder
	chunks := []string{
		"# Heading\n\n```go\n",
		strings.Repeat("literal **text** with `code`\n", 400),
		"```\n\n| column | value |\n| --- | --- |\n",
		"| final | row |\n",
	}
	for _, delta := range chunks {
		before := m.liveTranscriptTail()
		want.WriteString(delta)
		m, _ = m.Update(EngineEventMsg{Event: engine.StepOutput{RunID: "run-1", StepID: "a", Delta: delta}})
		m, _ = m.Update(TickMsg(time.Now()))
		got := m.liveTranscriptTail()
		if got != want.String() || !strings.HasPrefix(got, before) {
			t.Fatalf("stream changed earlier text or interpreted Markdown: got %d bytes, want %d", len(got), want.Len())
		}
		if !m.chatVP.AtBottom() {
			t.Fatal("stream stopped following the tail")
		}
	}
	m.chatVP.GotoTop()
	m.updateTranscriptFollow(false)
	before := m.chatVP.View()
	m, _ = m.Update(EngineEventMsg{Event: engine.StepOutput{RunID: "run-1", StepID: "a", Delta: "new line\n"}})
	m, _ = m.Update(TickMsg(time.Now()))
	if m.chatVP.View() != before || m.chatAutoScroll {
		t.Fatal("appending moved the paused reader")
	}
}

func TestMonitorStreamPausedFinalizationKeepsPreviewUntilFollow(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.RunDir = writeTranscript(t, "a", []transcript.Entry{{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "Earlier response"}}}})
	m = enterChatStep(t, m, "a")
	text := strings.Repeat("**literal line**\n", 400)
	m, _ = m.Update(EngineEventMsg{Event: engine.StepOutput{RunID: "run-1", StepID: "a", Delta: text}})
	m, _ = m.Update(TickMsg(time.Now()))
	m.scrollTranscript(-5)
	before, offset := m.chatVP.View(), m.chatVP.YOffset()
	appendTranscriptEntry(t, m.RunDir, "a", transcript.Entry{Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: text}}})
	m, _ = m.Update(EngineEventMsg{Event: engine.StepMessage{RunID: "run-1", StepID: "a", Seq: 2}})
	m, _ = m.Update(EngineEventMsg{Event: engine.StepOutput{RunID: "run-1", StepID: "a", Delta: "Next response"}})
	m, _ = m.Update(TickMsg(time.Now()))
	if m.chatVP.View() != before || m.chatVP.YOffset() != offset || m.chatAutoScroll {
		t.Fatal("finalization or the next response moved the paused reader")
	}
	m, _ = m.Update(key("G"))
	if m.chatPausedLive != nil || !m.chatAutoScroll || !m.chatVP.AtBottom() {
		t.Fatal("follow did not replace the frozen preview")
	}
	body := stripANSI(m.chatBody())
	if strings.Contains(body, "**literal line**") || strings.Count(body, "literal line") != 400 || strings.Count(body, "Next response") != 1 {
		t.Fatal("follow failed to load saved Markdown exactly once alongside the new preview")
	}
}

func TestMonitorStreamScrollbackEvictionPreservesPausedReader(t *testing.T) {
	m := enterChatStep(t, newMonitorWithSteps(t), "a")
	first := strings.Repeat("old line\n", liveScrollbackBytes/9)
	m, _ = m.Update(EngineEventMsg{Event: engine.StepOutput{RunID: "run-1", StepID: "a", Delta: first}})
	m, _ = m.Update(TickMsg(time.Now()))
	m.scrollTranscript(-10)
	before := m.chatVP.View()
	m, _ = m.Update(EngineEventMsg{Event: engine.StepOutput{RunID: "run-1", StepID: "a", Delta: strings.Repeat("new line\n", 100)}})
	m, _ = m.Update(TickMsg(time.Now()))
	if m.chatVP.View() != before || m.chatAutoScroll {
		t.Fatal("scrollback eviction moved the paused reader")
	}
	if got := m.stepOutput["a"].text.Len(); got > liveScrollbackBytes {
		t.Fatalf("live buffer grew to %d bytes", got)
	}
	m.resumeTranscriptFollow()
	if m.chatPausedLive != nil || !strings.Contains(m.chatVP.View(), "new line") || !m.chatVP.AtBottom() {
		t.Fatal("resuming did not show the newest live output")
	}
	if !strings.HasPrefix(m.liveTranscriptTail(), "Earlier live output left scrollback.") {
		t.Fatal("scrollback eviction lacks a visible notice")
	}
}

func TestLiveTranscriptBufferUnicodeControlsAndResize(t *testing.T) {
	var buf liveTranscriptBuffer
	buf.append("one\r\n\t**two**\x1b[2J\x1b]52;c;hidden\a\x07\r" + string([]byte{0xe7, 0x95}))
	if got := buf.render(80); got != "one\n    **two**" {
		t.Fatalf("literal display controls/partial rune = %q", got)
	}
	buf.append(string([]byte{0x8c}) + "🙂")
	if got := buf.render(80); got != "one\n    **two**界🙂" {
		t.Fatalf("completed Unicode = %q", got)
	}
	wide := buf.render(80)
	if narrow := buf.render(8); narrow == wide || strings.ReplaceAll(narrow, "\n", "") != strings.ReplaceAll(wide, "\n", "") {
		t.Fatalf("resize lost content: %q", narrow)
	}
	if got := buf.render(80); got != wide {
		t.Fatalf("resize back changed content: %q", got)
	}
}

func TestLiveTranscriptBufferBoundsOversizedChunk(t *testing.T) {
	var buf liveTranscriptBuffer
	buf.append(strings.Repeat("界", liveScrollbackBytes) + "END")
	if buf.text.Len() > liveScrollbackBytes || !buf.dropped || !strings.HasSuffix(buf.text.String(), "END") {
		t.Fatalf("oversized delta not bounded at its tail: bytes=%d dropped=%v", buf.text.Len(), buf.dropped)
	}
	if strings.Contains(buf.render(80), "�") {
		t.Fatal("scrollback cut split a UTF-8 rune")
	}
}

func BenchmarkLiveTranscriptBufferRender(b *testing.B) {
	for _, size := range []int{16 * 1024, liveScrollbackBytes} {
		b.Run(fmt.Sprintf("bytes_%d", size), func(b *testing.B) {
			var buf liveTranscriptBuffer
			const line = "literal **text** with `code`\n"
			buf.append(strings.Repeat(line, size/len(line)))
			b.ReportAllocs()
			b.SetBytes(int64(buf.text.Len()))
			b.ResetTimer()
			for b.Loop() {
				buf.width = 0 // Measure a changed frame, not a cache hit.
				buf.render(80)
			}
		})
	}
}

func TestMonitorStreamFrozenPreviewRespectsSelectionAndPaging(t *testing.T) {
	m := enterChatStep(t, newMonitorWithSteps(t), "a")
	m, _ = m.Update(EngineEventMsg{Event: engine.StepOutput{RunID: "run-1", StepID: "a", Delta: "Frozen preview"}})
	m.chatAutoScroll = false
	m, _ = m.Update(EngineEventMsg{Event: engine.StepMessage{RunID: "run-1", StepID: "a", Seq: 1}})
	if m.liveTranscriptTail() != "Frozen preview" {
		t.Fatal("paused preview did not survive persistence-off finalization")
	}
	m.selKind = "file"
	if m.liveTranscriptTail() != "" {
		t.Fatal("frozen preview appeared in file view")
	}
	m.selKind = ""
	m.chatPage.HasLater = true
	if m.liveTranscriptTail() != "" {
		t.Fatal("frozen preview appeared on a historical page")
	}
	m.chatPage.HasLater = false
	m.cursor = m.index["b"]
	m.reloadTranscript()
	if m.chatPausedLive != nil || m.liveTranscriptTail() != "" {
		t.Fatal("frozen preview survived changing the selected step")
	}
}
