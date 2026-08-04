package tui

import (
	"strings"

	keybind "charm.land/bubbles/v2/key"
)

// keys.go is the single home for every key.Binding in the TUI, mirroring the
// styles.go convention of one central definition per concern. Each screen owns a
// *Keys struct built by a default*Keys constructor; the model holds it as a
// `keys` field and matches input with keybind.Matches rather than comparing
// msg.String(). Footers render from the same bindings via hintString, so the
// hint line can never advertise a key the handler does not accept, nor omit one
// it does — the class of drift that produced the stale monitor freeze comment
// (see docs/bug-gate-navigation-freeze.md).
//
// Some bindings are display-only: a footer shows a combined label ("j/k",
// "1-9") whose per-key matching is done elsewhere (a viewport keymap, or a
// digit loop that needs the option index). Those carry help text for the footer
// but are never passed to keybind.Matches; they are called out inline.

// keyQuit is the global quit chord. The root model performs the actual quit
// (chatModel disconnects first); screens list it in their footer so the binding
// and its advertised help live in one place.
var keyQuit = keybind.NewBinding(
	keybind.WithKeys("ctrl+c"),
	keybind.WithHelp("ctrl+c", "quit"),
)

// hintString renders the enabled bindings into the footer's "k desc  •  k desc"
// hint format. Disabled bindings (SetEnabled(false)) are skipped, so a hint can
// never outlive the key it describes.
func hintString(bindings ...keybind.Binding) string {
	parts := make([]string, 0, len(bindings))
	for _, b := range bindings {
		if !b.Enabled() {
			continue
		}
		h := b.Help()
		parts = append(parts, h.Key+" "+h.Desc)
	}
	return strings.Join(parts, "  •  ")
}

// ── selector ─────────────────────────────────────────────────────────────────

// selectorKeys covers the workflow picker. Navigation and filtering are owned by
// the embedded bubbles list; Nav/Filter/Apply/Clear are display-only so the
// footer stays honest about what the list accepts, while Open is the one binding
// the selector itself matches.
type selectorKeys struct {
	Nav    keybind.Binding // display-only (list-owned)
	Filter keybind.Binding // display-only (list-owned)
	Open   keybind.Binding // matched
	Apply  keybind.Binding // display-only (list-owned, filtering)
	Clear  keybind.Binding // display-only (list-owned, filtering)
}

func defaultSelectorKeys() selectorKeys {
	return selectorKeys{
		Nav:    keybind.NewBinding(keybind.WithKeys("up", "down", "k", "j"), keybind.WithHelp("↑/↓", "navigate")),
		Filter: keybind.NewBinding(keybind.WithKeys("/"), keybind.WithHelp("/", "filter")),
		Open:   keybind.NewBinding(keybind.WithKeys("enter"), keybind.WithHelp("enter", "open")),
		Apply:  keybind.NewBinding(keybind.WithKeys("enter"), keybind.WithHelp("enter", "apply")),
		Clear:  keybind.NewBinding(keybind.WithKeys("esc"), keybind.WithHelp("esc", "clear filter")),
	}
}

// ── detail ───────────────────────────────────────────────────────────────────

// detailKeys covers the read-only workflow view. Run is disabled until a valid
// workflow loads, so it both stops matching and drops out of the footer.
type detailKeys struct {
	Run  keybind.Binding // matched (SetEnabled on wf presence)
	Runs keybind.Binding // matched
	Back keybind.Binding // matched
}

func defaultDetailKeys() detailKeys {
	return detailKeys{
		Run:  keybind.NewBinding(keybind.WithKeys("r"), keybind.WithHelp("r", "run")),
		Runs: keybind.NewBinding(keybind.WithKeys("enter"), keybind.WithHelp("enter", "runs")),
		Back: keybind.NewBinding(keybind.WithKeys("esc", "q", "backspace", "h", "left"), keybind.WithHelp("esc", "back")),
	}
}

// ── runs ─────────────────────────────────────────────────────────────────────

// runsKeys covers the run list. Up/Down are matched but not shown (the footer
// stays as it was, advertising the actions rather than the cursor keys).
type runsKeys struct {
	Up     keybind.Binding // matched
	Down   keybind.Binding // matched
	Open   keybind.Binding // matched
	NewRun keybind.Binding // matched
	Back   keybind.Binding // matched
}

