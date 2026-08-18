package detail

import (
	"fmt"
	"strings"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"

	"jig/internal/tui/chart"
	"jig/internal/tui/shared"
	"jig/internal/workflow"
)

func (m Model) View() string {
	if !m.Ready {
		return "\n  Loading…\n"
	}
	footer := m.footerView()
	body := shared.Panel(m.titleText(), m.vp.View(), m.width, m.height-lipgloss.Height(footer), true)
	return lipgloss.JoinVertical(lipgloss.Left, body, footer)
}

// HelpSections satisfies the root's helpProvider bridge: the workflow detail
// actions plus the global chord. Run drops out when disabled, mirroring the footer.
func (m Model) HelpSections() []shared.HelpSection {
	return []shared.HelpSection{
		{Title: "Workflow", Bindings: []keybind.Binding{m.keys.Run, m.keys.Toggle, m.keys.Runs, m.keys.Back}},
		{Title: "Global", Bindings: []keybind.Binding{shared.KeyHelp, shared.KeyQuit}},
	}
}

func (m Model) CapturesText() bool { return false }

func (m Model) footerView() string {
	// Run and Toggle drop out automatically when disabled (no workflow loaded).
	return shared.Theme.Footer.Render("  " + shared.HintString(m.keys.Run, m.keys.Toggle, m.keys.Runs, m.keys.Back, shared.KeyHelp, shared.KeyQuit))
}

// titleText is the detail panel's title: the workflow name, falling back to the
// file path when the workflow could not be named (unparseable or unnamed).
func (m Model) titleText() string {
	if m.meta.Name != "" {
		return m.meta.Name
	}
	return m.path
}

// body renders the header and step list into the viewport's content.
func (m Model) body() string {
	if !m.Loaded {
		return "\n  Loading…\n"
	}

	// The panel title carries the workflow name, so the body opens with the
	// version/description/path metadata rather than repeating the name.
	var b strings.Builder
	b.WriteString("\n")
	if m.meta.Version != "" {
		b.WriteString("  " + shared.Theme.Question.Render("v"+m.meta.Version) + "\n")
	}
	if m.meta.Description != "" {
		b.WriteString("  " + shared.Theme.Question.Render(m.meta.Description) + "\n")
	}
	b.WriteString("  " + shared.Theme.Path.Render(m.path) + "\n\n")

	if m.loadErr != nil {
		b.WriteString("  " + shared.Theme.Error.Render(shared.IconError+" invalid workflow") + "\n\n")
		// The loader aggregates every problem; indent the multi-line message.
		for _, line := range strings.Split(strings.TrimRight(m.loadErr.Error(), "\n"), "\n") {
			b.WriteString("  " + line + "\n")
		}
		return b.String()
	}

	b.WriteString("  " + shared.Theme.Valid.Render(shared.IconSuccess+" valid") +
		shared.Theme.Question.Render(fmt.Sprintf("  ·  %d step(s)", len(m.wf.Steps))) + "\n\n")
	if m.viewMode {
		b.WriteString(m.chartView())
	} else {
		b.WriteString(m.stepsView())
	}
	return b.String()
}

// chartView renders the depends_on DAG as a top-down flowchart laid out to fit
// the viewport's inner width. It falls back to the flat list if no valid
// workflow is loaded (the toggle is disabled in that case, so this is defensive).
func (m Model) chartView() string {
	if m.wf == nil {
		return m.stepsView()
	}
	return chart.RenderChart(m.wf, m.vpWidth)
}

const stepTypeBadgeWidth = 8

// stepsView renders one line per step: an index, the step id, a type badge, and
// any loop/gate/guard annotations.
func (m Model) stepsView() string {
	steps := m.wf.Steps
	idWidth := len("id")
	for _, s := range steps {
		if len(s.ID) > idWidth {
			idWidth = len(s.ID)
		}
	}

	var b strings.Builder
	for i, s := range steps {
		typ := string(s.Type)
		badge := typ
		if style, ok := shared.Theme.Step.Types[typ]; ok {
			badge = style.Render(typ)
		}
		fmt.Fprintf(&b, "  %2d  %s  %s",
			i+1,
			shared.Theme.Step.ID.Render(shared.PadRight(s.ID, idWidth)),
			shared.PadRight(badge, len(typ), stepTypeBadgeWidth),
		)
		for _, mk := range stepMarkers(s) {
			b.WriteString("  " + shared.Theme.Marker.Render(mk))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// stepMarkers describes the extra structure hanging off a step: its bounded
// loop back-edge, its deterministic validation gate, and any run guard.
func stepMarkers(s workflow.Step) []string {
	var out []string
	if s.Type == workflow.StepAgent {
		// Resolved backend/transport (applyDefaults fills these before Load
		// returns). Show when non-default so mixed-transport workflows are
		// visible before Run.
		if s.Transport != "" && s.Transport != workflow.TransportSDK {
			out = append(out, fmt.Sprintf("%s/%s", s.Backend, s.Transport))
		} else if s.Backend != "" && s.Backend != workflow.BackendClaude {
			out = append(out, s.Backend)
		}
	}
	if s.Loop != nil {
		m := fmt.Sprintf("↺ loop→%s", s.Loop.Goto)
		if s.Loop.MaxIterations > 0 {
			m += fmt.Sprintf(" (max %d)", s.Loop.MaxIterations)
		}
		out = append(out, m)
	}
	if s.Validate != nil {
		out = append(out, "⇢ gate")
	}
	if s.When != "" {
		out = append(out, "when "+s.When)
	}
	return out
}
