package monitor

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"jig/internal/datastore"
	"jig/internal/step"
	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

// reloadTranscript re-points the Transcript panel at the cursor's step and reads
// its transcript eagerly (Resolved Decision 10), resetting per-step view state so
// block-cursor/expand toggles never carry over between steps (seq keys are only
// meaningful within one step's transcript).
func (m *Model) reloadTranscript() {
	stepID := m.cursorStepID()
	if stepID == "" {
		return
	}

	// File row: set selKind/selFile so chatBody renders the file.
	rows := m.visibleRows()
	if m.cursor >= 0 && m.cursor < len(rows) && rows[m.cursor].isFileRow() {
		f := rows[m.cursor].file
		if f != nil {
			m.selKind = "file"
			m.selFile = f.path
			m.chatAutoScroll = false
			m.pendingGPrefix = false
			if m.ready {
				m.chatVP.GotoTop()
			}
		}
		return
	}

	// Step row: revert to chat transcript.
	m.selKind = ""
	m.selFile = ""

	if stepID == m.chatStep {
		return
	}
	m.chatStep = stepID
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
func (m *Model) loadChat() {
	m.chatEntries = nil
	m.chatBlocks = nil
	m.chatElided = 0
	if m.RunDir == "" || m.chatStep == "" {
		return
	}
	r, err := transcript.Open(datastore.TranscriptPath(m.RunDir, m.chatStep))
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
func (m Model) chatBody() string {
	if m.selKind == "file" && m.selFile != "" {
		return m.fileBody()
	}

	var b strings.Builder

	// The step id now titles the Transcript panel border, so no in-body title
	// row (task 2.4). A compact status line gives at-a-glance state.
	i, ok := m.index[m.chatStep]
	if !ok {
		if m.chatStep == "" {
			b.WriteString("  " + shared.Theme.Question.Render("select a step") + "\n")
		} else {
			b.WriteString("  " + shared.Theme.Question.Render("no such step") + "\n")
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
			b.WriteString("  " + shared.Theme.Chat.Hint.Render("proposed changes") + "\n\n")
			if rev.Diff != "" {
				writeDiff(&b, rev.Diff)
				b.WriteString("\n")
			}
			return b.String()
		}
		if m.RunDir == "" {
			b.WriteString("  " + shared.Theme.Question.Render("transcript unavailable (persistence off)") + "\n")
		} else if !running && !hasTail {
			b.WriteString("  " + shared.Theme.Question.Render("no output yet") + "\n")
		}
	}

	if m.chatElided > 0 {
		b.WriteString("  " + shared.Theme.Chat.Hint.Render(
			fmt.Sprintf("… %d earlier message(s) elided", m.chatElided)) + "\n\n")
	}

	lastIter, lastAttempt, lastGen := -1, -1, -1
	for _, e := range m.chatEntries {
		if lastGen != -1 && e.Generation > lastGen {
			b.WriteString("\n  " + shared.Theme.Marker.Render(
				fmt.Sprintf("── re-run %d ──", e.Generation+1)) + "\n\n")
		}
		if lastIter != -1 && e.Iteration > lastIter {
			b.WriteString("\n  " + shared.Theme.Marker.Render(
				fmt.Sprintf("── iteration %d ──", e.Iteration+1)) + "\n\n")
		}
		if lastAttempt != -1 && e.Attempt > lastAttempt {
			b.WriteString("\n  " + shared.Theme.Marker.Render(
				fmt.Sprintf("── retry %d ──", e.Attempt)) + "\n\n")
		}
		lastIter, lastAttempt, lastGen = e.Iteration, e.Attempt, e.Generation

		b.WriteString("  " + shared.Theme.Chat.Hint.Render(fmt.Sprintf("#%d %s", e.Seq, e.Role)) + "\n")
		for bi, blk := range e.Blocks {
			key := blockKey{seq: e.Seq, block: bi}
			m.writeBlock(&b, key, blk, e.Role)
		}
		b.WriteString("\n")
	}

	// Live tail: the current, not-yet-finalized bubble. Reset on each
	// StepMessage, so it shows only deltas past the last finalized entry.
	if running && hasTail {
		b.WriteString("  " + shared.Theme.Question.Render("typing…") + "\n")
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
func (m Model) writeBlock(b *strings.Builder, key blockKey, blk transcript.Block, role transcript.Role) {
	switch blk.Type {
	case transcript.BlockText:
		if role == transcript.RoleSystem {
			writeVerbatim(b, blk.Text)
			return
		}
		b.WriteString(m.renderMarkdown(key, blk.Text))
	case transcript.BlockThinking:
		m.writeCollapsible(b, key, shared.Theme.Chat.Thinking, shared.Theme.Chat.BarThinking, shared.IconThinking+" reasoning", blk.Text, "", false, blk.Truncated)
	case transcript.BlockToolUse:
		inp := expandView(string(blk.Input))
		m.writeCollapsible(b, key, shared.Theme.Chat.ToolCall, shared.Theme.Chat.BarToolCall, shared.IconToolCall+" "+blk.Name, inp, fenceJSON(inp), false, false)
	case transcript.BlockToolResult:
		res := expandView(blk.Content)
		m.writeCollapsible(b, key, shared.Theme.Chat.ToolResult, shared.Theme.Chat.BarToolResult, shared.IconToolResult+" result", res, fenceJSON(res), blk.IsError, blk.Truncated)
	default:
		b.WriteString("  " + shared.Theme.Question.Render("[unsupported block: "+string(blk.Type)+"]") + "\n")
	}
}

// renderMarkdown renders a text block as markdown, caching the result per block.
// The cache map is shared across the value copies of Model, so writing to
// it here persists even though the receiver is by value; the map is invalidated
// wholesale on a width change (rebuildRenderer).
func (m Model) renderMarkdown(key blockKey, text string) string {
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
// not valid JSON so callers can fall back to plain text. The caller is
// responsible for bounding s before passing it in (e.g. via expandView) when
// the source is an unbounded transcript block; file content is pre-bounded by
// readOutputFile's 256 KiB cap so no truncation is needed there.
func fenceJSON(s string) string {
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
// obvious; error results take shared.Theme.Error and the danger bar.
func (m Model) writeCollapsible(b *strings.Builder, key blockKey, labelStyle, barStyle lipgloss.Style, label, content, formattedContent string, isError, truncated bool) {
	expanded := m.chatExpandAll || m.chatExpand[key]
	cursored := len(m.chatBlocks) > 0 && m.chatBlockCursor < len(m.chatBlocks) &&
		m.chatBlocks[m.chatBlockCursor] == key

	marker := shared.CollapsedMarker
	if expanded {
		marker = shared.ExpandedMarker
	}
	head := labelStyle
	bar := barStyle
	if isError {
		head = shared.Theme.Error
		bar = shared.Theme.Chat.BarError
	}
	barGlyph := bar.Render(shared.BarThick)
	if cursored {
		b.WriteString("  " + barGlyph + " " + shared.Theme.Chat.BlockCursor.Render(marker+" "+label))
	} else {
		b.WriteString("  " + barGlyph + " " + marker + " " + head.Render(label))
	}

	if !expanded {
		shown, clipped := collapseLine(content)
		if shown != "" {
			b.WriteString("  " + shared.Theme.Question.Render(shown))
		}
		if clipped || truncated {
			b.WriteString(shared.Theme.Chat.Hint.Render(
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
			body.WriteString(shared.Theme.Question.Render(l) + "\n")
		}
		b.WriteString(withBar(bar, body.String()))
	}
	if truncated {
		b.WriteString("    " + shared.Theme.Chat.Hint.Render("… (truncated at write)") + "\n")
	}
}

// withBar prefixes every line of content with a role-colored thick bar ("▌"),
// crush's signature block affordance. content may already carry ANSI styling
// (e.g. glamour output); the bar is emitted before each line's styling begins so
// nested SGR resets never clear it.
func withBar(style lipgloss.Style, content string) string {
	bar := style.Render(shared.BarThick)
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
	b.WriteString("  " + shared.Theme.Question.Render("── diff ─────────────────────────────") + "\n")
	lines := strings.Split(diff, "\n")
	const maxDiffLines = 200
	truncated := len(lines) > maxDiffLines
	if truncated {
		lines = lines[:maxDiffLines]
	}
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			b.WriteString("  " + shared.Theme.Diff.Add.Render(line) + "\n")
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			b.WriteString("  " + shared.Theme.Diff.Remove.Render(line) + "\n")
		case strings.HasPrefix(line, "@@"):
			b.WriteString("  " + shared.Theme.Diff.Hunk.Render(line) + "\n")
		default:
			b.WriteString("  " + line + "\n")
		}
	}
	if truncated {
		b.WriteString("  " + shared.Theme.Question.Render("… diff truncated") + "\n")
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

// stripBlankEdges drops leading and trailing lines that are blank when ANSI
// escape sequences are removed, then appends a single trailing newline.
// Glamour always emits a blank first line above a code block (its internal
// top-margin row) and a trailing blank; this trims both so the content sits
// flush in the panel without wasted screen rows.
func stripBlankEdges(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	start := 0
	for start < len(lines) && strings.TrimSpace(stripSGR(lines[start])) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(stripSGR(lines[end-1])) == "" {
		end--
	}
	if start >= end {
		return "\n"
	}
	return strings.Join(lines[start:end], "\n") + "\n"
}

// stripSGR removes ANSI SGR escape sequences from s so blank-line detection
// can operate on visible content rather than styled spaces.
func stripSGR(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case inEsc:
			if r == 'm' {
				inEsc = false
			}
		case r == '\x1b':
			inEsc = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fileBody renders the currently-selected output file in the Transcript pane.
// Markdown goes through glamour, JSON is fenced+pretty then glamour, other
// types are verbatim.
func (m Model) fileBody() string {
	var b strings.Builder
	path := m.selFile

	// Find the outputFile to get its kind.
	kind := kindOther
	for _, files := range m.stepFiles {
		for _, f := range files {
			if f.path == path {
				kind = f.kind
				break
			}
		}
	}

	content, placeholder := readOutputFile(path, kind)
	if placeholder != "" {
		b.WriteString("  " + shared.Theme.Question.Render(placeholder) + "\n")
		return b.String()
	}

	// Render directly without the transcript block cache: the cache key
	// (blockKey) is integer-only, so there's no stable per-file key, and file
	// content is read fresh from disk each call anyway.
	// fileRenderer has document margin/prefix/suffix zeroed (see rebuildRenderer)
	// so content sits flush in the panel without glamour's standard document framing.
	render := func(text string) string {
		if m.fileRenderer != nil {
			if r, err := m.fileRenderer.Render(text); err == nil {
				return stripBlankEdges(r)
			}
		}
		return text
	}

	switch kind {
	case kindMarkdown:
		b.WriteString(render(content))
	case kindJSON:
		fenced := fenceJSON(content)
		if fenced == "" {
			fenced = content
		}
		b.WriteString(render(fenced))
	default:
		writeVerbatim(&b, content)
	}

	return b.String()
}
