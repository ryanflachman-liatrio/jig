package shared

import (
	"image/color"

	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
)

// Styles groups all lipgloss styles for the TUI, organized by UI region.
// Build via DefaultTheme(); the package-level Theme var is the singleton.
type Styles struct {
	Title         lipgloss.Style
	Question      lipgloss.Style
	Error         lipgloss.Style
	Spinner       lipgloss.Style
	Footer        lipgloss.Style
	UserPrompt    lipgloss.Style
	StatusLine    lipgloss.Style
	TurnIndicator lipgloss.Style
	Marker        lipgloss.Style
	Valid         lipgloss.Style
	Running       lipgloss.Style
	Path          lipgloss.Style
	SelectedLine  lipgloss.Style
	SelectedBar   lipgloss.Style // charple "▌" cursor prefix for list rows
	Accent        lipgloss.Style // accent-colored (bok) emphasis

	// Canvas is the full-screen background (Charmtone Pepper), applied to the
	// v2 tea.View so the compositor paints it edge-to-edge. GradFrom/GradTo are
	// the working-indicator / logo gradient endpoints (Charple→Dolly).
	Canvas   color.Color
	GradFrom color.Color
	GradTo   color.Color

	Viewport struct {
		Focused lipgloss.Style
		Blurred lipgloss.Style
	}
	// Help is the modal "?" overlay (see help.go): a bordered box with a title,
	// per-section headers, and a two-column key/description list. Box reuses the
	// focused Charple border so the modal reads as the active surface.
	Help struct {
		Box     lipgloss.Style
		Title   lipgloss.Style
		Section lipgloss.Style
		Key     lipgloss.Style
		Desc    lipgloss.Style
	}
	// Panel is the titled-box primitive (see panel.go). The border styles omit
	// the top edge (the helper hand-composites the titled top line per ADR 0001);
	// Focused/Blurred reuse the same Charple/Iron token pair as Viewport so the
	// focus convention stays consistent app-wide.
	Panel struct {
		FocusedBorder lipgloss.Style
		BlurredBorder lipgloss.Style
		Title         lipgloss.Style
	}
	Textarea struct {
		Base lipgloss.Style
		// Borderless is Base with no border/padding: used when a surrounding panel
		// owns the frame (the paneled chat's Message panel) so the textarea does
		// not draw a second box inside it.
		Borderless    lipgloss.Style
		FocusedBorder color.Color
		BlurredBorder color.Color
	}
	Chat struct {
		Thinking    lipgloss.Style
		ToolCall    lipgloss.Style
		ToolResult  lipgloss.Style
		Hint        lipgloss.Style
		BlockCursor lipgloss.Style

		// Left thick-bar accents ("▌") coloring each chat block by role. Crush's
		// signature block affordance; see withBar in monitor.go.
		BarThinking   lipgloss.Style
		BarToolCall   lipgloss.Style
		BarToolResult lipgloss.Style
		BarError      lipgloss.Style
	}
	// Badge renders a compact status pill: onPrimary text on a solid status
	// background. Only ever wraps plain (unstyled) text so the background stays
	// solid — nested SGR resets would punch holes in it.
	Badge struct {
		Error   lipgloss.Style
		Warning lipgloss.Style
		Info    lipgloss.Style
		Success lipgloss.Style
		Neutral lipgloss.Style
		Accent  lipgloss.Style
	}
	Diff struct {
		Add    lipgloss.Style
		Remove lipgloss.Style
		Hunk   lipgloss.Style
	}
	Step struct {
		ID    lipgloss.Style
		Types map[string]lipgloss.Style
	}

	// Chart styles the read-only detail chart view (see chart/render.go): the
	// node boxes and the three connector classes. Box carries the node border
	// (recolored per step type from Step.Types at render time); Edge is a plain
	// depends_on arrow, Conditional a `when`-guarded arrow, and BackEdge a
	// bounded loop back-edge. Gate/Label style the ⇢ gate glyph and node id.
	Chart struct {
		Box         lipgloss.Style
		Edge        lipgloss.Style
		Conditional lipgloss.Style
		BackEdge    lipgloss.Style
		Gate        lipgloss.Style
		Label       lipgloss.Style
	}

	// Security styles the per-severity rows in the Security pane and its header.
	// Rows are always rendered verbatim (not through glamour) so redacted previews
	// like [aws-key:…MPLE] display literally.
	Security struct {
		Header      lipgloss.Style
		CriticalRow lipgloss.Style
		HighRow     lipgloss.Style
		MediumRow   lipgloss.Style
		LowRow      lipgloss.Style
	}

	// Markdown is the themed glamour config used for every transcript/chat
	// render, replacing glamour's stock "dark" style so headings/code/links
	// carry the Charmtone brand.
	Markdown ansi.StyleConfig
}

