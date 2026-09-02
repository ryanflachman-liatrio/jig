package monitor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

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
			prefix += shared.Theme.SelectedBar.Render("▌") + " "
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
			label := s.label
			if item.toolUse == nil {
				label = shared.IconToolResult + " Result (unknown origin)"
			}
			if item.displayState == toolDisplayError {
				label += " failed"
			} else if item.displayState == toolDisplayRunning {
				label += " · running"
			} else if item.displayState == toolDisplayUnknownUse || item.displayState == toolDisplayUnknownResult {
				label += " · incomplete"
			}
			row := marker + " " + label
			if s.preview != "" {
				row += " " + s.preview
			}
			if item.displayState == toolDisplayError {
				row += " · " + toolErrorHint(m, item)
				b.WriteString(prefix + shared.Theme.Chat.TranscriptError.Render(row) + "\n")
			} else if selected {
				b.WriteString(prefix + shared.Theme.Chat.TranscriptSelected.Render(row) + "\n")
			} else {
				b.WriteString(prefix + shared.Theme.Chat.TranscriptActivity.Render(row) + "\n")
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
	hasDetail := false
	for _, content := range activity.Content {
		if content.Diff == nil {
			continue
		}
		hasDetail = true
		old := "(new file)"
		if content.Diff.OldText != nil {
			old = *content.Diff.OldText
		}
		m.writeItemDetail(b, "Diff "+content.Diff.Path, "old:\n"+old+"\nnew:\n"+content.Diff.NewText)
	}
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