func defaultRunsKeys() runsKeys {
	return runsKeys{
		Up:     keybind.NewBinding(keybind.WithKeys("up", "k"), keybind.WithHelp("↑/k", "up")),
		Down:   keybind.NewBinding(keybind.WithKeys("down", "j"), keybind.WithHelp("↓/j", "down")),
		Open:   keybind.NewBinding(keybind.WithKeys("enter"), keybind.WithHelp("enter", "monitor")),
		NewRun: keybind.NewBinding(keybind.WithKeys("r"), keybind.WithHelp("r", "new run")),
		Back:   keybind.NewBinding(keybind.WithKeys("esc", "q", "backspace", "h", "left"), keybind.WithHelp("esc", "back")),
	}
}

// ── chat ─────────────────────────────────────────────────────────────────────

// chatKeys covers the standalone streaming chat. ToOutput (esc) and FocusInput
// (i) are matched separately because they move focus in opposite directions;
// SwitchFocus is a display-only binding that labels the pair in the footer.
// Newline is display-only (the textarea owns alt+enter).
type chatKeys struct {
	Send        keybind.Binding // matched
	Newline     keybind.Binding // display-only (textarea-owned)
	SwitchFocus keybind.Binding // display-only (esc/i pair)
	ToOutput    keybind.Binding // matched (esc)
	FocusInput  keybind.Binding // matched (i)
	PrevTurn    keybind.Binding // matched (left)
	NextTurn    keybind.Binding // matched (right)
	Quit        keybind.Binding // matched (ctrl+c: disconnect then quit)
}

func defaultChatKeys() chatKeys {
	return chatKeys{
		Send:        keybind.NewBinding(keybind.WithKeys("enter"), keybind.WithHelp("enter", "send")),
		Newline:     keybind.NewBinding(keybind.WithKeys("alt+enter", "shift+enter"), keybind.WithHelp("alt+enter", "newline")),
		SwitchFocus: keybind.NewBinding(keybind.WithKeys("esc", "i"), keybind.WithHelp("esc/i", "switch focus")),
		ToOutput:    keybind.NewBinding(keybind.WithKeys("esc"), keybind.WithHelp("esc", "output")),
		FocusInput:  keybind.NewBinding(keybind.WithKeys("i"), keybind.WithHelp("i", "input")),
		PrevTurn:    keybind.NewBinding(keybind.WithKeys("left"), keybind.WithHelp("←", "prev turn")),
		NextTurn:    keybind.NewBinding(keybind.WithKeys("right"), keybind.WithHelp("→", "next turn")),
		Quit:        keybind.NewBinding(keybind.WithKeys("ctrl+c"), keybind.WithHelp("ctrl+c", "quit")),
	}
}

// ── monitor ──────────────────────────────────────────────────────────────────

// monitorKeys covers the run monitor, whose key surface is state-dependent: a
// list/chat navigation split plus four human-in-the-loop gates. Bindings marked
// display-only are rendered in the footer but matched elsewhere — the digit
// gates (Verdict/Answer/ToggleOpt) run a loop that needs the option index, and
// ListNav/Scroll label the viewport's own j/k handling.
//
// NOTE: PromptLeave is display-only. The from="user" prompt gate advertises
// "esc runs list" but the handler currently routes esc to the textarea (it does
// not leave). That pre-existing discrepancy is left intact by this mechanical
// sweep and is slated for the monitor FSM cleanup — see
// docs/bug-gate-navigation-freeze.md.
type monitorKeys struct {
	// modeList navigation
	Up      keybind.Binding // matched (k/up)
	Down    keybind.Binding // matched (j/down)
	ListNav keybind.Binding // display-only ("j/k select")
	Open    keybind.Binding // matched (enter)
	Back    keybind.Binding // matched (esc/q/backspace/h/left → runs list)

	// modeChat
	ChatBack  keybind.Binding // matched (esc/h/left)
	Scroll    keybind.Binding // display-only ("j/k scroll")
	NextBlock keybind.Binding // matched (tab)
	PrevBlock keybind.Binding // matched (shift+tab)
	Toggle    keybind.Binding // matched (enter/space → expand)
	ExpandAll keybind.Binding // matched (o)

	// gates
	Submit         keybind.Binding // matched (enter: input/prompt/compose submit)
	Newline        keybind.Binding // display-only (textarea-owned)
	InputLeave     keybind.Binding // matched (esc, block_on gate)
	PromptLeave    keybind.Binding // display-only (see NOTE above)
	ComposeCancel  keybind.Binding // matched (esc, while composing)
	Message        keybind.Binding // matched (m, review gate)
	ReviewLeave    keybind.Binding // matched (esc/q, review gate)
	Verdict        keybind.Binding // display-only ("1-9 select verdict")
	Answer         keybind.Binding // display-only ("1-9 select answer")
	ToggleOpt      keybind.Binding // display-only ("1-9 toggle", multiSelect)
	QConfirm       keybind.Binding // matched (enter/space, multiSelect confirm)
	QuestionCancel keybind.Binding // matched (esc/q, question gate)
}

