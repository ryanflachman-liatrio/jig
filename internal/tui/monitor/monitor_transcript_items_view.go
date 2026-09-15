package monitor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"jig/internal/toolcall"
	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

// itemTranscriptBody is the conversation renderer. It consumes the bounded
// item page directly: a matched tool result is therefore never a second row.
//
// Slice-04 discipline: each visible item renders into a per-iteration
// scratch buffer and its bytes pass through trimStructuralBlankEdges before
// they contribute to the transcript body. An item whose trimmed output is
// empty contributes neither content nor a separator line, and receives no
// chatItemLineRanges entry (the zero-height guard). itemSpacingBefore is
// applied only between two items that both contributed content, so a
// filtered or hidden item does not leave a ghost gap behind. A row whose
// raw bytes contain any non-whitespace (including an SGR escape) survives
// the trim, so a tinted card padding row is preserved as intentional
// content while a plain blank line is not.
//
// Slice-11 addition: at each coordinate boundary (itemSpacingBefore == 2)
// and after the last visible item of a terminal step, the loop emits one
// dim metadata row for the closing turn. The row's rendered lines are
// folded into the preceding item's chatItemLineRanges entry so n/N block
// navigation still lands on items rather than on the reference row; when
// the row renders empty (all fields unavailable) the layout matches slice
// 04 exactly.
func (m *Model) itemTranscriptBody() string {
	var b strings.Builder
	m.chatItemLineRanges = make(map[transcriptLineKey]lineRange)
	selectedKey := m.selectedTranscriptItemKey()
	line := 0
	lastRenderedIdx := -1
	for i, item := range m.chatVisibleItems {
		var scratch strings.Builder
		var relativeRanges map[transcriptItemKey]lineRange
		if item.kind == transcriptItemToolGroup {
			rendered := m.renderCompactToolGroup(item, selectedKey)
			scratch.WriteString(rendered.body)
			relativeRanges = rendered.ranges
		} else {
			m.writeTranscriptItem(&scratch, item, selectedKey == item.key)
		}
		body := trimStructuralBlankEdges(scratch.String())
		if body == "" {
			continue
		}
		if lastRenderedIdx >= 0 {
			prev := m.chatVisibleItems[lastRenderedIdx]
			spacing := itemSpacingBefore(prev, item)
			// Coordinate change: split the two-line gap so the closing
			// turn's metadata row (slice 11) and the arriving turn's
			// boundary banner (slice 12) sit between one leading and
			// one trailing blank. Both rows fold into the previous
			// item's line range so n/N block navigation still lands on
			// items rather than on this trailing chrome.
			if spacing >= 2 {
				b.WriteString("\n")
				line++
				row := m.renderTurnMetadataRow(prev.coord, false, false)
				if row != "" {
					b.WriteString(row)
					b.WriteString("\n")
					line += strings.Count(row, "\n") + 1
				}
				banner := m.renderBoundaryBanner(prev.coord, item.coord)
				if banner != "" {
					b.WriteString(banner)
					b.WriteString("\n")
					line += strings.Count(banner, "\n") + 1
				}
				if row != "" || banner != "" {
					key := transcriptLineKey{itemKey: m.renderedEndItemKey(prev)}
					rng := m.chatItemLineRanges[key]
					rng.end = line - 1
					m.chatItemLineRanges[key] = rng
				}
				for range spacing - 1 {
					b.WriteString("\n")
					line++
				}
			} else {
				for range spacing {
					b.WriteString("\n")
					line++
				}
			}
		} else if banner := m.renderBoundaryBanner(toolCorrelationKey{}, item.coord); banner != "" {
			// Page-edge banner (visibleExecutionBoundaries i==0 case):
			// the loaded page begins mid-run, so announce the arriving
			// coord above the first rendered item. Emitted outside any
			// item's chatItemLineRanges entry — its rows sit above the
			// first item's start so n/N navigation lands on the item.
			b.WriteString(banner)
			b.WriteString("\n")
			line += strings.Count(banner, "\n") + 1
			b.WriteString("\n")
			line++
		}
		start := line
		b.WriteString(body)
		b.WriteString("\n")
		line += strings.Count(body, "\n") + 1
		if len(relativeRanges) == 0 {
			m.chatItemLineRanges[transcriptLineKey{itemKey: item.key}] = lineRange{start: start, end: line - 1}
		} else {
			for key, rng := range relativeRanges {
				m.chatItemLineRanges[transcriptLineKey{itemKey: key}] = lineRange{start: start + rng.start, end: start + rng.end}
			}
		}
		lastRenderedIdx = i
	}
	// Step-end metadata row: emit below the last rendered item when the
	// current step has reached a terminal status. Interior boundaries do
	// not carry cost/tokens because StepStatus.Cost/Tokens accrue across
	// attempts, not per turn (see docs/plans/omp-slice-11-turn-metadata-row.md).
	if lastRenderedIdx >= 0 {
		prev := m.chatVisibleItems[lastRenderedIdx]
		if idx, ok := m.index[m.chatStep]; ok && !m.steps[idx].end.IsZero() {
			if row := m.renderTurnMetadataRow(prev.coord, true, true); row != "" {
				b.WriteString("\n")
				line++
				b.WriteString(row)
				b.WriteString("\n")
				line += strings.Count(row, "\n") + 1
				key := transcriptLineKey{itemKey: m.renderedEndItemKey(prev)}
				rng := m.chatItemLineRanges[key]
				rng.end = line - 1
				m.chatItemLineRanges[key] = rng
			}
		}
	}
	return b.String()
}

