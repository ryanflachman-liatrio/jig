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
)
