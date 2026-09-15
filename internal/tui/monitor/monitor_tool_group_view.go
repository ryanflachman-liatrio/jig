package monitor

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"jig/internal/tui/shared"
)

type toolGroupRender struct {
	body   string
	ranges map[transcriptItemKey]lineRange
}

func (m *Model) renderCompactToolGroup(item transcriptItem, selected transcriptItemKey) toolGroupRender {
	policy, ok := compactToolGroupPolicy(item, m.chatEntries)
	if !ok {
		return toolGroupRender{}
	}
	expanded := m.chatItemExpandAll || m.chatItemExpand[item.key]
	groupSelected := selected == item.key
	glyph, iconStyle := shared.ToolStatusIcon(item.displayState, policy.kind)
	titleStyle := shared.Theme.Chat.ReadGroupTitle
	if groupSelected {
		titleStyle = shared.Theme.Chat.TranscriptSelected
	}
	header := shared.RenderStatusLine(shared.StatusLine{
		Icon:       glyph,
		IconStyle:  iconStyle,
		Title:      policy.title,
		TitleStyle: titleStyle,
		Meta:       []string{"(" + strconv.Itoa(len(item.groupMembers)) + ")"},
	})
	marker := shared.CollapsedMarker
	if expanded {
		marker = shared.ExpandedMarker
	}
	prefix := "  "
	if groupSelected {
		prefix = shared.Theme.SelectedBar.Render(shared.CursorBar) + " "
	}
	lines := []string{boundedToolGroupLine(prefix, marker+" "+header, m.transcriptInnerW)}
	ranges := map[transcriptItemKey]lineRange{item.key: {start: 0, end: 0}}
	if !expanded {
		rows := compactToolGroupRows(item, m.chatEntries)
		for i, row := range rows {
			connector := shared.Theme.Chat.ReadGroupConnector.Render(shared.TreePrefix(i == len(rows)-1))
			lines = append(lines, boundedToolGroupLine(prefix, "  "+connector+shared.Theme.Chat.ReadGroupTarget.Render(row.text), m.transcriptInnerW))
		}
		ranges[item.key] = lineRange{start: 0, end: len(lines) - 1}
		return toolGroupRender{body: strings.Join(lines, "\n"), ranges: ranges}
	}

	for i, member := range item.groupMembers {
		last := i == len(item.groupMembers)-1
		branch := "  " + shared.Theme.Chat.ReadGroupConnector.Render(shared.TreePrefix(last))
		continuation := "  " + shared.Theme.Chat.ReadGroupConnector.Render(shared.TreeContinuationPrefix(last))
		childWidth := max(0, m.transcriptInnerW-lipgloss.Width(branch))
		nested := *m
		nested.transcriptInnerW = childWidth
		var child strings.Builder
		nested.writeTranscriptItem(&child, member, selected == member.key)
		body := trimStructuralBlankEdges(child.String())
		if body == "" {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		start := len(lines)
		for row, childLine := range strings.Split(body, "\n") {
			childLine = strings.TrimPrefix(childLine, "  ")
			if row == 0 {
				lines = append(lines, branch+childLine)
			} else {
				lines = append(lines, continuation+childLine)
			}
		}
		ranges[member.key] = lineRange{start: start, end: len(lines) - 1}
	}
	return toolGroupRender{body: strings.Join(lines, "\n"), ranges: ranges}
}

func boundedToolGroupLine(prefix, content string, width int) string {
	available := width - lipgloss.Width(prefix)
	if available <= 0 {
		return ""
	}
	return prefix + shared.TruncateTitle(content, available)
}

func (m Model) renderedEndItemKey(item transcriptItem) transcriptItemKey {
	if item.kind == transcriptItemToolGroup && (m.chatItemExpandAll || m.chatItemExpand[item.key]) && len(item.groupMembers) > 0 {
		return item.groupMembers[len(item.groupMembers)-1].key
	}
	return item.key
}