// writeTranscriptItem renders one transcript item into b. Each case arm is
// unchanged from the pre-slice-04 loop; the extraction makes the per-item
// scratch-buffer + edge-trim discipline in itemTranscriptBody readable.
func (m *Model) writeTranscriptItem(b *strings.Builder, item transcriptItem, selected bool) {
	block := m.chatEntries[item.primary.entryIdx].Blocks[item.primary.blockIdx]
	prefix := "  "
	if selected {
		prefix = shared.Theme.SelectedBar.Render(shared.CursorBar) + " "
	}
	expanded := m.chatItemExpandAll || m.chatItemExpand[item.key]
	marker := " "
	if itemHasDetail(item) {
		marker = shared.CollapsedMarker
		if expanded {
			marker = shared.ExpandedMarker
		}
	}
	switch item.kind {
	case transcriptItemReadGroup:
		m.writeReadGroup(b, item, selected, expanded)
	case transcriptItemText:
		if item.role == transcript.RoleUser {
			b.WriteString(m.renderUserText(prefix, item, block, expanded))
		} else {
			rendered := m.renderMarkdown(item.primary.key, block.Text)
			// Glamour prepends a top-margin blank line. Land the item
			// prefix (bar+space when selected, two spaces when unselected)
			// on the first content line instead of on a standalone blank
			// row so the per-item edge trim in itemTranscriptBody behaves
			// prefix-invariantly. A plain-whitespace prefix on its own
			// line would otherwise get trimmed as structural blank while
			// an SGR-styled prefix would survive, drifting the item's
			// line count on selection.
			b.WriteString(prefix + strings.TrimLeft(rendered, "\n"))
		}
	case transcriptItemSystem:
		b.WriteString(prefix)
		writeVerbatim(b, block.Text)
	case transcriptItemToolExchange, transcriptItemToolResult:
		use := block
		if item.toolUse != nil {
			use = m.chatEntries[item.toolUse.entryIdx].Blocks[item.toolUse.blockIdx]
		}
		activity := use.Activity()
		detailActivity := activity
		if item.toolResult != nil {
			result := m.chatEntries[item.toolResult.entryIdx].Blocks[item.toolResult.blockIdx]
			if result.Activity() != nil {
				detailActivity = result.Activity().Clone()
				if detailActivity.Title == "" && activity != nil {
					detailActivity.Title, detailActivity.Kind = activity.Title, activity.Kind
				}
				if detailActivity.ID == "" && activity != nil {
					detailActivity.ID = activity.ID
				}
				if detailActivity.Title != "" || detailActivity.Kind != "" || len(detailActivity.Locations) > 0 || len(detailActivity.Output) > 0 {
					activity = detailActivity
				}
			}
		}
		// summarizeActivity needs the tool-use's Input to format arguments
		// (epic slice 15): when a settled result's activity carries its own
		// Title/Kind/Locations/Output, `activity` above was swapped to that
		// result activity, which normally has no Input of its own. Restore
		// Input from the original use activity on a scoped copy so the
		// summary keeps seeing the request's arguments without mutating
		// `detailActivity` (still used verbatim for the expanded view below).
		summaryActivity := activity
		if summaryActivity != nil && len(summaryActivity.Input) == 0 {
			if orig := use.Activity(); orig != nil && len(orig.Input) > 0 {
				clone := *summaryActivity
				clone.Input = orig.Input
				summaryActivity = &clone
			}
		}
		// The fair-share formatter (epic slice 15) budgets against the full
		// panel content width rather than the narrower space actually left
		// after the icon/title/separator, since RenderStatusLine never
		// truncates internally (status_line.go) and the composed row is
		// clipped to its real on-screen space downstream by the card frame;
		// this keeps the budget an upper bound rather than a pixel-exact fit.
		s := summarizeActivity(summaryActivity, m.transcriptInnerW)
		header := composeToolHeader(m, item, s, selected)
		row := marker + " " + header
		if item.kind == transcriptItemToolExchange {
			b.WriteString(m.renderToolExchangeCard(item, prefix, row, selected, expanded) + "\n")
		} else {
			b.WriteString(prefix + row + "\n")
		}
		if expanded {
			m.writeToolActivityDetails(b, detailActivity, item.displayState == toolDisplaySuccess, anchorForState(item.displayState))
			if (item.toolUse != nil && use.Truncated) || (item.toolResult != nil && m.chatEntries[item.toolResult.entryIdx].Blocks[item.toolResult.blockIdx].Truncated) {
				b.WriteString("      " + shared.Theme.Chat.Hint.Render(shared.CaptureTruncatedHint()) + "\n")
			}
		}
	case transcriptItemThinking:
		m.writeThinkingItem(b, item, block, prefix, marker, expanded)
	default:
		label := "Unsupported " + string(block.Type)
		b.WriteString(prefix + marker + " " + shared.Theme.Chat.Hint.Render(label) + "\n")
		if expanded {
			m.writeItemDetail(b, "Content", unsupportedBlockContent(block), detailAnchorHead)
		}
	}
}