func defaultMonitorKeys() monitorKeys {
	return monitorKeys{
		Up:      keybind.NewBinding(keybind.WithKeys("k", "up"), keybind.WithHelp("↑/k", "up")),
		Down:    keybind.NewBinding(keybind.WithKeys("j", "down"), keybind.WithHelp("↓/j", "down")),
		ListNav: keybind.NewBinding(keybind.WithKeys("j", "k"), keybind.WithHelp("j/k", "select")),
		Open:    keybind.NewBinding(keybind.WithKeys("enter"), keybind.WithHelp("enter", "open")),
		Back:    keybind.NewBinding(keybind.WithKeys("esc", "q", "backspace", "h", "left"), keybind.WithHelp("esc", "runs list")),

		ChatBack:  keybind.NewBinding(keybind.WithKeys("esc", "h", "left"), keybind.WithHelp("esc", "back")),
		Scroll:    keybind.NewBinding(keybind.WithKeys("j", "k"), keybind.WithHelp("j/k", "scroll")),
		NextBlock: keybind.NewBinding(keybind.WithKeys("tab"), keybind.WithHelp("tab", "block")),
		PrevBlock: keybind.NewBinding(keybind.WithKeys("shift+tab"), keybind.WithHelp("shift+tab", "prev block")),
		Toggle:    keybind.NewBinding(keybind.WithKeys("enter", " "), keybind.WithHelp("enter", "expand")),
		ExpandAll: keybind.NewBinding(keybind.WithKeys("o"), keybind.WithHelp("o", "all")),

		Submit:         keybind.NewBinding(keybind.WithKeys("enter"), keybind.WithHelp("enter", "submit")),
		Newline:        keybind.NewBinding(keybind.WithKeys("alt+enter", "shift+enter"), keybind.WithHelp("alt+enter", "newline")),
		InputLeave:     keybind.NewBinding(keybind.WithKeys("esc"), keybind.WithHelp("esc", "runs list")),
		PromptLeave:    keybind.NewBinding(keybind.WithKeys("esc"), keybind.WithHelp("esc", "runs list")),
		ComposeCancel:  keybind.NewBinding(keybind.WithKeys("esc"), keybind.WithHelp("esc", "cancel")),
		Message:        keybind.NewBinding(keybind.WithKeys("m"), keybind.WithHelp("m", "message")),
		ReviewLeave:    keybind.NewBinding(keybind.WithKeys("esc", "q"), keybind.WithHelp("esc", "runs list")),
		Verdict:        keybind.NewBinding(keybind.WithKeys("1", "2", "3", "4", "5", "6", "7", "8", "9"), keybind.WithHelp("1-9", "select verdict")),
		Answer:         keybind.NewBinding(keybind.WithKeys("1", "2", "3", "4", "5", "6", "7", "8", "9"), keybind.WithHelp("1-9", "select answer")),
		ToggleOpt:      keybind.NewBinding(keybind.WithKeys("1", "2", "3", "4", "5", "6", "7", "8", "9"), keybind.WithHelp("1-9", "toggle")),
		QConfirm:       keybind.NewBinding(keybind.WithKeys("enter", " "), keybind.WithHelp("enter", "confirm")),
		QuestionCancel: keybind.NewBinding(keybind.WithKeys("esc", "q"), keybind.WithHelp("esc", "runs list")),
	}
}
