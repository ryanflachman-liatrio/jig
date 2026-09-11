package shared

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// ToolStatusIcon maps a tool exchange's display state and (optional) canonical
// tool kind to the glyph and style rendered into StatusLine.Icon.
//
// Two invariants drive its shape:
//
//   - Anti-jitter (epic CC-4). Running and pending resolve to the same glyph
//     so a settling row does not swap between spinners as one call completes.
//     The one glyph transition permitted is running/pending → success on a
//     settled exchange with a known kind, which swaps in that kind's
//     signature mark (IconTool*). Errors flip both glyph and color, but only
//     once, at settling.
//
//   - Border parity. The returned style shares its foreground with the
//     corresponding Card.Border* style (slice 01) so the header icon and the
//     card frame read as one indicator of state.
//
// Unknown kinds fall back to the generic pending glyph on success (rather
// than upgrading to a signature glyph the caller cannot name); unknown states
// fall back to pending presentation and never silently acquire success.
func ToolStatusIcon(state ToolDisplayState, kind string) (glyph string, style lipgloss.Style) {
	switch state {
	case ToolDisplaySuccess:
		return toolSignatureGlyph(kind), Theme.Card.BorderSuccess
	case ToolDisplayError:
		return IconStatusError, Theme.Card.BorderError
	case ToolDisplayRunning:
		return IconStatusRunning, Theme.Card.BorderRunning
	case ToolDisplayUnknownUse, ToolDisplayUnknownResult:
		return IconStatusWarning, Theme.Card.BorderWarning
	default:
		return IconStatusPending, Theme.Card.BorderPending
	}
}

// toolSignatureGlyph is the settled-success mark for a canonical tool kind.
// Unknown kinds fall back to the generic success glyph (a bullet), which
// keeps the CC-4 rule intact: the caller either has a name for the tool and
// gets its signature mark, or does not and gets a neutral completion mark.
func toolSignatureGlyph(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "read":
		return IconToolRead
	case "edit":
		return IconToolEdit
	case "write":
		return IconToolWrite
	case "notebookedit":
		return IconToolEdit
	case "glob", "grep":
		return IconToolSearch
	case "bash":
		return IconToolShell
	case "websearch", "webfetch":
		return IconToolWeb
	case "task":
		return IconToolAgent
	case "todowrite", "todoread":
		return IconToolTodo
	case "askuserquestion":
		return IconToolAsk
	case "skill":
		return IconToolAgent
	default:
		return IconStatusSuccess
	}
}