// anchorForState maps a tool exchange's display state to its detail anchor:
// running exchanges tail-anchor so a streaming buffer pins its newest rows
// to the bottom; every other state (settled success, error, warning,
// unknown-use, unknown-result) keeps the head+tail head anchor so the
// beginning of a completed result stays visible (slice 06 Q-06.2).
func anchorForState(state toolDisplayState) detailAnchor {
	if state == toolDisplayRunning {
		return detailAnchorTail
	}
	return detailAnchorHead
}

func cardState(state toolDisplayState) shared.CardState {
	switch state {
	case toolDisplaySuccess:
		return shared.CardSuccess
	case toolDisplayError:
		return shared.CardError
	case toolDisplayRunning:
		return shared.CardRunning
	default:
		return shared.CardWarning
	}
}

// renderToolExchangeCard owns the exchange-only render cache. Detail output is
// intentionally rendered below the header card and never enters this cache.
// The `header` argument is already per-slot styled by composeToolHeader;
// this function does not re-wrap the row so state stays carried by the icon
// and the card border (epic CC-2, FR-02.18).
func (m *Model) renderToolExchangeCard(item transcriptItem, prefix, header string, selected, expanded bool) string {
	available := m.transcriptInnerW - lipgloss.Width(prefix)
	if available < 3 {
		return ""
	}
	key := transcriptRenderKey{itemKey: item.key, surface: transcriptRenderCard, width: available, expanded: expanded, selected: selected, state: item.displayState, header: header}
	if cached, ok := m.chatItemRendered[key]; ok {
		return prefixCardRows(prefix, cached)
	}
	for old := range m.chatItemRendered {
		if old.surface == transcriptRenderCard && old.itemKey == item.key {
			delete(m.chatItemRendered, old)
		}
	}
	card := shared.RenderCard(shared.Card{Header: header, Width: available, State: cardState(item.displayState), Tint: true})
	m.chatItemRendered[key] = card
	return prefixCardRows(prefix, card)
}

