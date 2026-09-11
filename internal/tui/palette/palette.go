// Package palette implements the k9s-style filterable command palette (ctrl+k).
// It lists currently-enabled actions with their key equivalents and, on enter,
// re-dispatches the same key path the binding would have taken.
package palette

import (
	"strings"

	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"

	"jig/internal/tui/shared"
)

// Command is one runnable palette entry. Key is the first chord of Binding and
// is what Execute re-injects so the screen's normal handler runs. Run, when
// set, is executed directly on enter instead of re-dispatching Key — the path
// for named actions with no corresponding single keypress on the current
// screen.
type Command struct {
	ID       string
	Title    string
	Binding  string // display only ("ctrl+r", "r", …)
	Key      string // first key chord to re-dispatch; empty = title-only / no-op
	Category string // section/prefix label, included in fuzzy match text
	Run      func() tea.Cmd
	Enabled  bool
}

// Model is the centered filterable overlay.
type Model struct {
	open     bool
	filter   string
	cursor   int
	commands []Command
	visible  []Command
	// matches holds the fuzzy-matched rune indexes within each visible
	// command's Title, keyed by Command.ID, for View's highlighting. Empty
	// (nil) when the filter is empty — the catalog falls back to original order.
	matches map[string][]int
}

func New() Model { return Model{} }

func (m Model) Open() bool { return m.open }

// Show opens the palette with the given catalog (disabled entries are dropped).
func (m Model) Show(commands []Command) Model {
	enabled := make([]Command, 0, len(commands))
	for _, c := range commands {
		if c.Enabled && c.Title != "" {
			enabled = append(enabled, c)
		}
	}
	m.open = true
	m.filter = ""
	m.cursor = 0
	m.commands = enabled
	m.visible = enabled
	return m
}

func (m Model) Hide() Model {
	m.open = false
	m.filter = ""
	m.cursor = 0
	m.visible = nil
	m.matches = nil
	return m
}

// Selected returns the cursor command when open, or false.
func (m Model) Selected() (Command, bool) {
	if !m.open || m.cursor < 0 || m.cursor >= len(m.visible) {
		return Command{}, false
	}
	return m.visible[m.cursor], true
}

// Update handles palette-local keys while open. Returns (model, runCmd, consumed).
// When runCmd is non-nil the caller should Hide and execute it (typically by
// re-dispatching a KeyPressMsg).
func (m Model) Update(msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	if !m.open {
		return m, nil, false
	}
	switch msg.String() {
	case "esc":
		return m.Hide(), nil, true
	case "enter":
		cmd, ok := m.Selected()
		m = m.Hide()
		if !ok {
			return m, nil, true
		}
		if cmd.Run != nil {
			return m, cmd.Run(), true
		}
		if cmd.Key == "" {
			return m, nil, true
		}
		return m, DispatchKey(cmd.Key), true
	case "up", "ctrl+p":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil, true
	case "down", "ctrl+n":
		if m.cursor < len(m.visible)-1 {
			m.cursor++
		}
		return m, nil, true
	case "backspace":
		if m.filter != "" {
			r := []rune(m.filter)
			m.filter = string(r[:len(r)-1])
			m.refilter()
		}
		return m, nil, true
	default:
		// Printable text always filters; arrows alone move the cursor so j/k
		// remain available as search characters (k9s-style).
		if msg.Text != "" && !msg.Mod.Contains(tea.ModCtrl) && !msg.Mod.Contains(tea.ModAlt) {
			m.filter += msg.Text
			m.refilter()
			return m, nil, true
		}
	}
	return m, nil, true
}

// refilter ranks m.commands by fuzzy match score against m.filter (best match
// first), matching against each command's title, key binding, and category.
// An empty filter falls back to the full catalog in its original order.
func (m *Model) refilter() {
	q := strings.TrimSpace(m.filter)
	if q == "" {
		m.visible = append([]Command(nil), m.commands...)
		m.matches = nil
		if m.cursor >= len(m.visible) {
			m.cursor = max(len(m.visible)-1, 0)
		}
		return
	}
	sources := make([]string, len(m.commands))
	for i, c := range m.commands {
		sources[i] = c.Title + " " + c.Binding + " " + c.Category
	}
	found := fuzzy.Find(q, sources)
	next := make([]Command, 0, len(found))
	matches := make(map[string][]int, len(found))
	for _, fm := range found {
		c := m.commands[fm.Index]
		next = append(next, c)
		titleLen := len([]rune(c.Title))
		idx := make([]int, 0, len(fm.MatchedIndexes))
		for _, i := range fm.MatchedIndexes {
			if i < titleLen {
				idx = append(idx, i)
			}
		}
		matches[c.ID] = idx
	}
	m.visible = next
	m.matches = matches
	if m.cursor >= len(m.visible) {
		m.cursor = max(len(m.visible)-1, 0)
	}
}

