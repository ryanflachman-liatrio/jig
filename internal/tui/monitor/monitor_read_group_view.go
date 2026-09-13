package monitor

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"jig/internal/toolcall"
	"jig/internal/tui/shared"
)

func (m *Model) writeReadGroup(b *strings.Builder, item transcriptItem, selected, expanded bool) {
	rows := readGroupRows(item, m.chatEntries)
	glyph, iconStyle := shared.ToolStatusIcon(item.displayState, "read")
	titleStyle := shared.Theme.Chat.ReadGroupTitle
	if selected {
		titleStyle = shared.Theme.Chat.TranscriptSelected
	}
	header := shared.RenderStatusLine(shared.StatusLine{
		Icon:       glyph,
		IconStyle:  iconStyle,
		Title:      "Read",
		TitleStyle: titleStyle,
		Meta:       []string{"(" + strconv.Itoa(len(item.groupMembers)) + ")"},
	})
	prefix := "  "
	if selected {
		prefix = shared.Theme.SelectedBar.Render(shared.CursorBar) + " "
	}

	cacheHeader := header
	for _, row := range rows {
		cacheHeader += "\x00" + row.path + "\x00" + strings.Join(row.selectors, ",") + "\x00" + strconv.Itoa(int(row.state))
	}
	key := transcriptRenderKey{itemKey: item.key, surface: transcriptRenderReadGroup, width: m.transcriptInnerW, expanded: expanded, selected: selected, state: item.displayState, header: cacheHeader}
	summary, ok := m.chatItemRendered[key]
	if !ok {
		var out strings.Builder
		writeBoundedGroupLine(&out, prefix, header, m.transcriptInnerW)
		for i, row := range rows {
			out.WriteByte('\n')
			last := i == len(rows)-1
			connector := shared.Theme.Chat.ReadGroupConnector.Render(shared.TreePrefix(last))
			state := ""
			if row.state != toolDisplaySuccess {
				stateGlyph, stateStyle := shared.ToolStatusIcon(row.state, "read")
				state = stateStyle.Render(stateGlyph) + " "
			}
			target := shared.Theme.Chat.ReadGroupTarget.Render(shortFile(row.path))
			if selectors := compactReadSelectors(row.selectors); len(selectors) > 0 {
				target += shared.Theme.Chat.ToolMeta.Render(":" + strings.Join(selectors, ", "))
			}
			writeBoundedGroupLine(&out, prefix, connector+state+target, m.transcriptInnerW)
		}
		summary = out.String()
		m.chatItemRendered[key] = summary
	}
	b.WriteString(summary)
	b.WriteByte('\n')

	if !expanded {
		return
	}
	for rowIdx, row := range rows {
		last := rowIdx == len(rows)-1
		continuation := shared.Theme.Chat.ReadGroupConnector.Render(shared.TreeContinuationPrefix(last))
		for _, member := range row.members {
			activity := m.readGroupMemberActivity(member)
			var detail strings.Builder
			m.writeToolActivityDetails(&detail, activity, member.displayState == toolDisplaySuccess, anchorForState(member.displayState))
			if (member.toolUse != nil && m.chatEntries[member.toolUse.entryIdx].Blocks[member.toolUse.blockIdx].Truncated) ||
				(member.toolResult != nil && m.chatEntries[member.toolResult.entryIdx].Blocks[member.toolResult.blockIdx].Truncated) {
				detail.WriteString("      " + shared.Theme.Chat.Hint.Render(shared.CaptureTruncatedHint()) + "\n")
			}
			for _, line := range strings.Split(strings.TrimRight(detail.String(), "\n"), "\n") {
				if line == "" {
					continue
				}
				b.WriteString(prefix)
				b.WriteString(continuation)
				b.WriteString(strings.TrimPrefix(line, "      "))
				b.WriteByte('\n')
			}
		}
	}
}

func writeBoundedGroupLine(b *strings.Builder, prefix, content string, width int) {
	available := width - lipgloss.Width(prefix)
	if available <= 0 {
		return
	}
	b.WriteString(prefix)
	b.WriteString(shared.TruncateTitle(content, available))
}

func (m *Model) readGroupMemberActivity(member transcriptItem) *toolcall.Activity {
	var activity *toolcall.Activity
	if member.toolUse != nil {
		activity = m.chatEntries[member.toolUse.entryIdx].Blocks[member.toolUse.blockIdx].Activity()
	}
	if member.toolResult == nil {
		return activity
	}
	result := m.chatEntries[member.toolResult.entryIdx].Blocks[member.toolResult.blockIdx]
	if result.Activity() == nil {
		return activity
	}
	detail := result.Activity().Clone()
	if activity != nil {
		if detail.Title == "" {
			detail.Title, detail.Kind = activity.Title, activity.Kind
		}
		if detail.ID == "" {
			detail.ID = activity.ID
		}
		if len(detail.Input) == 0 {
			detail.Input = append(detail.Input[:0], activity.Input...)
		}
	}
	return detail
}
