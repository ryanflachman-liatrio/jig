package shared

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// StatusLine is the four-slot grammar for tool-exchange headers used by the
// Monitor transcript (epic slice 02). Each slot is rendered independently and
// composed with fixed separators, so slice 05 (tool detail sections), slice 07
// (diff badge), slice 08 (grouped reads), slice 13 (spinner ticker in Icon),
// and slice 15 (inline argument previews in Description) can write into named
// fields rather than manipulating a fused string.
//
// The shape mirrors omp's `renderStatusLine`
// (packages/coding-agent/src/tui/status-line.ts:32-54) intentionally:
//
//	<Icon> <Title>: <Description> <Badge> <Meta · Meta · Meta>
//
// Separators are asymmetric on purpose. ": " binds the description to the
// title; a bare " " detaches the badge and the meta list. Meta entries join
// with " · " (a single space each side), and empty or whitespace-only entries
// are dropped so a row never ends in a dangling separator.
//
// Each *Style field is optional. When the caller supplies the zero-value
// lipgloss.Style{}, RenderStatusLine falls back to the theme's per-slot style
// (Theme.Chat.ToolTitle / .ToolDescription / .ToolMeta / .ToolBadge for the
// text slots; Icon and Badge have no theme default when the caller supplies no
// style). When the caller supplies any style, unset fields inherit the theme
// default via lipgloss.Style.Inherit, so a caller can override a single
// attribute (e.g. TranscriptSelected's bold + primary) without redeclaring
// every other property.
//
// Values in every slot are flattened to a single row before styling: every CR
// and LF is replaced with a space so a caller cannot smuggle a second row into
// a status line that lives inside a card frame. Tabs remain the caller's
// responsibility; sanitization of untrusted tool arguments belongs upstream
// (Monitor's `sanitizeToolSummary`).
//
// RenderStatusLine never truncates. Clipping to a specific visible-cell width
// belongs to the enclosing card frame (slice 01's `TruncateTitle` inside
// composeBorderBar), which lets one primitive stay pure and the other own
// geometry.
type StatusLine struct {
	Icon             string
	IconStyle        lipgloss.Style
	Title            string
	TitleStyle       lipgloss.Style
	Description      string
	DescriptionStyle lipgloss.Style
	Badge            string
	BadgeStyle       lipgloss.Style
	Meta             []string
	MetaStyle        lipgloss.Style
}

// flattenStatusSlot replaces every CR/LF in a slot's raw text with a space so
// the composed status line is always exactly one row tall. Callers may still
// embed valid ANSI escape sequences (styled tool titles, for example); those
// are single-line by construction.
func flattenStatusSlot(s string) string {
	if s == "" {
		return ""
	}
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
}

// resolveSlotStyle merges the caller-supplied style with the theme default.
// When the caller passes the zero-value lipgloss.Style{}, the default wins
// entirely; when the caller supplies any settings, the default fills in only
// the attributes the caller has not set (via lipgloss.Style.Inherit).
func resolveSlotStyle(caller, fallback lipgloss.Style) lipgloss.Style {
	if _, unset := caller.GetForeground().(lipgloss.NoColor); unset && !hasStyleSettings(caller) {
		return fallback
	}
	return caller.Inherit(fallback)
}

// hasStyleSettings reports whether a lipgloss.Style carries any attribute that
// would survive Inherit. The zero-value Style{} has no settings; we treat it
// as "caller did not supply a style" so the fallback wins outright. A style
// with only Bold(true) but no foreground is still meaningful, so we check the
// most commonly used discriminators without cloning the whole Style tree.
func hasStyleSettings(s lipgloss.Style) bool {
	if s.GetBold() || s.GetItalic() || s.GetUnderline() {
		return true
	}
	if _, unset := s.GetForeground().(lipgloss.NoColor); !unset {
		return true
	}
	if _, unset := s.GetBackground().(lipgloss.NoColor); !unset {
		return true
	}
	return false
}

// RenderStatusLine composes a single row from the caller-supplied slots. The
// returned string is not truncated; callers that need to fit a width (the
// card frame from slice 01) clip afterward with an ANSI-aware helper such as
// TruncateTitle.
func RenderStatusLine(s StatusLine) string {
	iconText := flattenStatusSlot(s.Icon)
	titleText := flattenStatusSlot(s.Title)
	descText := flattenStatusSlot(s.Description)
	badgeText := flattenStatusSlot(s.Badge)

	metaEntries := make([]string, 0, len(s.Meta))
	for _, entry := range s.Meta {
		entry = flattenStatusSlot(entry)
		if strings.TrimSpace(entry) == "" {
			continue
		}
		metaEntries = append(metaEntries, entry)
	}

	titleStyle := resolveSlotStyle(s.TitleStyle, Theme.Chat.ToolTitle)
	descStyle := resolveSlotStyle(s.DescriptionStyle, Theme.Chat.ToolDescription)
	metaStyle := resolveSlotStyle(s.MetaStyle, Theme.Chat.ToolMeta)
	badgeStyle := resolveSlotStyle(s.BadgeStyle, Theme.Chat.ToolBadge)

	var b strings.Builder
	if iconText != "" {
		if _, unset := s.IconStyle.GetForeground().(lipgloss.NoColor); unset && !hasStyleSettings(s.IconStyle) {
			b.WriteString(iconText)
		} else {
			b.WriteString(s.IconStyle.Render(iconText))
		}
	}

	if titleText != "" {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(titleStyle.Render(titleText))
	}

	if descText != "" {
		if titleText != "" {
			b.WriteString(": ")
		} else if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(descStyle.Render(descText))
	}

	if badgeText != "" {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(badgeStyle.Render(badgeText))
	}

	if len(metaEntries) > 0 {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(metaStyle.Render(strings.Join(metaEntries, " · ")))
	}

	return b.String()
}
