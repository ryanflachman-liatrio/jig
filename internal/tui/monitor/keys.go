package monitor

import (
	keybind "charm.land/bubbles/v2/key"
)

// monitorKeys covers the two-panel run monitor. Focus moves between the Steps
// panel, the Transcript panel, and (when present) the Gate; each region reads its
// own keys, and the gates are non-blocking (ADR 0002) so focus keys are handled
// before any region sees input. Bindings marked display-only are rendered in the
// footer but matched elsewhere — the digit gates (Verdict/Answer/ToggleOpt) run a
// loop that needs the option index, and the Focus labels combine directional
// bindings (e.g. "tab/←/→") that are matched individually.
type monitorKeys struct {
	// focus movement (handled in Update before region dispatch)
	FocusNext  keybind.Binding // matched (tab)
	FocusPrev  keybind.Binding // matched (shift+tab)
	PanelFocus keybind.Binding // matched (left/right → the two side panels)
	FocusHint  keybind.Binding // display-only ("tab focus", gate footers)
	FocusFull  keybind.Binding // display-only ("tab/←/→ focus", panel footers)

	// Steps panel
	Down           keybind.Binding // matched (j/down)
	Up             keybind.Binding // matched (k/up)
	StepsNav       keybind.Binding // display-only ("j/k select")
	OpenTranscript keybind.Binding // matched (enter/l → Transcript)
	ToggleTree     keybind.Binding // matched (space → expand/collapse file tree)
	StepsLeave     keybind.Binding // matched (esc/q/backspace/h → runs list)

	// Transcript panel
	TransToSteps keybind.Binding // matched (esc/h → Steps)
	TransLeave   keybind.Binding // matched (q → runs list)
	Scroll       keybind.Binding // matched (j/k — scroll viewport)
	BlockNav     keybind.Binding // matched (n/N — next/previous collapsible block)
	Toggle       keybind.Binding // matched (enter/space → expand)
	ExpandAll    keybind.Binding // matched (o)
	GotoTop      keybind.Binding // matched (gg chord — pendingGPrefix state in model)
	Follow       keybind.Binding // matched (f/G — resume follow at latest entry)
	Search       keybind.Binding // matched (/)
	Filters      keybind.Binding // matched (F)
	ClearView    keybind.Binding // matched (c — clear search and filters)
	PageOlder    keybind.Binding // matched ([)
	PageNewer    keybind.Binding // matched (])

	// gates
	Submit         keybind.Binding // matched (enter: input/prompt/compose submit)
	Newline        keybind.Binding // display-only (textarea-owned)
	GateBlur       keybind.Binding // matched (esc: blurs gate → Steps, all kinds; ADR 0005)
	GateContext    keybind.Binding // matched (ctrl+o: view/return from the active gate's step context)
	GateEntryNav   keybind.Binding // matched ([/] — previous/next entry, multi-entry queue)
	InputLeave     keybind.Binding // retained for legacy footer refs; no longer triggers ShowRunsMsg
	PromptLeave    keybind.Binding // display-only ("esc blur", prompt gate)
	ComposeCancel  keybind.Binding // matched (esc, while composing — caught by GateBlur at top)
	Message        keybind.Binding // matched (m, review gate)
	ReviewLeave    keybind.Binding // retained; esc caught by GateBlur; q unhandled until task 4
	Verdict        keybind.Binding // display-only ("1-9 verdict")
	Answer         keybind.Binding // display-only ("1-9 select answer")
	ToggleOpt      keybind.Binding // display-only ("1-9 toggle", multiSelect)
	QConfirm       keybind.Binding // matched (enter/space, multiSelect confirm)
	QuestionCancel keybind.Binding // retained; esc caught by GateBlur; q → task 4.5
	QuestionScroll keybind.Binding // display-only ("↑/↓ scroll", question option list overflow)

	RecoverRetry keybind.Binding // matched (r, recovery gate: re-run fresh)
	RecoverGuide keybind.Binding // matched (g, recovery gate: compose guidance + resume session)
	RecoverSkip  keybind.Binding // matched (s, recovery gate: accept failure and continue past it)
	RecoverAbort keybind.Binding // matched (a, recovery gate: fail the step and abort the run)

	IntegrationResolve keybind.Binding // matched (r, integration-conflict gate: finish the merge from the resolved run worktree)

	FinalMergeApprove keybind.Binding // matched (y, final-merge gate: land the run branch onto base)
	FinalMergeDiscard keybind.Binding // matched (d, final-merge gate: leave the run branch, merge nothing)

	// Steps panel — step lifecycle actions (spec 08 C4).
	// These are matched only in focusSteps and gated by step eligibility via
	// SetEnabled, so they never collide with the gate-focused RecoverRetry (r)
	// or IntegrationResolve (r) which are only active in focusGate.
	StopStep   keybind.Binding // matched (s, steps panel: cancel a running step)
	ResetStep  keybind.Binding // matched (r, steps panel: reset to a quiescent step)
	ResumeStep keybind.Binding // matched (ctrl+r, steps panel: resume a stopped step)

	ToggleHelp keybind.Binding // matched (ctrl+\: open/close the help agent modal)
}

