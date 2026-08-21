package monitor

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
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
	m.chatGroupExpand = make(map[blockKey]bool)
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
// chatWindowMax entries so a long run stays bounded. Consecutive tool_use and
// tool_result blocks are accumulated into tool call groups (chatGroupHeaders);
// rebuildActiveState derives chatBlocks and chatRenderPlan from those groups and
// the current expansion state. Safe to call while the writer appends: the reader
// opens, reads to EOF, and closes.
func (m *Model) loadChat() {
	// Preserve the block cursor across same-step reloads (e.g. a new StepMessage
	// arriving while the user is navigating). The saved key won't be found in a
	// freshly-loaded different step, so cursor correctly resets to 0 on step changes.
	var saved chatItem
	if len(m.chatBlocks) > 0 && m.chatBlockCursor < len(m.chatBlocks) {
		saved = m.chatBlocks[m.chatBlockCursor]
	}

	m.chatEntries = nil
	m.chatGroupHeaders = nil
	m.chatBlocks = nil
	m.chatRenderPlan = nil
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

	// Single-pass accumulator: consecutive tool_use / tool_result blocks are
	// merged into one tool call group; a thinking block or text block flushes
	// the pending group and becomes a standalone navigation item (thinking) or
	// a render-only item (text).
	var pendingBlocks []blockKey
	var pendingTools []toolLabel
	pendingByID := make(map[string]int) // ToolUseID → index in pendingTools
	pendingHasError := false

	flush := func() {
		if len(pendingBlocks) == 0 {
			return
		}
		g := &toolGroup{
			blocks:   pendingBlocks,
			tools:    pendingTools,
			count:    len(pendingTools),
			hasError: pendingHasError,
		}
		m.chatGroupHeaders = append(m.chatGroupHeaders, chatItem{
			isGroup: true,
			key:     pendingBlocks[0],
			group:   g,
		})
		pendingBlocks = nil
		pendingTools = nil
		pendingByID = make(map[string]int)
		pendingHasError = false
	}

	for _, e := range entries {
		for bi, blk := range e.Blocks {
			bk := blockKey{seq: e.Seq, block: bi}
			switch blk.Type {
			case transcript.BlockToolUse, transcript.BlockToolResult:
				pendingBlocks = append(pendingBlocks, bk)
				if blk.Type == transcript.BlockToolUse {
					idx := len(pendingTools)
					pendingByID[blk.ToolUseID] = idx
					pendingTools = append(pendingTools, toolLabel{
						name:      blk.Name,
						arg:       primaryArg(blk.Name, blk.Input),
						toolUseID: blk.ToolUseID,
					})
				} else {
					// tool_result: record error and pair result summary with its tool_use
					if blk.IsError {
						pendingHasError = true
					}
					if idx, ok := pendingByID[blk.ToolUseID]; ok {
						pendingTools[idx].resultSummary = extractResultSummary(blk)
					}
				}
			case transcript.BlockThinking:
				flush()
				m.chatGroupHeaders = append(m.chatGroupHeaders, chatItem{key: bk})
			default:
				// Text and other non-collapsible types: flush any pending group
				// but do not become a navigation item themselves.
				flush()
			}
		}
	}
	flush()

	m.rebuildActiveState(saved)
}