// composeToolHeader builds the four-slot status-line header for a tool
// exchange (paired or orphan). The icon and its style come from
// shared.ToolStatusIcon so state is carried by the glyph + border, not by
// appended prose (FR-02.5/02.12/02.15). Selection emphasizes only the title
// slot (FR-02.18); the enclosing card frame or orphan row is not wrapped in
// a further row-level style.
//
// The error hint moves from the appended-label position (previously
// `label += " · " + toolErrorHint(...)`) into the Meta slot so it remains
// visible on collapsed rows without occupying the title (FR-02.17). Slice
// 07 additionally emits a `+N/-M` badge in Meta on expanded non-error
// edit exchanges whose diff computes cleanly (FR-07.18). The error hint
// wins the slot when both would be present. Slice 15 adds a third Meta
// source — a known kind's secondary arguments (`s.meta`, e.g. grep's
// `path`/`case`) — appended only on non-error rows so an error row still
// shows only its hint, never a stale argument preview alongside it.
func composeToolHeader(m *Model, item transcriptItem, s toolCallSummary, selected bool) string {
	title := s.action
	kind := s.kind
	if item.toolUse == nil {
		title = "Result (unknown origin)"
		kind = ""
	}

	glyph, iconStyle := shared.ToolStatusIcon(item.displayState, kind)

	titleStyle := lipgloss.Style{}
	if selected {
		titleStyle = shared.Theme.Chat.TranscriptSelected
	}

	var meta []string
	if item.displayState == toolDisplayError {
		if hint := toolErrorHint(m, item); hint != "" {
			meta = append(meta, hint)
		}
	} else {
		if item.kind == transcriptItemToolExchange && (m.chatItemExpandAll || m.chatItemExpand[item.key]) {
			if badge := diffStatsBadge(m, item); badge != "" {
				meta = append(meta, badge)
			}
		}
		meta = append(meta, s.meta...)
	}

	return shared.RenderStatusLine(shared.StatusLine{
		Icon:        glyph,
		IconStyle:   iconStyle,
		Title:       title,
		TitleStyle:  titleStyle,
		Description: s.detail,
		Meta:        meta,
	})
}

// diffStatsBadge returns the `+N/-M` badge text for a tool exchange
// whose activity carries at least one Diff that computes cleanly and
// records a non-empty change count. Slice 07 uses ASCII bracketing
// (`+3/-2`, not `⟦+3/-2⟧`) until slice 14's glyph preset lands. The
// Diff payload can live on either the tool-use block or its paired
// result block depending on the harness; both are checked and the
// counts aggregated.
func diffStatsBadge(m *Model, item transcriptItem) string {
	if m == nil {
		return ""
	}
	var stats diffStats
	var any bool
	if item.toolUse != nil {
		if a := m.chatEntries[item.toolUse.entryIdx].Blocks[item.toolUse.blockIdx].Activity(); a != nil {
			if s, ok := activityDiffStats(a); ok {
				stats.added += s.added
				stats.removed += s.removed
				any = true
			}
		}
	}
	if item.toolResult != nil {
		if a := m.chatEntries[item.toolResult.entryIdx].Blocks[item.toolResult.blockIdx].Activity(); a != nil {
			if s, ok := activityDiffStats(a); ok {
				stats.added += s.added
				stats.removed += s.removed
				any = true
			}
		}
	}
	if !any {
		return ""
	}
	return "+" + strconv.Itoa(stats.added) + "/-" + strconv.Itoa(stats.removed)
}

// renderUserText builds the operator-authored text item's content and frames
// it as a background bubble (FR-09.1/FR-09.2). An oversized item that is not
// expanded renders a dim one-line summary instead of invoking the markdown
// renderer at all (FR-09.4/FR-09.5); every other user-role text item, and an
// oversized item once expanded, renders the full markdown body (FR-09.6).
func (m *Model) renderUserText(prefix string, item transcriptItem, block transcript.Block, expanded bool) string {
	if item.oversized && !expanded {
		summary := shared.Theme.Chat.Hint.Render(buildCollapseSummary(block.Text, m.transcriptInnerW))
		return m.renderUserBubble(prefix, summary)
	}
	rendered := m.renderMarkdown(item.primary.key, block.Text)
	return m.renderUserBubble(prefix, rendered)
}

// renderUserBubble wraps already-rendered content in the user bubble: one
// tinted blank row above and below the content rows, each row padded with
// trailing fill to the panel's content width so the tint reads as a full
// background fill rather than a highlight limited to the text. Width matches
// the wrap width baked into m.renderer (FR-09.3: same horizontal offset as
// assistant prose, which is prefixed without a separate width reservation).
// Rows are tinted via shared.TintRow so a background-resetting SGR sequence
// inside glamour output (fenced code) does not punch a hole in the bubble
// (FR-09.10).
func (m *Model) renderUserBubble(prefix, content string) string {
	width := m.transcriptInnerW
	if width < 1 {
		width = 1
	}
	background := shared.StyleBackground(shared.Theme.Chat.UserBubble)
	lines := strings.Split(strings.Trim(content, "\n"), "\n")
	rows := make([]string, 0, len(lines)+2)
	padding := shared.TintRow(strings.Repeat(" ", width), background)
	rows = append(rows, padding)
	for _, line := range lines {
		fill := max(width-lipgloss.Width(line), 0)
		rows = append(rows, shared.TintRow(line+strings.Repeat(" ", fill), background))
	}
	rows = append(rows, padding)
	return prefixCardRows(prefix, strings.Join(rows, "\n"))
}

