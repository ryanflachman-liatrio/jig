package tui

import (
	"image/color"

	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
)

// Charmtone palette (crush's default "Pantera" theme), dark-only. Names mirror
// github.com/charmbracelet/x/exp/charmtone so the mapping back to upstream stays
// greppable. We keep hex strings (for glamour's ansi.StyleConfig, which wants
// *string) and derive lipgloss.Color from them for lipgloss styles.
const (
	// Brand.
	hexCharple = "#6B50FF" // primary   (violet)
	hexDolly   = "#FF60FF" // secondary (magenta)
	hexBok     = "#68FFD6" // accent
	hexBlush   = "#FF84FF" // keyword
	hexButter  = "#FFFAF1" // onPrimary (fg atop colored backgrounds)

	// Foreground ladder, brightest → dimmest.
	hexSash   = "#ECEBF0" // fgBase
	hexSmoke  = "#BFBCC8" // fgSubtle
	hexSquid  = "#858392" // fgMuted
	hexOyster = "#605F6B" // fgDim

	// Background ladder, least → most visible atop the base.
	hexPepper = "#201F26" // bgBase
	hexBBQ    = "#2D2C36" // bgLeastVisible
	hexChar   = "#3A3943" // bgLessVisible / separator
	hexIron   = "#4D4C57" // bgMostVisible

	// Status.
	hexSriracha = "#EB4268" // error
	hexCoral    = "#FF577D" // destructive
	hexMustard  = "#F5EF34" // warning
	hexTang     = "#FF985A" // attention
	hexJulep    = "#00FFB2" // success
	hexGuac     = "#12C78F" // success (subtle)
	hexMalibu   = "#00A4FF" // info
	hexCitron   = "#E8FF27" // busy
)

// Icon vocabulary. Centralized (crush-style) so glyphs stay consistent and a
// single edit re-skins every call site.
const (
	IconSuccess  = "✓"
	IconError    = "✗"
	IconPending  = "○"
	IconRunning  = "●"
	IconSkipped  = "—"
	IconReview   = "?"
	IconInput    = "⊙"
	IconValidate = "⇢"

	IconThinking   = "◇"
	IconToolCall   = "▸"
	IconToolResult = "↳"

	CollapsedMarker = "▸"
	ExpandedMarker  = "▾"

	BarThick  = "▌" // left accent bar on chat blocks
	CursorBar = "▌" // selected-row marker
	RuleGlyph = "─"
	LoopGlyph = "↺"
	GateGlyph = "⇢"
)

// Styles groups all lipgloss styles for the TUI, organized by UI region.
// Build via DefaultTheme(); the package-level theme var is the singleton.
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

	// Markdown is the themed glamour config used for every transcript/chat
	// render, replacing glamour's stock "dark" style so headings/code/links
	// carry the Charmtone brand.
	Markdown ansi.StyleConfig
}

// DefaultTheme builds the Charmtone Pantera theme (dark-only). All colors derive
// from the hex tokens above, mirroring crush's quickStyle → Styles pipeline.
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

	s.Canvas = lipgloss.Color(hexPepper)
	s.GradFrom = primary
	s.GradTo = secondary

	s.Markdown = charmtoneMarkdown()

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

// theme is the package-level singleton; swap it out to change the active theme.
var theme = DefaultTheme()
