package shared

import "strings"

// EmptyState is the shared empty/waiting body used across Home, Runs, and
// Monitor panes. Exactly one CTA line is emphasized; body and secondary stay
// dim so the operator learns the next key without sticker clutter.
type EmptyState struct {
	Title     string // what this pane is / why it is empty
	Body      string // optional secondary explanation (dim)
	CTA       string // one primary action, e.g. "r  start a run"
	Secondary string // optional extra dim hint (max one)
}

// RenderEmptyState builds a left-padded empty body. Empty fields are omitted.
func RenderEmptyState(s EmptyState) string {
	var b strings.Builder
	b.WriteString("\n")
	if s.Title != "" {
		b.WriteString("  " + Theme.Title.Render(s.Title) + "\n")
	}
	if s.Body != "" {
		b.WriteString("\n  " + Theme.Question.Render(s.Body) + "\n")
	}
	if s.CTA != "" {
		b.WriteString("\n  " + Theme.Accent.Render(s.CTA) + "\n")
	}
	if s.Secondary != "" {
		b.WriteString("\n  " + Theme.Chat.Hint.Render(s.Secondary) + "\n")
	}
	return b.String()
}
