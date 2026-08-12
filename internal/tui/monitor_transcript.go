package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"jig/internal/datastore"
	"jig/internal/step"
	"jig/internal/transcript"
)

// reloadTranscript re-points the Transcript panel at the cursor's step and reads
// its transcript eagerly (Resolved Decision 10), resetting per-step view state so
// block-cursor/expand toggles never carry over between steps (seq keys are only
// meaningful within one step's transcript).
func (m *monitorModel) reloadTranscript() {
	if m.cursor >= len(m.steps) {
		return
	}
	id := m.steps[m.cursor].id
	if id == m.chatStep {
		return
	}
	m.chatStep = id
	m.chatBlockCursor = 0
	m.chatExpandAll = false
	m.chatExpand = make(map[blockKey]bool)
	// blockKey is (seq, block) and seq restarts per step-file, so cached renders
	// from the previous step would collide with the new step's same-seq blocks.
	// Reset the render cache along with the other per-step view state.
	m.chatRendered = make(map[blockKey]string)
	m.chatAutoScroll = true
	m.pendingGPrefix = false
	m.loadChat()
	if m.ready {
		m.chatVP.GotoBottom()
	}
}

// loadChat re-reads chatStep's transcript into the model. The transcript file is
// the source of truth (not the lossy event bus); we read only the trailing
// chatWindowMax entries so a long run stays bounded, and flatten the collapsible
// blocks so the block cursor and expand toggles have a stable index. Safe to
// call while the writer appends: the reader opens, reads to EOF, and closes.
func (m *monitorModel) loadChat() {
	m.chatEntries = nil
	m.chatBlocks = nil
	m.chatElided = 0
	if m.runDir == "" || m.chatStep == "" {
		return
	}
	r, err := transcript.Open(datastore.TranscriptPath(m.runDir, m.chatStep))
	if err != nil {
		return
	}
	count, err := r.Count()
	if err != nil {
		return
	}
	offset := 0
	if count > chatWindowMax {
		offset = count - chatWindowMax
		m.chatElided = offset
	}
	entries, err := r.Window(offset, chatWindowMax)
	if err != nil {
		return
	}
	m.chatEntries = entries
	for _, e := range entries {
		for bi, blk := range e.Blocks {
			if collapsible(blk.Type) {
				m.chatBlocks = append(m.chatBlocks, blockKey{seq: e.Seq, block: bi})
			}
		}
	}
	if m.chatBlockCursor >= len(m.chatBlocks) {
		m.chatBlockCursor = 0
	}
}

// collapsible reports whether a block type is collapsed to chatCollapseWidth by
// default (and thus a target for the block cursor). Text renders in full as
// markdown; unknown types render as an inert placeholder.
func collapsible(t transcript.BlockType) bool {
	switch t {
	case transcript.BlockThinking, transcript.BlockToolUse, transcript.BlockToolResult:
		return true
	default:
		return false
	}
}

