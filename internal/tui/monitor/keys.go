package monitor

import (
	keybind "charm.land/bubbles/v2/key"

	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

// monitorKeys covers the two-panel run monitor. Focus moves between the Steps
// panel, the Transcript panel, and (when present) the Gate; each region reads its
// own keys, and the gates are non-blocking (ADR 0002) so focus keys are handled
// before any region sees input. Bindings marked display-only are rendered in the
// footer but matched elsewhere — Verdict runs a loop that needs the option index,
// and the Focus labels combine directional bindings (e.g. "tab/←/→") that are
// matched individually.
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
	StepsLeave     keybind.Binding // matched (esc/q → Home)

	// Transcript panel
	TransToSteps keybind.Binding // matched (esc → Steps)
	TransLeave   keybind.Binding // matched (q → Home)
	Scroll       keybind.Binding // matched (j/k — scroll viewport)
	ScrollFast   keybind.Binding // matched (J/K — scroll viewport by 10 rows)
	BlockNav     keybind.Binding // matched (n/N — next/previous collapsible block)
	Toggle       keybind.Binding // matched (enter/space → expand)
	ExpandAll    keybind.Binding // matched (o)
	GotoTop      keybind.Binding // matched (gg chord — pendingGPrefix state in model)
	Follow       keybind.Binding // matched (f/G — resume follow at latest entry)
	Search       keybind.Binding // matched (/)
	Filters      keybind.Binding // matched (F)
	CompactTools keybind.Binding // matched (c — compact eligible non-read tools)
	ToggleBell   keybind.Binding // matched (B — session gate-bell toggle, Steps/Transcript)
	ClearView    keybind.Binding // matched (x — clear search and filters)
	PageOlder    keybind.Binding // matched ([)
	PageNewer    keybind.Binding // matched (])

	// gates
	Submit       keybind.Binding // matched (enter: input/prompt/compose submit)
	Newline      keybind.Binding // display-only (textarea-owned)
	GateBack     keybind.Binding // display-only (esc: leaves a nested form)
	GateBlur     keybind.Binding // matched (esc: blurs a top-level gate → Steps)
	GateContext  keybind.Binding // matched (ctrl+o: view/return from the active gate's step context)
	GateEntryNav keybind.Binding // matched ([/] — previous/next entry, multi-entry queue)
	ReviewOpen   keybind.Binding // matched (enter: open a document review workspace)
	Verdict      keybind.Binding // display-only ("1-9 decision")

	RecoverRetry keybind.Binding // matched (r, recovery gate: re-run fresh)
	RecoverGuide keybind.Binding // matched (g, recovery gate: compose guidance + resume session)
	RecoverSkip  keybind.Binding // matched (s, recovery gate: accept failure and continue past it)
	RecoverAbort keybind.Binding // matched (a, recovery gate: fail the step and abort the run)

	IntegrationResolve keybind.Binding // matched (r, integration-conflict gate: finalize a staged resolution)
	IntegrationAgent   keybind.Binding // matched (g, integration-conflict gate: ask an agent for a proposal)

	FinalMergeApprove keybind.Binding // matched (y, final-merge gate: land the run branch onto base)
	FinalMergeDiscard keybind.Binding // matched (d, final-merge gate: leave the run branch, merge nothing)
	ResetConfirm      keybind.Binding // matched (y, reset-confirm gate)
	ResetCancel       keybind.Binding // matched (n, reset-confirm gate)

	// Steps panel — step lifecycle actions (spec 08 C4).
	// These are matched only in focusSteps and gated by step eligibility via
	// SetEnabled, so they never collide with the gate-focused RecoverRetry (r)
	// or IntegrationResolve (r) which are only active in focusGate.
	StopStep   keybind.Binding // matched (s, steps panel: cancel a running step)
	ResetStep  keybind.Binding // matched (r, steps panel: reset to a quiescent step)
	ResumeStep keybind.Binding // matched (ctrl+r, steps panel: resume a stopped step)

	ToggleHelp   keybind.Binding // matched (ctrl+\: open/close the help agent modal)
	ToggleSimple keybind.Binding // matched (ctrl+shift+a: simple/advanced chrome)

	// NotificationDiagnostics opens/closes the bounded diagnostics overlay
	// (spec 23-spec-run-notifications FR-17). Command-palette discoverable via
	// PaletteSections; never auto-opened by an arriving diagnostic.
	NotificationDiagnostics keybind.Binding // matched (ctrl+g)

	// Copy actions (spec 23-spec-clipboard-yank).
	// CopyItem (y) copies the current transcript item / tool exchange.
	// CopyAll (Y) copies the current source in full: the file for a file view,
	// the full recorded transcript for a step's messages.
	CopyItem keybind.Binding
	CopyAll  keybind.Binding
}