func prefixCardRows(prefix, card string) string {
	if card == "" {
		return ""
	}
	return prefix + strings.ReplaceAll(card, "\n", "\n"+prefix)
}

// writeThinkingItem renders a thinking block's label row followed by its
// content: full italic/muted markdown prose when the block is at or under
// the collapse threshold (FR-10.1/FR-10.2, unconditionally — there is no
// expand/collapse toggle for an under-threshold block), or the shared
// collapse-summary row/full prose pair driven by `expanded` when oversized
// (FR-10.3), reusing the same helpers renderUserText uses for oversized
// user text.
func (m *Model) writeThinkingItem(b *strings.Builder, item transcriptItem, block transcript.Block, prefix, marker string, expanded bool) {
	glyph := shared.IconThinking
	if item.running {
		glyph = pulseFrame(m.lastTick, shared.PulseFrames)
	}
	label := glyph + " reasoning"
	b.WriteString(prefix + marker + " " + shared.Theme.Chat.Thinking.Render(label) + "\n")
	if item.oversized && !expanded {
		summary := shared.Theme.Chat.Hint.Render(buildCollapseSummary(block.Text, m.transcriptInnerW))
		b.WriteString(prefix + summary + "\n")
		return
	}
	rendered := m.renderThinkingMarkdown(item.primary.key, block.Text)
	b.WriteString(prefix + strings.TrimLeft(rendered, "\n"))
}

func itemHasDetail(item transcriptItem) bool {
	if item.kind == transcriptItemText || item.kind == transcriptItemThinking {
		return item.oversized
	}
	return true
}

func (m *Model) writeItemDetail(b *strings.Builder, label, content string, anchor detailAnchor) {
	shown, hidden := boundTranscriptDetail(content, max(m.transcriptInnerW-8, 1), anchor)
	// The detail body is currently rendered inside `if expanded { ... }`
	// so the item's toggle state is always true here; passing expanded=false
	// lets ExpandHint short-circuit only on hasMore, which keeps the hint
	// visible whenever content is bounded. jig has no separate "fully
	// expanded body" state distinct from the row bound, so this is the
	// correct semantic (epic slice 06, Q-06.1).
	hint := shared.ExpandHint(false, hidden > 0, m.keys.Toggle.Help().Key)
	b.WriteString("      " + shared.Theme.Chat.TranscriptLabel.Render(label+":") + "\n")
	if hidden > 0 && anchor == detailAnchorTail {
		line := shared.HintLine(shared.EarlierItems(hidden, "line", "lines"), hint)
		b.WriteString("      " + shared.Theme.Chat.Hint.Render(line) + "\n")
	}
	for _, row := range strings.Split(shown, "\n") {
		b.WriteString("      " + shared.Theme.Chat.TranscriptDetail.Render(shared.BoxVertical+" "+row) + "\n")
	}
	if hidden > 0 && anchor != detailAnchorTail {
		line := shared.HintLine(shared.MoreItems(hidden, "line", "lines"), hint)
		b.WriteString("      " + shared.Theme.Chat.Hint.Render(line) + "\n")
	}
}