// chatBody renders one step's agent chat chain from its transcript: assistant
// text (markdown), reasoning, tool calls with inputs, and tool results, with
// large blocks collapsed to chatCollapseWidth and iteration/retry separators.
func (m monitorModel) chatBody() string {
	var b strings.Builder

	// The step id now titles the Transcript panel border, so no in-body title
	// row (task 2.4). A compact status line gives at-a-glance state.
	i, ok := m.index[m.chatStep]
	if !ok {
		if m.chatStep == "" {
			b.WriteString("  " + theme.Question.Render("select a step") + "\n")
		} else {
			b.WriteString("  " + theme.Question.Render("no such step") + "\n")
		}
		return b.String()
	}
	s := m.steps[i]
	indicator, _ := stepIndicator(s.status)
	b.WriteString("  " + indicator + "  " +
		statusStyle(s.status).Render(string(s.status)) + "\n\n")

	running := s.status == step.StatusRunning
	hasTail := false
	if buf, ok := m.stepOutput[m.chatStep]; ok && buf.Len() > 0 {
		hasTail = true
	}

	if len(m.chatEntries) == 0 {
		// Review steps have no transcript. Show the diff here so the reviewer can
		// read it while the verdict choices live in the gate entry (Decision 2/ADR 0005).
		if rev, ok := m.reviews[m.chatStep]; ok {
			b.WriteString("  " + theme.Chat.Hint.Render("proposed changes") + "\n\n")
			if rev.Diff != "" {
				writeDiff(&b, rev.Diff)
				b.WriteString("\n")
			}
			return b.String()
		}
		if m.runDir == "" {
			b.WriteString("  " + theme.Question.Render("transcript unavailable (persistence off)") + "\n")
		} else if !running && !hasTail {
			b.WriteString("  " + theme.Question.Render("no output yet") + "\n")
		}
	}

	if m.chatElided > 0 {
		b.WriteString("  " + theme.Chat.Hint.Render(
			fmt.Sprintf("… %d earlier message(s) elided", m.chatElided)) + "\n\n")
	}

	lastIter, lastAttempt, lastGen := -1, -1, -1
	for _, e := range m.chatEntries {
		if lastGen != -1 && e.Generation > lastGen {
			b.WriteString("\n  " + theme.Marker.Render(
				fmt.Sprintf("── re-run %d ──", e.Generation+1)) + "\n\n")
		}
		if lastIter != -1 && e.Iteration > lastIter {
			b.WriteString("\n  " + theme.Marker.Render(
				fmt.Sprintf("── iteration %d ──", e.Iteration+1)) + "\n\n")
		}
		if lastAttempt != -1 && e.Attempt > lastAttempt {
			b.WriteString("\n  " + theme.Marker.Render(
				fmt.Sprintf("── retry %d ──", e.Attempt)) + "\n\n")
		}
		lastIter, lastAttempt, lastGen = e.Iteration, e.Attempt, e.Generation

		b.WriteString("  " + theme.Chat.Hint.Render(fmt.Sprintf("#%d %s", e.Seq, e.Role)) + "\n")
		for bi, blk := range e.Blocks {
			key := blockKey{seq: e.Seq, block: bi}
			m.writeBlock(&b, key, blk, e.Role)
		}
		b.WriteString("\n")
	}

	// Live tail: the current, not-yet-finalized bubble. Reset on each
	// StepMessage, so it shows only deltas past the last finalized entry.
	if running && hasTail {
		b.WriteString("  " + theme.Question.Render("typing…") + "\n")
		tail := m.stepOutput[m.chatStep].String()
		lines := strings.Split(tail, "\n")
		if len(lines) > outputMaxLines {
			lines = lines[len(lines)-outputMaxLines:]
		}
		for _, l := range lines {
			b.WriteString("  " + l + "\n")
		}
	}

	return b.String()
}

// writeBlock renders one transcript block. Assistant text is markdown (cached);
// system text (command output) is shown verbatim so terminal output is not
// reflowed as prose; thinking, tool_use, and tool_result collapse to
// chatCollapseWidth until expanded.
func (m monitorModel) writeBlock(b *strings.Builder, key blockKey, blk transcript.Block, role transcript.Role) {
	switch blk.Type {
	case transcript.BlockText:
		if role == transcript.RoleSystem {
			writeVerbatim(b, blk.Text)
			return
		}
		b.WriteString(m.renderMarkdown(key, blk.Text))
	case transcript.BlockThinking:
		m.writeCollapsible(b, key, theme.Chat.Thinking, theme.Chat.BarThinking, IconThinking+" reasoning", blk.Text, "", false, blk.Truncated)
	case transcript.BlockToolUse:
		m.writeCollapsible(b, key, theme.Chat.ToolCall, theme.Chat.BarToolCall, IconToolCall+" "+blk.Name, string(blk.Input), fenceJSON(string(blk.Input)), false, false)
	case transcript.BlockToolResult:
		m.writeCollapsible(b, key, theme.Chat.ToolResult, theme.Chat.BarToolResult, IconToolResult+" result", blk.Content, fenceJSON(blk.Content), blk.IsError, blk.Truncated)
	default:
		b.WriteString("  " + theme.Question.Render("[unsupported block: "+string(blk.Type)+"]") + "\n")
	}
}

// renderMarkdown renders a text block as markdown, caching the result per block.
// The cache map is shared across the value copies of monitorModel, so writing to
// it here persists even though the receiver is by value; the map is invalidated
// wholesale on a width change (rebuildRenderer).
func (m monitorModel) renderMarkdown(key blockKey, text string) string {
	if cached, ok := m.chatRendered[key]; ok {
		return cached
	}
	out := text
	if m.renderer != nil {
		if r, err := m.renderer.Render(text); err == nil {
			out = r
		}
	}
	if m.chatRendered != nil {
		m.chatRendered[key] = out
	}
	return out
}

// fenceJSON pretty-prints s as a ```json fenced markdown block so it can be
// rendered with syntax highlighting via renderMarkdown. Returns "" when s is
// not valid JSON so callers can fall back to plain text.
func fenceJSON(s string) string {
	s = expandView(s)
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return ""
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return "```json\n" + string(pretty) + "\n```"
}