// defaultMonitorKeys is the effective keymap: the built-in bindings with the
// validated [keys] overrides applied.
func defaultMonitorKeys() monitorKeys {
	k := baseMonitorKeys()
	shared.ApplyKeymap(k.actions())
	return k
}

// Actions lists the Monitor's named actions over the built-in bindings, for
// keymap validation.
func Actions() []shared.Action {
	k := baseMonitorKeys()
	return k.actions()
}

func baseMonitorKeys() monitorKeys {
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
		StepsLeave:     keybind.NewBinding(keybind.WithKeys("esc", "q"), keybind.WithHelp("esc/q", "home")),

		TransToSteps: keybind.NewBinding(keybind.WithKeys("esc"), keybind.WithHelp("esc", "steps")),
		TransLeave:   keybind.NewBinding(keybind.WithKeys("q"), keybind.WithHelp("q", "home")),
		Scroll:       keybind.NewBinding(keybind.WithKeys("j", "k"), keybind.WithHelp("j/k", "scroll 2")),
		ScrollFast:   keybind.NewBinding(keybind.WithKeys("J", "K"), keybind.WithHelp("J/K", "scroll 10")),
		BlockNav:     keybind.NewBinding(keybind.WithKeys("n", "N"), keybind.WithHelp("n/N", "block")),
		Toggle:       keybind.NewBinding(keybind.WithKeys("enter", " "), keybind.WithHelp("enter", "expand")),
		ExpandAll:    keybind.NewBinding(keybind.WithKeys("o"), keybind.WithHelp("o", "all")),
		GotoTop:      keybind.NewBinding(keybind.WithKeys("g"), keybind.WithHelp("gg", "top")),
		Follow:       keybind.NewBinding(keybind.WithKeys("f", "G"), keybind.WithHelp("f/G", "follow")),
		Search:       keybind.NewBinding(keybind.WithKeys("/"), keybind.WithHelp("/", "search transcript")),
		Filters:      keybind.NewBinding(keybind.WithKeys("F"), keybind.WithHelp("F", "filters")),
		CompactTools: keybind.NewBinding(keybind.WithKeys("c"), keybind.WithHelp("c", "compact tools")),
		ToggleBell:   keybind.NewBinding(keybind.WithKeys("B"), keybind.WithHelp("B", "bell")),
		ClearView:    keybind.NewBinding(keybind.WithKeys("x"), keybind.WithHelp("x", "clear")),
		PageOlder:    keybind.NewBinding(keybind.WithKeys("["), keybind.WithHelp("[", "older")),
		PageNewer:    keybind.NewBinding(keybind.WithKeys("]"), keybind.WithHelp("]", "newer")),

		Submit:       keybind.NewBinding(keybind.WithKeys("enter"), keybind.WithHelp("enter", "submit")),
		Newline:      keybind.NewBinding(keybind.WithKeys("alt+enter", "shift+enter"), keybind.WithHelp("alt+enter", "newline")),
		GateBack:     keybind.NewBinding(keybind.WithKeys("esc"), keybind.WithHelp("esc", "back")),
		GateBlur:     keybind.NewBinding(keybind.WithKeys("esc"), keybind.WithHelp("esc", "blur")),
		GateContext:  keybind.NewBinding(keybind.WithKeys("ctrl+o"), keybind.WithHelp("ctrl+o", "view context")),
		GateEntryNav: keybind.NewBinding(keybind.WithKeys("[", "]"), keybind.WithHelp("[/]", "entries")),
		ReviewOpen:   keybind.NewBinding(keybind.WithKeys("enter"), keybind.WithHelp("enter", "open review")),
		Verdict:      keybind.NewBinding(keybind.WithKeys("1", "2", "3", "4", "5", "6", "7", "8", "9"), keybind.WithHelp("1-9", "decision")),

		RecoverRetry: keybind.NewBinding(keybind.WithKeys("r"), keybind.WithHelp("r", "retry")),
		RecoverGuide: keybind.NewBinding(keybind.WithKeys("g"), keybind.WithHelp("g", "guide+retry")),
		RecoverSkip:  keybind.NewBinding(keybind.WithKeys("s"), keybind.WithHelp("s", "skip")),
		RecoverAbort: keybind.NewBinding(keybind.WithKeys("a"), keybind.WithHelp("a", "abort")),

		IntegrationResolve: keybind.NewBinding(keybind.WithKeys("r"), keybind.WithHelp("r", "finalize")),
		IntegrationAgent:   keybind.NewBinding(keybind.WithKeys("g"), keybind.WithHelp("g", "agent proposal")),

		FinalMergeApprove: keybind.NewBinding(keybind.WithKeys("y"), keybind.WithHelp("y", "merge")),
		FinalMergeDiscard: keybind.NewBinding(keybind.WithKeys("d"), keybind.WithHelp("d", "discard")),
		ResetConfirm:      keybind.NewBinding(keybind.WithKeys("y"), keybind.WithHelp("y", "confirm")),
		ResetCancel:       keybind.NewBinding(keybind.WithKeys("n"), keybind.WithHelp("n", "cancel")),

		StopStep:   keybind.NewBinding(keybind.WithKeys("s"), keybind.WithHelp("s", "stop")),
		ResetStep:  keybind.NewBinding(keybind.WithKeys("r"), keybind.WithHelp("r", "reset")),
		ResumeStep: keybind.NewBinding(keybind.WithKeys("ctrl+r"), keybind.WithHelp("ctrl+r", "resume")),

		ToggleHelp:   keybind.NewBinding(keybind.WithKeys("ctrl+\\"), keybind.WithHelp("ctrl+\\", "help agent")),
		ToggleSimple: keybind.NewBinding(keybind.WithKeys("ctrl+shift+a"), keybind.WithHelp("ctrl+shift+a", "simple/advanced")),

		NotificationDiagnostics: shared.KeyNotificationDiagnostics,

		CopyItem: keybind.NewBinding(keybind.WithKeys("y"), keybind.WithHelp("y", "copy")),
		CopyAll:  keybind.NewBinding(keybind.WithKeys("Y"), keybind.WithHelp("Y", "copy all")),
	}
}