func (m *Model) writeToolActivityDetails(b *strings.Builder, activity *toolcall.Activity, completed bool, anchor detailAnchor) {
	if activity == nil {
		return
	}
	if activity.IsEdit() && writeNewCodeCards(m, b, activity, anchor) {
		return
	}
	hasDetail := false
	if !hasDetail && len(activity.Locations) > 0 {
		hasDetail = true
		var locations []string
		for _, location := range activity.Locations {
			value := location.Path
			if location.Line != nil {
				value += fmt.Sprintf(":%d", *location.Line)
			}
			locations = append(locations, value)
		}
		m.writeItemDetail(b, "Locations", strings.Join(locations, "\n"), anchor)
	}
	if len(activity.Input) > 0 {
		hasDetail = true
		m.writeItemDetail(b, "Input", prettyToolInput(activity.Input), anchor)
	}
	if len(activity.Output) > 0 {
		hasDetail = true
		m.writeItemDetail(b, "Output", prettyToolInput(activity.Output), anchor)
	}
	for _, content := range activity.Content {
		if content.Diff != nil {
			continue
		}
		if content.Text != "" {
			hasDetail = true
			m.writeItemDetail(b, "Content", content.Text, anchor)
		}
		if len(content.Raw) > 0 {
			hasDetail = true
			m.writeItemDetail(b, "Content", prettyToolInput(content.Raw), anchor)
		}
	}
	if completed && activity.IsEdit() && !hasDetail {
		m.writeItemDetail(b, "Edit", "Adapter did not provide edit details.", anchor)
	}
}

// writeNewCodeCards renders the diff section below an edit tool
// exchange. When Diff.OldText is present and computation succeeds it
// emits the before/after diff produced by renderDiffRows below a
// `Diff · <path>` label; otherwise it falls back to the resulting-
// source card (the historical behavior) below the `New code · <path>`
// label. File creation renders no fallback hint; skipped or failed
// computation prepends shared.DiffUnavailableHint(); a truncated
// enclosing block prepends shared.DiffClampedHint() so the operator
// sees the clamp before trusting the change spans.
//
// The anchor argument mirrors writeItemDetail: a running exchange
// tail-anchors so the newest lines pin to the bottom of the visible
// window.
func writeNewCodeCards(m *Model, b *strings.Builder, activity *toolcall.Activity, anchor detailAnchor) bool {
	wrote := false
	blockTruncated := activityBlockTruncated(m, activity)
	for _, content := range activity.Content {
		if content.Diff == nil {
			continue
		}
		wrote = true
		proj, _, outcome := computeDiff(content.Diff)
		switch outcome {
		case computeOK:
			if proj == nil || len(proj.Presentation.Hunks) == 0 {
				// Identical inputs (no visible change): fall through to the
				// resulting-source card so the operator still sees the file
				// content the exchange wrote.
				writeResultingSourceCard(m, b, content.Diff, "", anchor)
				continue
			}
			hintText := ""
			if blockTruncated {
				hintText = shared.DiffClampedHint()
			}
			writeDiffSection(m, b, content.Diff, proj, hintText, anchor)
		case computeFileCreation:
			writeResultingSourceCard(m, b, content.Diff, "", anchor)
		default:
			writeResultingSourceCard(m, b, content.Diff, shared.DiffUnavailableHint(), anchor)
		}
	}
	return wrote
}

// writeDiffSection emits the diff-labeled section: an optional
// clamped-content hint, the diff rows produced by renderDiffRows,
// and the slice-06 head/tail anchor bound applied over the joined
// rows so long diffs share the transcript's row budget.
func writeDiffSection(m *Model, b *strings.Builder, d *toolcall.Diff, proj *diffProjection, hintText string, anchor detailAnchor) {
	label := "Diff · " + d.Path
	if d.Path == "" {
		label = "Diff"
	}
	b.WriteString("    " + shared.Theme.Chat.TranscriptLabel.Render(label) + "\n")

	contentWidth := max(m.transcriptInnerW-4, 1)
	expandKey := m.keys.Toggle.Help().Key
	rows := renderDiffRows(proj, d.Path, contentWidth, true, m.insetRenderer, expandKey)
	joined := strings.Join(rows, "\n")
	shown, hidden := boundTranscriptDetail(joined, contentWidth, anchor)
	hint := shared.ExpandHint(false, hidden > 0, expandKey)

	if hintText != "" {
		b.WriteString("    " + shared.Theme.Chat.Hint.Render(hintText) + "\n")
	}
	if hidden > 0 && anchor == detailAnchorTail {
		line := shared.HintLine(shared.EarlierItems(hidden, "line", "lines"), hint)
		b.WriteString("    " + shared.Theme.Chat.Hint.Render(line) + "\n")
	}
	for _, row := range strings.Split(shown, "\n") {
		b.WriteString("    " + row + "\n")
	}
	if hidden > 0 && anchor != detailAnchorTail {
		line := shared.HintLine(shared.MoreItems(hidden, "line", "lines"), hint)
		b.WriteString("    " + shared.Theme.Chat.Hint.Render(line) + "\n")
	}
}