// writeCollapsible renders one collapsible block: a role-colored left bar ("▌")
// and a labelled header with a ▸/▾ affordance, then either a one-line preview
// clipped to chatCollapseWidth or the bounded full content (also bar-accented).
// The block under the chat cursor is highlighted so the expand target is
// obvious; error results take theme.Error and the danger bar.
func (m monitorModel) writeCollapsible(b *strings.Builder, key blockKey, labelStyle, barStyle lipgloss.Style, label, content, formattedContent string, isError, truncated bool) {
	expanded := m.chatExpandAll || m.chatExpand[key]
	cursored := len(m.chatBlocks) > 0 && m.chatBlockCursor < len(m.chatBlocks) &&
		m.chatBlocks[m.chatBlockCursor] == key

	marker := CollapsedMarker
	if expanded {
		marker = ExpandedMarker
	}
	head := labelStyle
	bar := barStyle
	if isError {
		head = theme.Error
		bar = theme.Chat.BarError
	}
	barGlyph := bar.Render(BarThick)
	if cursored {
		b.WriteString("  " + barGlyph + " " + theme.Chat.BlockCursor.Render(marker+" "+label))
	} else {
		b.WriteString("  " + barGlyph + " " + marker + " " + head.Render(label))
	}

	if !expanded {
		shown, clipped := collapseLine(content)
		if shown != "" {
			b.WriteString("  " + theme.Question.Render(shown))
		}
		if clipped || truncated {
			b.WriteString(theme.Chat.Hint.Render(
				fmt.Sprintf(" [%d chars]", utf8.RuneCountInString(content))))
		}
		b.WriteString("\n")
		return
	}

	b.WriteString("\n")
	if formattedContent != "" {
		b.WriteString(withBar(bar, m.renderMarkdown(key, formattedContent)))
	} else {
		var body strings.Builder
		for _, l := range strings.Split(expandView(content), "\n") {
			body.WriteString(theme.Question.Render(l) + "\n")
		}
		b.WriteString(withBar(bar, body.String()))
	}
	if truncated {
		b.WriteString("    " + theme.Chat.Hint.Render("… (truncated at write)") + "\n")
	}
}

// withBar prefixes every line of content with a role-colored thick bar ("▌"),
// crush's signature block affordance. content may already carry ANSI styling
// (e.g. glamour output); the bar is emitted before each line's styling begins so
// nested SGR resets never clear it.
func withBar(style lipgloss.Style, content string) string {
	bar := style.Render(BarThick)
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		b.WriteString("  " + bar + " " + l + "\n")
	}
	return b.String()
}

// collapseLine flattens s to a single line and clips it to chatCollapseWidth
// runes, reporting whether it was clipped. Interior whitespace is collapsed so a
// multi-line block previews as one tidy line.
func collapseLine(s string) (string, bool) {
	flat := strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(flat) <= chatCollapseWidth {
		return flat, false
	}
	return string([]rune(flat)[:chatCollapseWidth]), true
}

// expandView bounds a block's expanded content: below chatExpandMax it returns
// the content unchanged; above it, a head+tail with the middle elided so a
// write-capped 256 KiB result never lays out in full.
func expandView(s string) string {
	if len(s) <= chatExpandMax {
		return s
	}
	half := chatExpandMax / 2
	head := clampRunes(s[:half])
	tail := clampRunesTail(s[len(s)-half:])
	elided := len(s) - len(head) - len(tail)
	return head + fmt.Sprintf("\n… %d KB elided …\n", elided/1024) + tail
}

// clampRunes / clampRunesTail back a byte slice off to a rune boundary so
// expandView never splits a multibyte rune at the elision seam.
func clampRunes(s string) string {
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func clampRunesTail(s string) string {
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[1:]
	}
	return s
}

// writeDiff renders a unified diff with +/-/@@ lines styled and indented two
// spaces, truncated to maxDiffLines. Shared by the review overlay in the list
// view and the review-step drill-in in the chat view (Phase 6).
func writeDiff(b *strings.Builder, diff string) {
	b.WriteString("  " + theme.Question.Render("── diff ─────────────────────────────") + "\n")
	lines := strings.Split(diff, "\n")
	const maxDiffLines = 200
	truncated := len(lines) > maxDiffLines
	if truncated {
		lines = lines[:maxDiffLines]
	}
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			b.WriteString("  " + theme.Diff.Add.Render(line) + "\n")
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			b.WriteString("  " + theme.Diff.Remove.Render(line) + "\n")
		case strings.HasPrefix(line, "@@"):
			b.WriteString("  " + theme.Diff.Hunk.Render(line) + "\n")
		default:
			b.WriteString("  " + line + "\n")
		}
	}
	if truncated {
		b.WriteString("  " + theme.Question.Render("… diff truncated") + "\n")
	}
}

// writeVerbatim renders text as-is, one indented line per line, without markdown
// reflow. Used for command-step output (role system), where the content is
// terminal output that glamour would mangle (Phase 6).
func writeVerbatim(b *strings.Builder, text string) {
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		b.WriteString("  " + line + "\n")
	}
}