// rebuildActiveState builds chatBlocks (the active navigation list) and
// chatRenderPlan (the pre-computed render sequence) from chatGroupHeaders and
// the current expansion state (chatGroupExpand, chatExpand, chatExpandAll).
// saved is the chatItem the cursor was on before the rebuild; the cursor is
// restored to its new index after the rebuild (or 0 if not found).
// Must be called whenever expansion state changes.
func (m *Model) rebuildActiveState(saved chatItem) {
	// ── Build block → entry lookup (for cross-entry group member rendering) ──
	type entryRef struct {
		entry *transcript.Entry
	}
	entryBySeq := make(map[int]entryRef, len(m.chatEntries))
	for i := range m.chatEntries {
		entryBySeq[m.chatEntries[i].Seq] = entryRef{entry: &m.chatEntries[i]}
	}

	// ── Build set of all blockKeys that belong to any group ──────────────────
	// groupByFirst maps a group's first blockKey to its chatItem.
	// inGroup is the full membership set (used to suppress absorbed blocks).
	groupByFirst := make(map[blockKey]chatItem, len(m.chatGroupHeaders))
	inGroup := make(map[blockKey]struct{})
	for _, item := range m.chatGroupHeaders {
		if item.isGroup {
			groupByFirst[item.key] = item
			for _, bk := range item.group.blocks {
				inGroup[bk] = struct{}{}
			}
		}
	}

	// ── Build chatBlocks (active navigation list) ─────────────────────────────
	m.chatBlocks = m.chatBlocks[:0]
	for _, item := range m.chatGroupHeaders {
		m.chatBlocks = append(m.chatBlocks, item)
		if item.isGroup && (m.chatGroupExpand[item.key] || m.chatExpandAll) {
			for _, bk := range item.group.blocks {
				m.chatBlocks = append(m.chatBlocks, chatItem{key: bk})
			}
		}
	}

	// ── Restore cursor ────────────────────────────────────────────────────────
	if len(m.chatBlocks) == 0 {
		m.chatBlockCursor = 0
	} else {
		found := 0
		for i, item := range m.chatBlocks {
			if item.isGroup == saved.isGroup && item.key == saved.key {
				found = i
				break
			}
		}
		m.chatBlockCursor = found
		if m.chatBlockCursor >= len(m.chatBlocks) {
			m.chatBlockCursor = 0
		}
	}

	// ── Build chatRenderPlan ──────────────────────────────────────────────────
	m.chatRenderPlan = m.chatRenderPlan[:0]

	lastIter, lastAttempt, lastGen := -1, -1, -1
	for _, e := range m.chatEntries {
		// Emit iteration / retry / re-run banners between entries.
		if lastGen != -1 && e.Generation > lastGen {
			m.chatRenderPlan = append(m.chatRenderPlan, renderItem{
				kind: renderEntrySep,
				sep:  fmt.Sprintf("── re-run %d ──", e.Generation+1),
			})
		}
		if lastIter != -1 && e.Iteration > lastIter {
			m.chatRenderPlan = append(m.chatRenderPlan, renderItem{
				kind: renderEntrySep,
				sep:  fmt.Sprintf("── iteration %d ──", e.Iteration+1),
			})
		}
		if lastAttempt != -1 && e.Attempt > lastAttempt {
			m.chatRenderPlan = append(m.chatRenderPlan, renderItem{
				kind: renderEntrySep,
				sep:  fmt.Sprintf("── retry %d ──", e.Attempt),
			})
		}
		lastIter, lastAttempt, lastGen = e.Iteration, e.Attempt, e.Generation

		// Parse the entry timestamp for the header. RFC3339 failures silently
		// produce an empty string so the header still renders without a timestamp.
		ts := ""
		if t, err := time.Parse(time.RFC3339, e.Ts); err == nil {
			ts = t.Local().Format("15:04:05")
		}

		// Suppress the entry header if every block is absorbed into a group
		// (tool-role entries: the user entry that carries only tool_result blocks).
		allAbsorbed := len(e.Blocks) > 0
		for bi := range e.Blocks {
			if _, ok := inGroup[blockKey{seq: e.Seq, block: bi}]; !ok {
				allAbsorbed = false
				break
			}
		}
		if !allAbsorbed {
			m.chatRenderPlan = append(m.chatRenderPlan, renderItem{
				kind: renderEntryHeader,
				key:  blockKey{seq: e.Seq},
				role: e.Role,
				ts:   ts,
			})
		}

		// Emit block items. Groups are emitted once at their first block's
		// position; all other group members are skipped here.
		//
		// renderedVisible tracks whether this entry produced any visible output
		// (entry header, group header, or standalone block). Fully-absorbed
		// entries that host no group header — e.g. the individual tool_use and
		// tool_result entries ACP writes one-per-message — contribute nothing
		// visible and must not emit a trailing separator, or N tool calls
		// stack N blank lines between the group header and the next message.
		renderedVisible := !allAbsorbed // entry header counts as visible output
		for bi := range e.Blocks {
			bk := blockKey{seq: e.Seq, block: bi}
			eRef := entryBySeq[e.Seq]
			blk := &eRef.entry.Blocks[bi]

			if groupItem, ok := groupByFirst[bk]; ok {
				// First block of a group: emit the group header (and inner blocks
				// if the group is expanded).
				renderedVisible = true
				expanded := m.chatGroupExpand[bk] || m.chatExpandAll
				m.chatRenderPlan = append(m.chatRenderPlan, renderItem{
					kind:  renderGroupHeader,
					key:   bk,
					group: groupItem.group,
				})
				if expanded {
					for _, mbk := range groupItem.group.blocks {
						mRef := entryBySeq[mbk.seq]
						var mBlk *transcript.Block
						if mRef.entry != nil && mbk.block < len(mRef.entry.Blocks) {
							mBlk = &mRef.entry.Blocks[mbk.block]
						}
						var mRole transcript.Role
						if mRef.entry != nil {
							mRole = mRef.entry.Role
						}
						m.chatRenderPlan = append(m.chatRenderPlan, renderItem{
							kind: renderBlock,
							key:  mbk,
							blk:  mBlk,
							role: mRole,
						})
					}
				}
				continue
			}

			if _, ok := inGroup[bk]; ok {
				// Member of a group but not the first block: already handled above.
				continue
			}

			// Standalone block (thinking or unsupported) or text.
			renderedVisible = true
			switch blk.Type {
			case transcript.BlockText:
				m.chatRenderPlan = append(m.chatRenderPlan, renderItem{
					kind: renderText,
					key:  bk,
					blk:  blk,
					role: e.Role,
				})
			default:
				// BlockThinking and any future collapsible type.
				m.chatRenderPlan = append(m.chatRenderPlan, renderItem{
					kind: renderBlock,
					key:  bk,
					blk:  blk,
					role: e.Role,
				})
			}
		}

		// Trailing blank line only when this entry rendered visible content.
		if renderedVisible {
			m.chatRenderPlan = append(m.chatRenderPlan, renderItem{kind: renderEntrySep, sep: ""})
		}
	}
}

