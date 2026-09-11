package monitor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"

	"jig/internal/toolcall"
	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

// itemTranscriptBody is the conversation renderer. It consumes the bounded
// item page directly: a matched tool result is therefore never a second row.
func (m *Model) itemTranscriptBody() string {
	var b strings.Builder
	m.chatItemLineRanges = make(map[transcriptLineKey]lineRange)
	line := 0
	for i, item := range m.chatVisibleItems {
		if i > 0 {
			for range itemSpacingBefore(m.chatVisibleItems[i-1], item) {
				b.WriteString("\n")
				line++
			}
		}
		start := line
		itemStart := b.Len()
		block := m.chatEntries[item.primary.entryIdx].Blocks[item.primary.blockIdx]
		selected := i == m.chatItemCursor
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
		case transcriptItemText:
			if item.role == transcript.RoleUser {
				b.WriteString(prefix + shared.Theme.Chat.UserGuidance.Render("User") + "\n" + m.renderMarkdown(item.primary.key, block.Text))
			} else {
				b.WriteString(prefix + m.renderMarkdown(item.primary.key, block.Text))
			}
		case transcriptItemSystem:
			b.WriteString(prefix)
			writeVerbatim(&b, block.Text)
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
			s := summarizeActivity(activity)
			header := composeToolHeader(m, item, s, selected)
			row := marker + " " + header
			if item.kind == transcriptItemToolExchange {
				b.WriteString(m.renderToolExchangeCard(item, prefix, row, selected, expanded) + "\n")
			} else {
				b.WriteString(prefix + row + "\n")
			}
			if expanded {
				m.writeToolActivityDetails(&b, detailActivity, item.displayState == toolDisplaySuccess)
				if (item.toolUse != nil && use.Truncated) || (item.toolResult != nil && m.chatEntries[item.toolResult.entryIdx].Blocks[item.toolResult.blockIdx].Truncated) {
					b.WriteString("      " + shared.Theme.Chat.Hint.Render("… capture truncated at write") + "\n")
				}
			}
		case transcriptItemThinking:
			b.WriteString(prefix + marker + " " + shared.Theme.Chat.Thinking.Render(shared.IconThinking+" reasoning") + "\n")
			if expanded {
				m.writeItemDetail(&b, "Reasoning", block.Text)
			}
		default:
			label := "Unsupported " + string(block.Type)
			b.WriteString(prefix + marker + " " + shared.Theme.Chat.Hint.Render(label) + "\n")
			if expanded {
				m.writeItemDetail(&b, "Content", unsupportedBlockContent(block))
			}
		}
		line += strings.Count(b.String()[itemStart:], "\n")
		m.chatItemLineRanges[transcriptLineKey{itemKey: item.key}] = lineRange{start: start, end: max(start, line-1)}
	}
	return b.String()
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
// visible on collapsed rows without occupying the title (FR-02.17).
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

func prefixCardRows(prefix, card string) string {
	if card == "" {
		return ""
	}
	return prefix + strings.ReplaceAll(card, "\n", "\n"+prefix)
}

func itemHasDetail(item transcriptItem) bool {
	return item.kind != transcriptItemText
}

func (m *Model) writeItemDetail(b *strings.Builder, label, content string) {
	shown, hidden := boundTranscriptDetail(content, max(m.transcriptInnerW-8, 1))
	b.WriteString("      " + shared.Theme.Chat.TranscriptLabel.Render(label+":") + "\n")
	for _, row := range strings.Split(shown, "\n") {
		b.WriteString("      " + shared.Theme.Chat.TranscriptDetail.Render("│ "+row) + "\n")
	}
	if hidden > 0 {
		b.WriteString("      " + shared.Theme.Chat.Hint.Render(fmt.Sprintf("… %d lines hidden", hidden)) + "\n")
	}
}

func (m *Model) writeToolActivityDetails(b *strings.Builder, activity *toolcall.Activity, completed bool) {
	if activity == nil {
		return
	}
	if activity.IsEdit() && writeNewCodeCards(m, b, activity) {
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
		m.writeItemDetail(b, "Locations", strings.Join(locations, "\n"))
	}
	if len(activity.Input) > 0 {
		hasDetail = true
		m.writeItemDetail(b, "Input", prettyToolInput(activity.Input))
	}
	if len(activity.Output) > 0 {
		hasDetail = true
		m.writeItemDetail(b, "Output", prettyToolInput(activity.Output))
	}
	for _, content := range activity.Content {
		if content.Diff != nil {
			continue
		}
		if content.Text != "" {
			hasDetail = true
			m.writeItemDetail(b, "Content", content.Text)
		}
		if len(content.Raw) > 0 {
			hasDetail = true
			m.writeItemDetail(b, "Content", prettyToolInput(content.Raw))
		}
	}
	if completed && activity.IsEdit() && !hasDetail {
		m.writeItemDetail(b, "Edit", "Adapter did not provide edit details.")
	}
}

// writeNewCodeCards deliberately shows the resulting source rather than a
// before/after patch. The inset Glamour renderer uses the shared Charm v2 code
// formatter, whose Lip Gloss code-block style owns the rounded card.
func writeNewCodeCards(m *Model, b *strings.Builder, activity *toolcall.Activity) bool {
	wrote := false
	for _, content := range activity.Content {
		if content.Diff == nil {
			continue
		}
		wrote = true
		shown, hidden := boundTranscriptDetail(content.Diff.NewText, max(m.transcriptInnerW-8, 1))
		b.WriteString("    " + shared.Theme.Chat.TranscriptLabel.Render("New code · "+content.Diff.Path) + "\n")
		for _, line := range strings.Split(strings.TrimRight(m.renderNewCodeCard(content.Diff.Path, shown), "\n"), "\n") {
			b.WriteString("    " + line + "\n")
		}
		if hidden > 0 {
			b.WriteString("    " + shared.Theme.Chat.Hint.Render(fmt.Sprintf("… %d lines hidden", hidden)) + "\n")
		}
	}
	return wrote
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
