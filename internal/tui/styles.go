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

	// Chat chain rendering (Phase 5): one style per block kind so a step's
	// transcript reads at a glance.

	// thinkingStyle dims reasoning blocks so they sit behind the model's actual
	// answer rather than competing with it.
	thinkingStyle = lipgloss.NewStyle().
			Faint(true).
			Italic(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#6b6b6b", Dark: "#8a8a8a"})

	// toolCallStyle tints a ⚙ tool_use line; toolResultStyle its ↳ result.
	toolCallStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"})

	toolResultStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#0F5FBF", Dark: "#7AA2F7"})

	// chatHintStyle renders the collapse affordance ("[N chars]", "… elided").
	chatHintStyle = lipgloss.NewStyle().Faint(true)

	// blockCursorStyle highlights the collapsible block under the chat cursor so
	// the expand target (tab to move, enter to toggle) is unambiguous.
	blockCursorStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#fafafa"})

	// Diff rendering (Phase 5 review = "diff").
	diffAddStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#5FD75F"})

	diffRemoveStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#D7263D", Dark: "#FF5D5D"})

	diffHunkStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#0F5FBF", Dark: "#7AA2F7"})
)
