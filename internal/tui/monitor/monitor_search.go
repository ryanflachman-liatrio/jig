package monitor

import (
	"fmt"
	"strings"

	"jig/internal/transcript"
)

type blockRef struct {
	entry int
	block int
}

var filterLabels = [...]string{
	"errors",
	"tools",
	"reasoning",
	"retries",
	"role:assistant",
	"role:user",
	"role:system",
	"role:result",
}

func (m Model) filteredEntries() []transcript.Entry {
	if !m.filters.active() {
		return m.chatEntries
	}

	visible := make(map[blockKey]bool)
	var pending []blockRef
	flushTools := func() {
		if len(pending) == 0 {
			return
		}
		matches := false
		for _, ref := range pending {
			e := m.chatEntries[ref.entry]
			blk := e.Blocks[ref.block]
			if entryMatchesScope(e, m.filters) && contentMatches(blk, m.filters) {
				matches = true
				break
			}
		}
		if matches {
			for _, ref := range pending {
				e := m.chatEntries[ref.entry]
				visible[blockKey{seq: e.Seq, block: ref.block}] = true
			}
		}
		pending = nil
	}

	for ei, e := range m.chatEntries {
		for bi, blk := range e.Blocks {
			switch blk.Type {
			case transcript.BlockToolUse, transcript.BlockToolResult:
				pending = append(pending, blockRef{entry: ei, block: bi})
			default:
				flushTools()
				if entryMatchesScope(e, m.filters) && contentMatches(blk, m.filters) {
					visible[blockKey{seq: e.Seq, block: bi}] = true
				}
			}
		}
	}
	flushTools()

	filtered := make([]transcript.Entry, 0, len(m.chatEntries))
	for _, e := range m.chatEntries {
		copyEntry := e
		copyEntry.Blocks = make([]transcript.Block, len(e.Blocks))
		visibleCount := 0
		for bi, blk := range e.Blocks {
			if visible[blockKey{seq: e.Seq, block: bi}] {
				copyEntry.Blocks[bi] = blk
				visibleCount++
			}
		}
		if visibleCount > 0 {
			filtered = append(filtered, copyEntry)
		}
	}
	return filtered
}

func entryMatchesScope(e transcript.Entry, filters transcriptFilters) bool {
	if filters.retries && e.Attempt <= 0 {
		return false
	}
	rolesActive := filters.assistant || filters.user || filters.system || filters.result
	if !rolesActive {
		return true
	}
	switch e.Role {
	case transcript.RoleAssistant:
		return filters.assistant
	case transcript.RoleUser:
		return filters.user
	case transcript.RoleSystem:
		return filters.system
	case transcript.RoleResult:
		return filters.result
	default:
		return false
	}
}

func contentMatches(blk transcript.Block, filters transcriptFilters) bool {
	kindsActive := filters.errors || filters.tools || filters.reasoning
	if !kindsActive {
		return true
	}
	switch blk.Type {
	case transcript.BlockThinking:
		return filters.reasoning
	case transcript.BlockToolUse:
		return filters.tools
	case transcript.BlockToolResult:
		return filters.tools || (filters.errors && blk.IsError)
	default:
		return false
	}
}

func (m *Model) toggleCurrentFilter() {
	switch m.filterCursor {
	case 0:
		m.filters.errors = !m.filters.errors
	case 1:
		m.filters.tools = !m.filters.tools
	case 2:
		m.filters.reasoning = !m.filters.reasoning
	case 3:
		m.filters.retries = !m.filters.retries
	case 4:
		m.filters.assistant = !m.filters.assistant
	case 5:
		m.filters.user = !m.filters.user
	case 6:
		m.filters.system = !m.filters.system
	case 7:
		m.filters.result = !m.filters.result
	}
	m.rebuildLoadedChat(chatItem{})
	m.rerunSearch()
}

func (m Model) filterEnabled(index int) bool {
	switch index {
	case 0:
		return m.filters.errors
	case 1:
		return m.filters.tools
	case 2:
		return m.filters.reasoning
	case 3:
		return m.filters.retries
	case 4:
		return m.filters.assistant
	case 5:
		return m.filters.user
	case 6:
		return m.filters.system
	case 7:
		return m.filters.result
	default:
		return false
	}
}