// DefaultTheme builds the Charmtone Pantera theme (dark-only). All colors derive
// from the hex tokens in palette.go, mirroring crush's quickStyle → Styles pipeline.
func DefaultTheme() Styles {
	var (
		primary   = lipgloss.Color(hexCharple)
		secondary = lipgloss.Color(hexDolly)
		accent    = lipgloss.Color(hexBok)
		onPrimary = lipgloss.Color(hexButter)

		fgBase  = lipgloss.Color(hexSash)
		fgMuted = lipgloss.Color(hexSquid)
		fgDim   = lipgloss.Color(hexOyster)

		bgLeast = lipgloss.Color(hexBBQ)
		bgLess  = lipgloss.Color(hexChar)

		danger  = lipgloss.Color(hexSriracha)
		success = lipgloss.Color(hexJulep)
		warning = lipgloss.Color(hexMustard)
		info    = lipgloss.Color(hexMalibu)
	)

	var s Styles

	s.Title = lipgloss.NewStyle().Bold(true).Foreground(primary)
	s.Question = lipgloss.NewStyle().Foreground(fgMuted)
	s.Error = lipgloss.NewStyle().Bold(true).Foreground(danger)
	s.Spinner = lipgloss.NewStyle().Foreground(primary)
	s.Footer = lipgloss.NewStyle().Foreground(fgDim)
	s.UserPrompt = lipgloss.NewStyle().Bold(true).Foreground(secondary)
	s.StatusLine = lipgloss.NewStyle().Foreground(fgDim).Italic(true)
	s.TurnIndicator = lipgloss.NewStyle().Foreground(fgDim).Italic(true)
	s.Marker = lipgloss.NewStyle().Foreground(fgMuted)
	s.Valid = lipgloss.NewStyle().Bold(true).Foreground(success)
	s.Running = lipgloss.NewStyle().Foreground(secondary)
	s.Path = lipgloss.NewStyle().Foreground(fgDim).Italic(true)
	s.SelectedLine = lipgloss.NewStyle().Bold(true).Foreground(fgBase)
	s.SelectedBar = lipgloss.NewStyle().Foreground(primary).Bold(true)
	s.Accent = lipgloss.NewStyle().Foreground(accent)

	s.Help.Box = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(primary).
		Padding(0, 2)
	s.Help.Title = lipgloss.NewStyle().Bold(true).Foreground(primary)
	s.Help.Section = lipgloss.NewStyle().Bold(true).Foreground(fgBase)
	s.Help.Key = lipgloss.NewStyle().Foreground(secondary)
	s.Help.Desc = lipgloss.NewStyle().Foreground(fgMuted)

	s.Viewport.Focused = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(primary).
		Padding(0, 1)
	s.Viewport.Blurred = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(hexIron)).
		Padding(0, 1)

	// Panel borders omit the top edge (BorderTop(false)); panel() hand-builds the
	// titled top line. Same primary/Iron pair as Viewport.Focused/.Blurred.
	panelBorder := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderTop(false).
		Padding(0, 1)
	s.Panel.FocusedBorder = panelBorder.BorderForeground(primary)
	s.Panel.BlurredBorder = panelBorder.BorderForeground(lipgloss.Color(hexIron))
	s.Panel.Title = lipgloss.NewStyle().Bold(true).Foreground(fgBase)

	s.Textarea.Base = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	s.Textarea.Borderless = lipgloss.NewStyle()
	s.Textarea.FocusedBorder = primary
	s.Textarea.BlurredBorder = lipgloss.Color(hexIron)

	s.Chat.Thinking = lipgloss.NewStyle().Italic(true).Foreground(fgDim)
	s.Chat.ToolCall = lipgloss.NewStyle().Foreground(primary)
	s.Chat.ToolResult = lipgloss.NewStyle().Foreground(accent)
	s.Chat.Hint = lipgloss.NewStyle().Foreground(fgDim)
	s.Chat.BlockCursor = lipgloss.NewStyle().Bold(true).Foreground(onPrimary).Background(primary)

	s.Chat.BarThinking = lipgloss.NewStyle().Foreground(fgDim)
	s.Chat.BarToolCall = lipgloss.NewStyle().Foreground(primary)
	s.Chat.BarToolResult = lipgloss.NewStyle().Foreground(accent)
	s.Chat.BarError = lipgloss.NewStyle().Foreground(danger)

	badge := lipgloss.NewStyle().Bold(true).Foreground(onPrimary).Padding(0, 1)
	s.Badge.Error = badge.Background(danger)
	s.Badge.Warning = badge.Background(warning).Foreground(lipgloss.Color(hexPepper))
	s.Badge.Info = badge.Background(info)
	s.Badge.Success = badge.Background(success).Foreground(lipgloss.Color(hexPepper))
	s.Badge.Neutral = badge.Background(bgLess).Foreground(fgBase)
	s.Badge.Accent = badge.Background(accent).Foreground(lipgloss.Color(hexPepper))

	s.Diff.Add = lipgloss.NewStyle().Foreground(success)
	s.Diff.Remove = lipgloss.NewStyle().Foreground(danger)
	s.Diff.Hunk = lipgloss.NewStyle().Foreground(info)

	s.Step.ID = lipgloss.NewStyle().Foreground(fgBase)
	s.Step.Types = map[string]lipgloss.Style{
		"agent":   lipgloss.NewStyle().Foreground(primary),
		"command": lipgloss.NewStyle().Foreground(info),
		"review":  lipgloss.NewStyle().Foreground(warning),
	}
	_ = bgLeast

	// Chart: node box reuses the rounded border on a muted default color (the
	// renderer recolors it per step type); the three edge classes reuse the
	// existing muted/info/warning tokens so the chart reads in the same palette
	// as the step list's type badges.
	s.Chart.Box = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(fgMuted).
		Padding(0, 1)
	s.Chart.Edge = lipgloss.NewStyle().Foreground(fgMuted)
	s.Chart.Conditional = lipgloss.NewStyle().Foreground(info)
	s.Chart.BackEdge = lipgloss.NewStyle().Foreground(warning)
	s.Chart.Gate = lipgloss.NewStyle().Foreground(fgDim)
	s.Chart.Label = lipgloss.NewStyle().Foreground(fgBase)

	s.Canvas = lipgloss.Color(hexPepper)
	s.GradFrom = primary
	s.GradTo = secondary

	s.Markdown = charmtoneMarkdown()

	s.Security.Header = lipgloss.NewStyle().Bold(true).Foreground(fgMuted)
	s.Security.CriticalRow = lipgloss.NewStyle().Bold(true).Foreground(danger)
	s.Security.HighRow = lipgloss.NewStyle().Foreground(danger)
	s.Security.MediumRow = lipgloss.NewStyle().Foreground(warning)
	s.Security.LowRow = lipgloss.NewStyle().Foreground(fgMuted)

	return s
}

