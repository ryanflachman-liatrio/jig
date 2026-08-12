package tui

import "jig/internal/tui/shared"

// Re-export the shared constants under the same unexported names so existing
// tui files need no changes during migration.
const (
	IconSuccess  = shared.IconSuccess
	IconError    = shared.IconError
	IconPending  = shared.IconPending
	IconRunning  = shared.IconRunning
	IconSkipped  = shared.IconSkipped
	IconReview   = shared.IconReview
	IconInput    = shared.IconInput
	IconValidate = shared.IconValidate

	IconThinking   = shared.IconThinking
	IconToolCall   = shared.IconToolCall
	IconToolResult = shared.IconToolResult

	CollapsedMarker = shared.CollapsedMarker
	ExpandedMarker  = shared.ExpandedMarker

	BarThick  = shared.BarThick
	CursorBar = shared.CursorBar
	RuleGlyph = shared.RuleGlyph
)

// Hex color aliases for selector.go which references the old unexported
// color tokens from the former styles.go. Keep unexported so the coupling
// is explicit and local.
const (
	hexCharple = "#6B50FF"
	hexDolly   = "#FF60FF"
	hexSash    = "#ECEBF0"
	hexSquid   = "#858392"
	hexOyster  = "#605F6B"
)

// theme is the package-level style singleton, aliasing the shared package's
// Theme so existing tui files can continue to write theme.X without change.
var theme = shared.Theme