func (m Model) filterSummary() string {
	var active []string
	for i, label := range filterLabels {
		if m.filterEnabled(i) {
			active = append(active, label)
		}
	}
	return strings.Join(active, ", ")
}

func (m *Model) clearTranscriptView() {
	m.searchOpen = false
	m.searchQuery = ""
	m.searchHits = nil
	m.searchHitCursor = 0
	m.filterOpen = false
	m.filters = transcriptFilters{}
	m.rebuildLoadedChat(chatItem{})
}

func (m *Model) rerunSearch() {
	query := strings.TrimSpace(m.searchQuery)
	m.searchHits = nil
	m.searchHitCursor = 0
	if query == "" {
		return
	}

	needle := strings.ToLower(query)
	for _, e := range m.filteredEntries() {
		for bi, blk := range e.Blocks {
			text := searchableBlockText(blk)
			if strings.Contains(strings.ToLower(text), needle) {
				m.searchHits = append(m.searchHits, searchHit{
					key:     blockKey{seq: e.Seq, block: bi},
					preview: searchPreview(text, needle),
				})
			}
		}
	}
}

func searchableBlockText(blk transcript.Block) string {
	switch blk.Type {
	case transcript.BlockText, transcript.BlockThinking:
		return blk.Text
	case transcript.BlockToolUse:
		return blk.Name + " " + string(blk.Input)
	case transcript.BlockToolResult:
		return blk.Content
	default:
		return strings.TrimSpace(blk.Text + " " + blk.Name + " " + string(blk.Input) + " " + blk.Content)
	}
}

func searchPreview(text, lowerNeedle string) string {
	flat := strings.Join(strings.Fields(text), " ")
	if flat == "" {
		return "(empty block)"
	}
	index := strings.Index(strings.ToLower(flat), lowerNeedle)
	if index < 0 {
		index = 0
	}
	start := max(0, index-24)
	if start > 0 {
		flat = clampRunesTail(flat[start:])
	}
	const maxRunes = 72
	runes := []rune(flat)
	if len(runes) > maxRunes {
		flat = string(runes[:maxRunes]) + "…"
	}
	if start > 0 {
		flat = "…" + flat
	}
	return flat
}

func (m Model) currentSearchHit() (searchHit, bool) {
	if len(m.searchHits) == 0 || m.searchHitCursor < 0 || m.searchHitCursor >= len(m.searchHits) {
		return searchHit{}, false
	}
	return m.searchHits[m.searchHitCursor], true
}

func (m *Model) applyCurrentSearchHit() {
	hit, ok := m.currentSearchHit()
	if !ok {
		return
	}
	if group, grouped := m.chatGroupForBlock[hit.key]; grouped {
		m.chatGroupExpand[group] = true
	}
	m.chatExpand[hit.key] = true
	m.rebuildActiveState(chatItem{key: hit.key})
	m.chatAutoScroll = false
	m.refreshPanels()
	m.ensureCurrentSearchHitVisible()
}

func (m *Model) moveSearchHit(delta int) {
	if len(m.searchHits) == 0 {
		return
	}
	m.searchHitCursor = (m.searchHitCursor + delta + len(m.searchHits)) % len(m.searchHits)
	m.applyCurrentSearchHit()
}

func (m *Model) ensureCurrentSearchHitVisible() {
	if !m.ready {
		return
	}
	hit, ok := m.currentSearchHit()
	if !ok {
		return
	}
	rng, ok := m.chatLineRanges[hit.key]
	if !ok {
		return
	}
	const margin = 2
	top := m.chatVP.YOffset()
	bottom := top + m.chatVP.Height() - 1
	switch {
	case rng.start-margin < top:
		m.chatVP.SetYOffset(max(0, rng.start-margin))
	case rng.end+margin > bottom:
		m.chatVP.SetYOffset(max(0, rng.end+margin-m.chatVP.Height()+1))
	}
}

func (m Model) searchStatus() string {
	if m.searchQuery == "" {
		return ""
	}
	if len(m.searchHits) == 0 {
		return fmt.Sprintf("/%s · no matches", m.searchQuery)
	}
	return fmt.Sprintf("/%s · %d/%d blocks", m.searchQuery, m.searchHitCursor+1, len(m.searchHits))
}
