package tui

import (
	"strings"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"

	"jig/internal/step"
)

// help.go renders the "?" modal overlay for the run monitor. Like the footer
// (see hintString), it renders straight from the same keybind.Binding structs
// the handlers match on, skipping disabled bindings — so the overlay can never
// advertise a key the monitor does not accept, nor omit one it does. The overlay
// is focus-aware: it shows the section for the region that currently holds focus
// plus the shared Focus and Global sections, mirroring how footerView already
// tailors its hint line to the active region.

// helpSection is one titled group of bindings in the overlay.
type helpSection struct {
	title    string
	bindings []keybind.Binding
}

// helpProvider is implemented by every screen model that contributes a help
// overlay. capturesText reports whether the screen is currently capturing free
// text (a list filter, a gate textarea), in which case "?" is a literal
// character and must not open the overlay.
type helpProvider interface {
	helpSections() []helpSection
	capturesText() bool
}

// renderHelpOverlay composites the modal box centered over base — the live
// screen — using a lipgloss v2 Canvas so the underlying screen shows through
// around the box (the box layer draws on top; cells it does not cover keep the
// base). It renders straight from keybind.Binding structs and skips disabled
// ones, so the overlay can never advertise a key the screen does not accept.
func renderHelpOverlay(base string, width, height int, sections []helpSection) string {
	// Compute the key-column width across all enabled bindings so the two columns
	// align regardless of which section a row belongs to.
	keyW := 0
	for _, sec := range sections {
		for _, b := range sec.bindings {
			if !b.Enabled() {
				continue
			}
			if k := lipgloss.Width(b.Help().Key); k > keyW {
				keyW = k
			}
		}
	}

	var body strings.Builder
	body.WriteString(theme.Help.Title.Render("jig · help"))
	body.WriteString("\n")
	for _, sec := range sections {
		rows := make([]string, 0, len(sec.bindings))
		for _, b := range sec.bindings {
			if !b.Enabled() {
				continue
			}
			h := b.Help()
			key := theme.Help.Key.Render(padRight(h.Key, keyW))
			rows = append(rows, "  "+key+"  "+theme.Help.Desc.Render(h.Desc))
		}
		if len(rows) == 0 {
			continue
		}
		body.WriteString("\n")
		body.WriteString(theme.Help.Section.Render(sec.title))
		body.WriteString("\n")
		body.WriteString(strings.Join(rows, "\n"))
		body.WriteString("\n")
	}
	body.WriteString("\n")
	body.WriteString(theme.Help.Desc.Render("? or esc to close"))

	box := theme.Help.Box.Render(body.String())

	// Center the box over the base screen. The base is layer z=0 (drawn first);
	// the box is placed on top at the centered offset, so everything the box does
	// not cover keeps showing the live screen beneath it.
	x := (width - lipgloss.Width(box)) / 2
	y := (height - lipgloss.Height(box)) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	// A Compositor (not Canvas.Compose directly) is what honors per-layer x/y/z:
	// the base sits at the origin as z=0, the box is offset and drawn on top as
	// z=1. Draw onto a fixed width×height canvas so the result matches the screen.
	comp := lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	)
	return lipgloss.NewCanvas(width, height).Compose(comp).Render()
}