func defaultMonitorKeys() monitorKeys {
	return monitorKeys{
		FocusNext:  keybind.NewBinding(keybind.WithKeys("tab"), keybind.WithHelp("tab", "focus")),
		FocusPrev:  keybind.NewBinding(keybind.WithKeys("shift+tab"), keybind.WithHelp("shift+tab", "prev focus")),
		PanelFocus: keybind.NewBinding(keybind.WithKeys("left", "right"), keybind.WithHelp("←/→", "focus")),
		FocusHint:  keybind.NewBinding(keybind.WithKeys("tab"), keybind.WithHelp("tab", "focus")),
		FocusFull:  keybind.NewBinding(keybind.WithKeys("tab", "left", "right"), keybind.WithHelp("tab/←/→", "focus")),

		Down:           keybind.NewBinding(keybind.WithKeys("j", "down"), keybind.WithHelp("↓/j", "down")),
		Up:             keybind.NewBinding(keybind.WithKeys("k", "up"), keybind.WithHelp("↑/k", "up")),
		StepsNav:       keybind.NewBinding(keybind.WithKeys("j", "k"), keybind.WithHelp("j/k", "select")),
		OpenTranscript: keybind.NewBinding(keybind.WithKeys("enter", "l"), keybind.WithHelp("enter", "transcript")),
		ToggleTree:     keybind.NewBinding(keybind.WithKeys("space"), keybind.WithHelp("space", "expand/collapse")),
		StepsLeave:     keybind.NewBinding(keybind.WithKeys("esc", "q", "backspace", "h"), keybind.WithHelp("esc", "runs list")),

		TransToSteps: keybind.NewBinding(keybind.WithKeys("esc", "h"), keybind.WithHelp("esc", "steps")),
		TransLeave:   keybind.NewBinding(keybind.WithKeys("q"), keybind.WithHelp("q", "runs list")),
		Scroll:       keybind.NewBinding(keybind.WithKeys("j", "k"), keybind.WithHelp("j/k", "scroll")),
		BlockNav:     keybind.NewBinding(keybind.WithKeys("n", "N"), keybind.WithHelp("n/N", "block")),
		Toggle:       keybind.NewBinding(keybind.WithKeys("enter", " "), keybind.WithHelp("enter", "expand")),
		ExpandAll:    keybind.NewBinding(keybind.WithKeys("o"), keybind.WithHelp("o", "all")),
		GotoTop:      keybind.NewBinding(keybind.WithKeys("g"), keybind.WithHelp("gg", "top")),
		Follow:       keybind.NewBinding(keybind.WithKeys("f", "G"), keybind.WithHelp("f/G", "follow")),
		Search:       keybind.NewBinding(keybind.WithKeys("/"), keybind.WithHelp("/", "search")),
		Filters:      keybind.NewBinding(keybind.WithKeys("F"), keybind.WithHelp("F", "filters")),
		ClearView:    keybind.NewBinding(keybind.WithKeys("c"), keybind.WithHelp("c", "clear")),
		PageOlder:    keybind.NewBinding(keybind.WithKeys("["), keybind.WithHelp("[", "older")),
		PageNewer:    keybind.NewBinding(keybind.WithKeys("]"), keybind.WithHelp("]", "newer")),

		Submit:         keybind.NewBinding(keybind.WithKeys("enter"), keybind.WithHelp("enter", "submit")),
		Newline:        keybind.NewBinding(keybind.WithKeys("alt+enter", "shift+enter"), keybind.WithHelp("alt+enter", "newline")),
		GateBlur:       keybind.NewBinding(keybind.WithKeys("esc"), keybind.WithHelp("esc", "blur")),
		GateContext:    keybind.NewBinding(keybind.WithKeys("ctrl+o"), keybind.WithHelp("ctrl+o", "view context")),
		GateEntryNav:   keybind.NewBinding(keybind.WithKeys("[", "]"), keybind.WithHelp("[/]", "entries")),
		InputLeave:     keybind.NewBinding(keybind.WithKeys("esc"), keybind.WithHelp("esc", "blur")),
		PromptLeave:    keybind.NewBinding(keybind.WithKeys("esc"), keybind.WithHelp("esc", "blur")),
		ComposeCancel:  keybind.NewBinding(keybind.WithKeys("esc"), keybind.WithHelp("esc", "blur")),
		Message:        keybind.NewBinding(keybind.WithKeys("m"), keybind.WithHelp("m", "message")),
		ReviewLeave:    keybind.NewBinding(keybind.WithKeys("esc", "q"), keybind.WithHelp("esc", "blur")),
		Verdict:        keybind.NewBinding(keybind.WithKeys("1", "2", "3", "4", "5", "6", "7", "8", "9"), keybind.WithHelp("1-9", "verdict")),
		Answer:         keybind.NewBinding(keybind.WithKeys("1", "2", "3", "4", "5", "6", "7", "8", "9"), keybind.WithHelp("1-9", "select answer")),
		ToggleOpt:      keybind.NewBinding(keybind.WithKeys("1", "2", "3", "4", "5", "6", "7", "8", "9"), keybind.WithHelp("1-9", "toggle")),
		QConfirm:       keybind.NewBinding(keybind.WithKeys("enter", " "), keybind.WithHelp("enter", "confirm")),
		QuestionCancel: keybind.NewBinding(keybind.WithKeys("esc", "q"), keybind.WithHelp("esc", "blur")),
		QuestionScroll: keybind.NewBinding(keybind.WithKeys("j", "k", "down", "up"), keybind.WithHelp("↑/↓", "scroll")),

		RecoverRetry: keybind.NewBinding(keybind.WithKeys("r"), keybind.WithHelp("r", "retry")),
		RecoverGuide: keybind.NewBinding(keybind.WithKeys("g"), keybind.WithHelp("g", "guide+retry")),
		RecoverSkip:  keybind.NewBinding(keybind.WithKeys("s"), keybind.WithHelp("s", "skip")),
		RecoverAbort: keybind.NewBinding(keybind.WithKeys("a"), keybind.WithHelp("a", "abort")),

		IntegrationResolve: keybind.NewBinding(keybind.WithKeys("r"), keybind.WithHelp("r", "resolve")),

		FinalMergeApprove: keybind.NewBinding(keybind.WithKeys("y"), keybind.WithHelp("y", "merge")),
		FinalMergeDiscard: keybind.NewBinding(keybind.WithKeys("d"), keybind.WithHelp("d", "discard")),

		StopStep:   keybind.NewBinding(keybind.WithKeys("s"), keybind.WithHelp("s", "stop")),
		ResetStep:  keybind.NewBinding(keybind.WithKeys("r"), keybind.WithHelp("r", "reset")),
		ResumeStep: keybind.NewBinding(keybind.WithKeys("ctrl+r"), keybind.WithHelp("ctrl+r", "resume")),

		ToggleHelp: keybind.NewBinding(keybind.WithKeys("ctrl+\\"), keybind.WithHelp("ctrl+\\", "help agent")),
	}
}