// primaryArg extracts a short representative argument string from a tool block's
// input JSON for use in the group summary line. It tries well-known parameter
// keys in priority order, then falls back to the first string value in sorted key
// order. Returns "" when no string value is found. The result is truncated to 40
// runes with a "…" suffix.
func primaryArg(_ string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		return ""
	}
	for _, key := range []string{"file_path", "path", "command", "pattern", "query", "url", "description", "content"} {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return truncateArg(s)
			}
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return truncateArg(s)
		}
	}
	return ""
}

// toolCategoryIcon returns a unicode glyph for a tool name based on its
// functional category, enabling fast visual scanning of tool call groups.
func toolCategoryIcon(name string) string {
	lower := strings.ToLower(name)
	for _, kw := range []string{"read", "write", "edit", "create", "view", "file", "notebook"} {
		if strings.Contains(lower, kw) {
			return "◈"
		}
	}
	for _, kw := range []string{"search", "grep", "find", "glob"} {
		if strings.Contains(lower, kw) {
			return "⌕"
		}
	}
	for _, kw := range []string{"bash", "execute", "shell", "run"} {
		if strings.Contains(lower, kw) {
			return "$"
		}
	}
	for _, kw := range []string{"webfetch", "websearch", "fetch", "web"} {
		if strings.Contains(lower, kw) {
			return "↗"
		}
	}
	for _, kw := range []string{"task", "todo", "agent"} {
		if strings.Contains(lower, kw) {
			return "⊙"
		}
	}
	return shared.IconToolCall
}

// extractResultSummary returns a short human-readable summary of a tool_result
// block for display in collapsed group headers. Error results return "error";
// JSON results with an exit_code key return "exit N"; others return up to 30
// runes of flattened content.
func extractResultSummary(blk transcript.Block) string {
	if blk.IsError {
		return "error"
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(blk.Content), &m); err == nil {
		if ec, ok := m["exit_code"]; ok {
			return fmt.Sprintf("exit %v", ec)
		}
	}
	flat := strings.Join(strings.Fields(blk.Content), " ")
	runes := []rune(flat)
	if len(runes) == 0 {
		return "ok"
	}
	if len(runes) <= 30 {
		return flat
	}
	return string(runes[:29]) + "…"
}

// truncateArg clips s to 40 runes, appending "…" when clipped.
func truncateArg(s string) string {
	runes := []rune(s)
	if len(runes) <= 40 {
		return s
	}
	return string(runes[:39]) + "…"
}

// collapsible reports whether a block type is collapsed to chatCollapseWidth by
// default (and thus navigable via the block cursor). Text renders in full as
// markdown; unknown types render as an inert placeholder.
func collapsible(t transcript.BlockType) bool {
	switch t {
	case transcript.BlockThinking, transcript.BlockToolUse, transcript.BlockToolResult:
		return true
	default:
		return false
	}
}

