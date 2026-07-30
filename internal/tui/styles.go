package tui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#fafafa"})

	questionStyle = lipgloss.NewStyle().
			Italic(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#4b4b4b", Dark: "#a0a0a0"})

	viewportFocusedStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(textareaFocusedBorder).
				Padding(0, 1)

	viewportBlurredStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(textareaBlurredBorder).
				Padding(0, 1)

	errorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#D7263D", Dark: "#FF5D5D"})

	spinnerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"})

	footerStyle = lipgloss.NewStyle().Faint(true)

	userPromptStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#0F5FBF", Dark: "#7AA2F7"})

	textareaStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1)

	textareaFocusedBorder = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	textareaBlurredBorder = lipgloss.AdaptiveColor{Light: "#BDBDBD", Dark: "#4B4B4B"}

	statusLineStyle = lipgloss.NewStyle().Faint(true).Italic(true)

	turnIndicatorStyle = lipgloss.NewStyle().Faint(true).Italic(true)

	// Workflow selector / detail screens.

	// stepTypeStyles colors a step's type badge by kind so the graph reads at a
	// glance: agents (the non-deterministic part) stand out from deterministic
	// commands and human review gates.
	stepTypeStyles = map[string]lipgloss.Style{
		"agent":   lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}),
		"command": lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#0F5FBF", Dark: "#7AA2F7"}),
		"review":  lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#B8860B", Dark: "#E0AF68"}),
	}

	// markerStyle tints the loop / gate annotations next to a step.
	markerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#4b4b4b", Dark: "#a0a0a0"})

	validStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#5FD75F"})

	stepIDStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#fafafa"})

	pathStyle = lipgloss.NewStyle().Faint(true).Italic(true)

	runningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#0F5FBF", Dark: "#7AA2F7"})
)