// actions names every binding a Monitor handler matches. Display-only
// bindings are omitted; they mirror a matched binding's keys. The
// notification-diagnostics chord is the global action's value, so it is
// registered once in shared.GlobalActions.
func (k *monitorKeys) actions() []shared.Action {
	const (
		all        = "monitor"
		steps      = "monitor.steps"
		transcript = "monitor.transcript"
		gate       = "monitor.gate"
	)
	a := func(id string, b *keybind.Binding, contexts ...string) shared.Action {
		return shared.Action{ID: "monitor." + id, Contexts: contexts, Binding: b}
	}
	fixed := func(id string, b *keybind.Binding, contexts ...string) shared.Action {
		act := a(id, b, contexts...)
		act.Fixed = true
		return act
	}
	return []shared.Action{
		fixed("focus_next", &k.FocusNext, all),
		fixed("focus_prev", &k.FocusPrev, all),
		fixed("panel_focus", &k.PanelFocus, all),
		a("toggle_help_agent", &k.ToggleHelp, all),
		a("toggle_simple", &k.ToggleSimple, all),
		a("gate_context", &k.GateContext, all),

		a("down", &k.Down, steps),
		a("up", &k.Up, steps),
		a("open_transcript", &k.OpenTranscript, steps),
		a("toggle_tree", &k.ToggleTree, steps),
		a("steps_leave", &k.StepsLeave, steps),
		a("stop_step", &k.StopStep, steps),
		a("reset_step", &k.ResetStep, steps),
		a("resume_step", &k.ResumeStep, steps),
		a("toggle_bell", &k.ToggleBell, steps, transcript),
		a("copy_all", &k.CopyAll, steps, transcript),

		fixed("scroll", &k.Scroll, transcript),
		fixed("scroll_fast", &k.ScrollFast, transcript),
		fixed("block_nav", &k.BlockNav, transcript),
		fixed("goto_top", &k.GotoTop, transcript),
		a("transcript_to_steps", &k.TransToSteps, transcript),
		a("transcript_leave", &k.TransLeave, transcript),
		a("toggle", &k.Toggle, transcript),
		a("expand_all", &k.ExpandAll, transcript),
		a("follow", &k.Follow, transcript),
		a("search", &k.Search, transcript),
		a("filters", &k.Filters, transcript),
		a("compact_tools", &k.CompactTools, transcript),
		a("clear_view", &k.ClearView, transcript),
		a("page_older", &k.PageOlder, transcript),
		a("page_newer", &k.PageNewer, transcript),
		a("copy_item", &k.CopyItem, transcript),

		fixed("gate_submit", &k.Submit, gate+".request", gate+".prompt", gate+".recovery"),
		fixed("gate_blur", &k.GateBlur, gate),
		fixed("gate_entry_nav", &k.GateEntryNav, gate),
		fixed("review_open", &k.ReviewOpen, gate+".review"),
		a("recover_retry", &k.RecoverRetry, gate+".recovery"),
		a("recover_guide", &k.RecoverGuide, gate+".recovery"),
		a("recover_skip", &k.RecoverSkip, gate+".recovery"),
		a("recover_abort", &k.RecoverAbort, gate+".recovery", gate+".integration"),
		a("integration_resolve", &k.IntegrationResolve, gate+".integration"),
		a("integration_agent", &k.IntegrationAgent, gate+".integration"),
		a("final_merge_approve", &k.FinalMergeApprove, gate+".final_merge"),
		a("final_merge_discard", &k.FinalMergeDiscard, gate+".final_merge"),
		a("reset_confirm", &k.ResetConfirm, gate+".reset_confirm"),
		a("reset_cancel", &k.ResetCancel, gate+".reset_confirm"),
	}
}