// chatBody renders one step's agent chat chain from its transcript, consuming
// the pre-computed chatRenderPlan so all grouping and expansion decisions are
// already resolved. chatBody itself is a dumb iterator over renderItems.
func (m Model) chatBody() string {
	if m.selKind == "file" && m.selFile != "" {
		return m.fileBody()
	}

	var b strings.Builder

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
	header := indicator + "  " + shared.Theme.Title.Render(m.chatStep) + "  " +
		statusStyle(s.status).Render(string(s.status))
	var breadcrumbs []string
	if s.iteration > 0 {
		breadcrumbs = append(breadcrumbs, fmt.Sprintf("iter %d", s.iteration+1))
	}
	if s.attempt > 0 {
		breadcrumbs = append(breadcrumbs, fmt.Sprintf("attempt %d", s.attempt))
	}
	if len(breadcrumbs) > 0 {
		header += "  " + shared.Theme.Chat.Hint.Render(strings.Join(breadcrumbs, " • "))
	}
	b.WriteString("  " + header + "\n\n")

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
			if s.err != "" {
				b.WriteString("  " + shared.Theme.Error.Render(s.err) + "\n")
			} else {
				b.WriteString("  " + shared.Theme.Question.Render("no output yet") + "\n")
			}
		}
	}

	if m.chatElided > 0 {
		sep := fmt.Sprintf("── %d earlier messages not shown ──", m.chatElided)
		b.WriteString("\n  " + shared.Theme.Marker.Render(sep) + "\n\n")
	}

	// Dumb iterator over the pre-computed render plan.
	for _, item := range m.chatRenderPlan {
		switch item.kind {
		case renderEntrySep:
			if item.sep == "" {
				b.WriteString("\n")
			} else {
				b.WriteString("\n  " + shared.Theme.Marker.Render(item.sep) + "\n\n")
			}
		case renderEntryHeader:
			left := fmt.Sprintf("#%d %s", item.key.seq, item.role)
			if item.ts != "" && m.transcriptInnerW > 0 {
				totalW := m.transcriptInnerW - 2 // subtract leading "  "
				pad := totalW - utf8.RuneCountInString(left) - utf8.RuneCountInString(item.ts)
				if pad < 1 {
					pad = 1
				}
				line := left + strings.Repeat(" ", pad) + item.ts
				b.WriteString("  " + shared.Theme.Chat.Hint.Render(line) + "\n")
			} else {
				b.WriteString("  " + shared.Theme.Chat.Hint.Render(left) + "\n")
			}
		case renderText:
			if item.blk != nil {
				m.writeBlock(&b, item.key, *item.blk, item.role)
			}
		case renderGroupHeader:
			expanded := m.chatGroupExpand[item.key] || m.chatExpandAll
			cursored := len(m.chatBlocks) > 0 && m.chatBlockCursor < len(m.chatBlocks) &&
				m.chatBlocks[m.chatBlockCursor].isGroup &&
				m.chatBlocks[m.chatBlockCursor].key == item.key
			m.writeGroupHeader(&b, item, expanded, cursored)
		case renderBlock:
			if item.blk != nil {
				m.writeBlock(&b, item.key, *item.blk, item.role)
			}
		}
	}

	// Live tail: the current, not-yet-finalized bubble.
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

// writeGroupHeader renders one tool call group header line:
//
//	▌ ▸/▾ ✓/✗ N tool call(s): Name1(arg), Name2(arg), … (+K)
//
// The bar is in shared.Theme.Chat.BarToolCall (charple accent, matching tool_use
// blocks). A status glyph (✓ success / ✗ error) follows the collapse marker so
// the outcome of a batch is visible without expanding. The name list is formatted
// dynamically to fit m.transcriptInnerW so the summary never overflows on resize.
func (m Model) writeGroupHeader(b *strings.Builder, item renderItem, expanded, cursored bool) {
	bar := shared.Theme.Chat.BarToolCall
	barGlyph := bar.Render(shared.BarThick)
	marker := shared.CollapsedMarker
	if expanded {
		marker = shared.ExpandedMarker
	}

	g := item.group
	noun := "tool calls"
	if g.count == 1 {
		noun = "tool call"
	}

	// Status glyph: styled separately so it retains its color even when cursored.
	var styledStatus string
	statusIcon := shared.IconSuccess
	if g.hasError {
		statusIcon = shared.IconError
		styledStatus = shared.Theme.Error.Render(statusIcon)
	} else {
		styledStatus = shared.Theme.Valid.Render(statusIcon)
	}

	// Available runes for the name list. fixedLeft = "  ▌ " (4) + marker (1) +
	// space (1) + status_icon (1) + space (1) + count + " " + noun + ": ".
	innerPrefix := fmt.Sprintf("%d %s: ", g.count, noun)
	fixedLeft := 4 + 1 + 1 + 1 + 1 + utf8.RuneCountInString(innerPrefix)
	available := m.transcriptInnerW - fixedLeft
	if available < 8 {
		available = 8
	}

	nameList := formatNameList(g.tools, available)

	if cursored {
		// Keep styledStatus outside BlockCursor so its error color is visible.
		b.WriteString("  " + barGlyph + " " + marker + " " + styledStatus + " " +
			shared.Theme.Chat.BlockCursor.Render(innerPrefix+nameList))
	} else {
		b.WriteString("  " + barGlyph + " " + shared.Theme.Chat.ToolCall.Render(marker) +
			" " + styledStatus + " " + shared.Theme.Chat.ToolCall.Render(innerPrefix+nameList))
	}
	b.WriteString("\n")
}

