package monitor

import (
	"strings"

	"jig/internal/tui/shared"
)

// writeUserGuidance keeps role-user text separate from role-user tool results:
// only the former is prose written by the workflow operator.
func (m Model) writeUserGuidance(b *strings.Builder, key blockKey, text string) {
	b.WriteString("  " + shared.Theme.Chat.UserGuidance.Render("User") + "\n")
	b.WriteString(m.renderMarkdown(key, text))
}