// helpSections returns the sections to show for the monitor's current focus and
// gate state. It reads the same bindings and eligibility as footerView so the two
// stay in lockstep.
func (m monitorModel) helpSections() []helpSection {
	var sections []helpSection

	switch {
	case m.focus == focusGate && m.hasGate():
		sections = append(sections, m.gateHelpSection())
	case m.focus == focusTranscript:
		sections = append(sections, helpSection{
			title: "Transcript",
			bindings: []keybind.Binding{
				m.keys.Scroll, m.keys.GotoTop, m.keys.GotoBottom,
				m.keys.BlockNav, m.keys.Toggle, m.keys.ExpandAll,
				m.keys.TransToSteps, m.keys.TransLeave,
			},
		})
	default: // focusSteps
		// Mirror footerView's eligibility gating so disabled lifecycle actions
		// drop out of the overlay exactly as they drop out of the footer.
		stopKey := m.keys.StopStep
		resetKey := m.keys.ResetStep
		resumeKey := m.keys.ResumeStep
		if m.done || m.cursor >= len(m.steps) {
			stopKey.SetEnabled(false)
			resetKey.SetEnabled(false)
			resumeKey.SetEnabled(false)
		} else {
			st := m.steps[m.cursor]
			stopKey.SetEnabled(st.status == step.StatusRunning)
			resumeKey.SetEnabled(st.status == step.StatusStopped)
			switch st.status {
			case step.StatusSucceeded, step.StatusFailed, step.StatusSkipped,
				step.StatusStopped, step.StatusAwaitingReview:
				resetKey.SetEnabled(true)
			default:
				resetKey.SetEnabled(false)
			}
		}
		sections = append(sections, helpSection{
			title: "Steps",
			bindings: []keybind.Binding{
				m.keys.StepsNav, m.keys.OpenTranscript,
				stopKey, resetKey, resumeKey, m.keys.StepsLeave,
			},
		})
	}

	// Focus + Global sections are shown on every screen.
	sections = append(sections, helpSection{
		title:    "Focus",
		bindings: []keybind.Binding{m.keys.FocusNext, m.keys.FocusPrev, m.keys.PanelFocus},
	})
	sections = append(sections, helpSection{
		title:    "Global",
		bindings: []keybind.Binding{keyHelp, keyQuit},
	})
	return sections
}

// gateHelpSection builds the section for the currently-active gate entry, keyed
// off the same kind/flags footerView branches on.
func (m monitorModel) gateHelpSection() helpSection {
	entry, ok := m.activeEntry()
	if !ok {
		return helpSection{title: "Gate", bindings: []keybind.Binding{m.keys.GateBlur}}
	}
	entryNav := m.keys.GateEntryNav
	entryNav.SetEnabled(len(m.inputQueue) > 1)

	sec := helpSection{title: "Gate"}
	switch entry.kind {
	case inputKindRequest:
		sec.bindings = []keybind.Binding{m.keys.Submit, m.keys.Newline, entryNav, m.keys.GateBlur}
	case inputKindQuestion:
		multi := entry.question != nil &&
			entry.questionIdx < len(entry.question.Questions) &&
			entry.question.Questions[entry.questionIdx].MultiSelect
		if multi {
			sec.bindings = []keybind.Binding{m.keys.ToggleOpt, m.keys.QuestionScroll, m.keys.QConfirm, entryNav, m.keys.GateBlur}
		} else {
			sec.bindings = []keybind.Binding{m.keys.Answer, m.keys.QuestionScroll, entryNav, m.keys.GateBlur}
		}
	case inputKindReview:
		switch {
		case entry.composing:
			sec.bindings = []keybind.Binding{m.keys.Submit, m.keys.Newline, m.keys.GateBlur}
		case entry.review != nil && entry.review.AllowMessage:
			sec.bindings = []keybind.Binding{m.keys.Verdict, m.keys.Message, entryNav, m.keys.GateBlur}
		default:
			sec.bindings = []keybind.Binding{m.keys.Verdict, entryNav, m.keys.GateBlur}
		}
	case inputKindPrompt:
		sec.bindings = []keybind.Binding{m.keys.Submit, m.keys.Newline, entryNav, m.keys.GateBlur}
	case inputKindRecovery:
		switch {
		case entry.composing:
			sec.bindings = []keybind.Binding{m.keys.Submit, m.keys.Newline, m.keys.GateBlur}
		case entry.recovery != nil && entry.recovery.CanResume:
			sec.bindings = []keybind.Binding{m.keys.RecoverRetry, m.keys.RecoverGuide, m.keys.RecoverAbort, entryNav, m.keys.GateBlur}
		default:
			sec.bindings = []keybind.Binding{m.keys.RecoverRetry, m.keys.RecoverAbort, entryNav, m.keys.GateBlur}
		}
	case inputKindIntegrationConflict:
		sec.bindings = []keybind.Binding{m.keys.IntegrationResolve, m.keys.RecoverAbort, entryNav, m.keys.GateBlur}
	case inputKindFinalMerge:
		sec.bindings = []keybind.Binding{m.keys.FinalMergeApprove, m.keys.FinalMergeDiscard, entryNav, m.keys.GateBlur}
	case inputKindResetConfirm:
		sec.bindings = []keybind.Binding{m.keys.GateBlur}
	}
	return sec
}

// capturesText reports whether a gate textarea is capturing free text; delegates
// to textareaActive. Satisfies helpProvider.
func (m monitorModel) capturesText() bool { return m.textareaActive() }