// formatNameList renders the tool label list into a string that fits within
// available runes. Each label includes a category icon prefix, the tool name,
// optional primary arg, and optional result summary (→ result). Entries that
// don't fit are replaced with ", … (+K)" where K is the omitted count.
func formatNameList(tools []toolLabel, available int) string {
	if len(tools) == 0 {
		return ""
	}
	labels := make([]string, len(tools))
	for i, tl := range tools {
		icon := toolCategoryIcon(tl.name)
		l := icon + " " + tl.name
		if tl.arg != "" {
			l += "(" + tl.arg + ")"
		}
		if tl.resultSummary != "" {
			l += " → " + tl.resultSummary
		}
		labels[i] = l
	}
	full := strings.Join(labels, ", ")
	if utf8.RuneCountInString(full) <= available {
		return full
	}
	// If result summaries pushed us over, try without them.
	labelsNoResult := make([]string, len(tools))
	for i, tl := range tools {
		icon := toolCategoryIcon(tl.name)
		l := icon + " " + tl.name
		if tl.arg != "" {
			l += "(" + tl.arg + ")"
		}
		labelsNoResult[i] = l
	}
	if utf8.RuneCountInString(strings.Join(labelsNoResult, ", ")) <= available {
		return strings.Join(labelsNoResult, ", ")
	}
	labels = labelsNoResult // truncation works on the no-result labels
	// Find the maximum prefix that fits alongside a "… (+K)" suffix.
	for n := len(labels) - 1; n >= 0; n-- {
		omitted := len(labels) - n
		var candidate string
		if n == 0 {
			candidate = fmt.Sprintf("… (+%d)", omitted)
		} else {
			candidate = strings.Join(labels[:n], ", ") + fmt.Sprintf(", … (+%d)", omitted)
		}
		if utf8.RuneCountInString(candidate) <= available || n == 0 {
			return candidate
		}
	}
	return fmt.Sprintf("… (+%d)", len(labels))
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
		m.writeCollapsible(b, key, shared.Theme.Chat.ToolCall, shared.Theme.Chat.BarToolCall, toolCategoryIcon(blk.Name)+" "+blk.Name, inp, fenceJSON(inp), false, false)
	case transcript.BlockToolResult:
		res := expandView(blk.Content)
		m.writeCollapsible(b, key, shared.Theme.Chat.ToolResult, shared.Theme.Chat.BarToolResult, shared.IconToolResult+" result", res, fenceJSON(res), blk.IsError, blk.Truncated)
	default:
		b.WriteString("  " + shared.Theme.Question.Render("[unsupported block: "+string(blk.Type)+"]") + "\n")
	}
}

// renderMarkdown renders a text block as markdown, caching the result per block.
// Glamour recognizes code fences and delegates them to the registered
// Chroma/Lip Gloss formatter. The cache map is shared across the value copies of
// Model, so writing to it here persists even though the receiver is by value; the
// map is invalidated wholesale on a width change (rebuildRenderer).
func (m Model) renderMarkdown(key blockKey, text string) string {
	if cached, ok := m.chatRendered[key]; ok {
		return cached
	}
	out := text
	if m.renderer != nil {
		if rendered, err := m.renderer.Render(text); err == nil {
			out = rendered
		}
	}
	if m.chatRendered != nil {
		m.chatRendered[key] = out
	}
	return out
}

// renderInsetMarkdown uses the renderer whose wrap width reserves room for the
// thick-bar prefix around expanded tool content.
func (m Model) renderInsetMarkdown(key blockKey, text string) string {
	if cached, ok := m.chatRendered[key]; ok {
		return cached
	}
	out := text
	if m.insetRenderer != nil {
		if rendered, err := m.insetRenderer.Render(text); err == nil {
			out = stripBlankEdges(rendered)
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
		!m.chatBlocks[m.chatBlockCursor].isGroup &&
		m.chatBlocks[m.chatBlockCursor].key == key

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
		b.WriteString(withBar(bar, m.renderInsetMarkdown(key, formattedContent)))
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
			if rendered, err := m.fileRenderer.Render(text); err == nil {
				return stripBlankEdges(rendered)
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