// charmtoneMarkdown clones glamour's dark style and repaints the headings, code,
// links, and emphasis in Charmtone so rendered transcript prose matches the rest
// of the TUI. Everything not overridden inherits glamour's sensible dark defaults
// (chroma code highlighting, list indentation, spacing).
func charmtoneMarkdown() ansi.StyleConfig {
	sp := func(s string) *string { return &s }
	bp := func(b bool) *bool { return &b }

	c := styles.DarkStyleConfig

	c.Text.Color = sp(hexSash)

	c.Heading.Color = sp(hexCharple)
	c.Heading.Bold = bp(true)
	c.H1.Color = sp(hexButter)
	c.H1.BackgroundColor = sp(hexCharple)
	c.H1.Bold = bp(true)
	c.H2.Color = sp(hexCharple)
	c.H3.Color = sp(hexDolly)
	c.H4.Color = sp(hexDolly)
	c.H5.Color = sp(hexBlush)
	c.H6.Color = sp(hexBlush)

	c.Emph.Color = sp(hexDolly)
	c.Emph.Italic = bp(true)
	c.Strong.Color = sp(hexBlush)
	c.Strong.Bold = bp(true)

	c.Link.Color = sp(hexMalibu)
	c.Link.Underline = bp(true)
	c.LinkText.Color = sp(hexBok)

	c.Code.Color = sp(hexBok)
	c.Code.BackgroundColor = sp(hexBBQ)

	c.Item.Color = sp(hexSash)
	c.Enumeration.Color = sp(hexCharple)

	c.BlockQuote.Color = sp(hexSquid)
	c.BlockQuote.Italic = bp(true)
	c.HorizontalRule.Color = sp(hexChar)

	return c
}

// Theme is the package-level singleton; swap it out to change the active theme.
var Theme = DefaultTheme()
