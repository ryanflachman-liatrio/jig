package monitor

import (
	"crypto/sha256"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/ryanflachman-liatrio/jig/internal/toolcall"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

type sourceWindowKey struct {
	owner   sourceOwner
	section int
}
type sourceWindowCache struct {
	raw            string
	path           string
	version        [sha256.Size]byte
	index          *sourceWindowIndex
	rendered       string
	renderedWindow int
}

// The Bubble Tea model owns these caches. Each loaded section retains only one
// width's index and one highlighted window; old widths/windows are replaced.
func (m *Model) sourceIndex(item transcriptItem, section int, d *toolcall.Diff) *sourceWindowCache {
	if m.sourceWindows == nil {
		m.sourceWindows = make(map[sourceWindowKey]*sourceWindowCache)
	}
	key := sourceWindowKey{m.sourceOwner(item), section}
	width := max(0, m.transcriptInnerW-4)
	cache := m.sourceWindows[key]
	if cache == nil || cache.raw != d.NewText || cache.path != d.Path || cache.index.width != width {
		cache = &sourceWindowCache{raw: d.NewText, path: d.Path, version: m.sourceVersion(item), index: indexSourceWindows(d.NewText, width), renderedWindow: -1}
		m.sourceWindows[key] = cache
	}
	return cache
}

func (m Model) selectedSource() (transcriptItem, int, bool) {
	item, ok := m.selectedTranscriptItem()
	if !ok || m.selKind == "file" {
		return item, 0, false
	}
	sections := m.sourceSections(item)
	state := m.sourceStates[m.sourceOwner(item)]
	if state.source {
		return item, state.section, state.section < len(sections)
	}
	// A single section already displaying fallback source can be browsed directly.
	if len(sections) == 1 {
		proj, _, outcome := m.recordedDiff(sections[0])
		return item, 0, outcome != computeOK || proj == nil || len(proj.Presentation.Hunks) == 0
	}
	return item, 0, false
}

func (m Model) browseSource(action string) Model {
	item, section, ok := m.selectedSource()
	if !ok {
		return m
	}
	// Navigation must measure the same inset as a compact group's child render.
	nested := m
	for _, group := range m.chatItems {
		for _, member := range group.groupMembers {
			if member.key == item.key {
				nested.transcriptInnerW = max(0, m.transcriptInnerW-lipgloss.Width("  "+shared.TreePrefix(false)))
			}
		}
	}
	if m.sourceWindows == nil {
		m.sourceWindows = make(map[sourceWindowKey]*sourceWindowCache)
		nested.sourceWindows = m.sourceWindows
	}
	idx := nested.sourceIndex(item, section, m.sourceSections(item)[section]).index
	if len(idx.windows) == 0 {
		return m
	}
	owner := m.sourceOwner(item)
	state, tracked := m.sourceStates[owner]
	if !tracked {
		state = sourcePresentation{source: true, section: section, version: m.sourceVersion(item)}
	}
	current := idx.at(state.offset)
	switch action {
	case "next":
		current = min(current+1, len(idx.windows)-1)
	case "previous":
		current = max(current-1, 0)
	case "first":
		current = 0
	case "last":
		current = len(idx.windows) - 1
	}
	state.offset = idx.windows[current].start
	if m.sourceStates == nil {
		m.sourceStates = make(map[sourceOwner]sourcePresentation)
	}
	m.sourceStates[owner] = state
	m.chatItemExpand[item.key] = true
	m.chatAutoScroll = false
	m.setChatContent()
	m.ensureTranscriptItemCursorVisible()
	return m
}

func (c *sourceWindowCache) render(window int, path string, highlight func(string, string) (string, error)) string {
	if c.renderedWindow != window {
		code := c.index.text(window)
		rendered, err := highlight(path, code)
		// Highlighting may decorate only: preserve blank rows and literal evidence.
		if err != nil || ansi.Strip(rendered) != code {
			rendered = code
		}
		c.rendered, c.renderedWindow = rendered, window
	}
	return c.rendered
}

func sourceWindowPosition(idx *sourceWindowIndex, window int) string {
	w := idx.windows[window]
	var suffix []string
	if w.continued {
		suffix = append(suffix, "continued")
	}
	if w.continues {
		suffix = append(suffix, "line continues")
	} else if window+1 < len(idx.windows) {
		suffix = append(suffix, "more below")
	} else {
		suffix = append(suffix, "end")
	}
	return strings.Join(suffix, " · ")
}