// highlightMatches wraps the runes at indexes (rune positions into title) in
// the match-highlight style, leaving the rest of the title unstyled.
func highlightMatches(title string, indexes []int) string {
	if len(indexes) == 0 {
		return title
	}
	set := make(map[int]bool, len(indexes))
	for _, i := range indexes {
		set[i] = true
	}
	var b strings.Builder
	for i, r := range []rune(title) {
		if set[i] {
			b.WriteString(shared.Theme.Help.Match.Render(string(r)))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// View renders the centered Commands box over base.
func (m Model) View(base string, width, height int) string {
	if !m.open {
		return base
	}
	const boxW = 48
	var b strings.Builder
	b.WriteString(shared.Theme.Help.Title.Render("Commands") + "\n")
	filterLine := "> " + m.filter
	if m.filter == "" {
		filterLine = "> "
	}
	b.WriteString(shared.Theme.Accent.Render(filterLine) + "\n")
	if len(m.visible) == 0 {
		b.WriteString(shared.Theme.Question.Render("  no matching commands") + "\n")
	} else {
		keyW := 0
		for _, c := range m.visible {
			if w := lipgloss.Width(c.Binding); w > keyW {
				keyW = w
			}
		}
		maxRows := 10
		start := 0
		if m.cursor >= maxRows {
			start = m.cursor - maxRows + 1
		}
		end := min(start+maxRows, len(m.visible))
		for i := start; i < end; i++ {
			c := m.visible[i]
			titleText := c.Title
			if idx := m.matches[c.ID]; len(idx) > 0 {
				titleText = highlightMatches(c.Title, idx)
			}
			title := shared.PadRight(titleText, len([]rune(c.Title)), 28)
			key := shared.PadRight(c.Binding, keyW)
			row := "  " + title + "  " + shared.Theme.Help.Key.Render(key)
			if i == m.cursor {
				row = shared.Theme.SelectedLine.Render("› " + title + "  " + key)
			}
			b.WriteString(ansi.Truncate(row, boxW-4, "") + "\n")
		}
	}
	b.WriteString("\n" + shared.Theme.Help.Desc.Render("type to filter · enter run · esc"))
	box := shared.Theme.Help.Box.Width(boxW).Render(b.String())

	x := (width - lipgloss.Width(box)) / 2
	y := (height - lipgloss.Height(box)) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	comp := lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	)
	return lipgloss.NewCanvas(width, height).Compose(comp).Render()
}

// FromBindings builds palette commands from help bindings. Disabled bindings
// are omitted. The first Keys() entry is the re-dispatch chord.
func FromBindings(prefix string, bindings []keybind.Binding) []Command {
	out := make([]Command, 0, len(bindings))
	for i, b := range bindings {
		if !b.Enabled() {
			continue
		}
		h := b.Help()
		keys := b.Keys()
		key := ""
		if len(keys) > 0 {
			key = keys[0]
		}
		id := prefix + ":" + h.Key + ":" + h.Desc
		if h.Desc == "" {
			id = prefix + ":" + h.Key + ":" + string(rune('a'+i))
		}
		out = append(out, Command{
			ID:       id,
			Title:    h.Desc,
			Binding:  h.Key,
			Key:      key,
			Category: prefix,
			Enabled:  true,
		})
	}
	return out
}

// DispatchKey returns a Cmd that emits a KeyPressMsg for chord (e.g. "r", "ctrl+r", "esc").
func DispatchKey(chord string) tea.Cmd {
	return func() tea.Msg {
		return ParseKey(chord)
	}
}

// ParseKey builds a KeyPressMsg from a binding chord string.
func ParseKey(chord string) tea.KeyPressMsg {
	switch chord {
	case "esc", "escape":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "enter", "return":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "space", " ":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	if strings.HasPrefix(chord, "ctrl+shift+") && len(chord) > 11 {
		r := []rune(strings.TrimPrefix(chord, "ctrl+shift+"))
		if len(r) == 1 {
			return tea.KeyPressMsg{Code: r[0], Mod: tea.ModCtrl | tea.ModShift, Text: string(r)}
		}
	}
	if strings.HasPrefix(chord, "ctrl+") && len(chord) > 5 {
		r := []rune(strings.TrimPrefix(chord, "ctrl+"))
		if len(r) == 1 {
			return tea.KeyPressMsg{Code: r[0], Mod: tea.ModCtrl, Text: string(r)}
		}
	}
	if strings.HasPrefix(chord, "alt+") && len(chord) > 4 {
		r := []rune(strings.TrimPrefix(chord, "alt+"))
		if len(r) == 1 {
			return tea.KeyPressMsg{Code: r[0], Mod: tea.ModAlt, Text: string(r)}
		}
	}
	r := []rune(chord)
	if len(r) == 1 {
		return tea.KeyPressMsg{Code: r[0], Text: chord}
	}
	// Multi-rune display help like "j/k" or "1-9" — fire the first rune.
	if strings.Contains(chord, "/") {
		first := strings.SplitN(chord, "/", 2)[0]
		return ParseKey(first)
	}
	if len(r) > 0 {
		return tea.KeyPressMsg{Code: r[0], Text: string(r[0])}
	}
	return tea.KeyPressMsg{}
}