// writeResultingSourceCard emits the historical "resulting source"
// card. It preserves the pre-slice-07 shape verbatim so slice 07's
// fallback path exercises the same rendering as file-creation cases
// have always used. hintText, when non-empty, is prepended as a dim
// row above the card so the operator learns why the diff view is not
// being shown.
func writeResultingSourceCard(m *Model, b *strings.Builder, d *toolcall.Diff, hintText string, anchor detailAnchor) {
	shown, hidden := boundTranscriptDetail(d.NewText, max(m.transcriptInnerW-8, 1), anchor)
	hint := shared.ExpandHint(false, hidden > 0, m.keys.Toggle.Help().Key)
	b.WriteString("    " + shared.Theme.Chat.TranscriptLabel.Render("New code · "+d.Path) + "\n")
	if hintText != "" {
		b.WriteString("    " + shared.Theme.Chat.Hint.Render(hintText) + "\n")
	}
	if hidden > 0 && anchor == detailAnchorTail {
		line := shared.HintLine(shared.EarlierItems(hidden, "line", "lines"), hint)
		b.WriteString("    " + shared.Theme.Chat.Hint.Render(line) + "\n")
	}
	for _, line := range strings.Split(strings.TrimRight(m.renderNewCodeCard(d.Path, shown), "\n"), "\n") {
		b.WriteString("    " + line + "\n")
	}
	if hidden > 0 && anchor != detailAnchorTail {
		line := shared.HintLine(shared.MoreItems(hidden, "line", "lines"), hint)
		b.WriteString("    " + shared.Theme.Chat.Hint.Render(line) + "\n")
	}
}

// activityBlockTruncated reports whether the transcript block that
// carries this activity's tool-use payload was clamped at write time.
// It lets the diff section emit shared.DiffClampedHint before rows so
// the operator sees the correctness label above the diff itself.
func activityBlockTruncated(m *Model, activity *toolcall.Activity) bool {
	if m == nil || activity == nil || activity.ID == "" {
		return false
	}
	for _, entry := range m.chatEntries {
		for _, block := range entry.Blocks {
			if block.Tool == nil || block.Tool.ID != activity.ID {
				continue
			}
			if block.Type == transcript.BlockToolUse && block.Truncated {
				return true
			}
		}
	}
	return false
}

func (m *Model) renderNewCodeCard(path, code string) string {
	if m.insetRenderer == nil {
		return shared.Theme.Chat.CodeBlock.Width(max(m.transcriptInnerW-4, 1)).Render(code)
	}
	rendered, err := m.insetRenderer.Render(fencedCode(path, code))
	if err != nil {
		return shared.Theme.Chat.CodeBlock.Width(max(m.transcriptInnerW-4, 1)).Render(code)
	}
	return stripBlankEdges(rendered)
}

func fencedCode(path, code string) string {
	delimiter := "```"
	for strings.Contains(code, delimiter) {
		delimiter += "`"
	}
	return delimiter + codeLanguage(path) + "\n" + code + "\n" + delimiter
}

func codeLanguage(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".toml":
		return "toml"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".md", ".markdown":
		return "markdown"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".mts", ".cts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".jsx":
		return "jsx"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".sh", ".bash", ".zsh":
		return "bash"
	default:
		return "text"
	}
}

func toolErrorHint(m *Model, item transcriptItem) string {
	if item.toolResult == nil {
		return "error"
	}
	block := m.chatEntries[item.toolResult.entryIdx].Blocks[item.toolResult.blockIdx]
	line := ""
	if block.Activity() != nil {
		for _, content := range block.Activity().Content {
			if content.Type == "text" {
				line, _, _ = strings.Cut(content.Text, "\n")
				break
			}
		}
	}
	if line = sanitizeToolSummary(line); line != "" {
		return line
	}
	return "error"
}

func unsupportedBlockContent(block transcript.Block) string {
	if block.Activity() == nil {
		return block.Text
	}
	activity := block.Activity()
	return strings.TrimSpace(block.Text + "\n" + activity.Title + "\n" + string(activity.Input) + "\n" + string(activity.Output))
}

func prettyToolInput(raw []byte) string {
	if len(raw) == 0 {
		return "(empty)"
	}
	var formatted bytes.Buffer
	if jsonErr := json.Indent(&formatted, raw, "", "  "); jsonErr == nil {
		return formatted.String()
	}
	return string(raw)
}
