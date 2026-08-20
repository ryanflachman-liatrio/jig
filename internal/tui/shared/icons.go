package shared

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

	BarThick   = "▌" // left accent bar on chat blocks
	CursorBar  = "▌" // selected-row marker
	RuleGlyph  = "─"
	LoopGlyph  = "↺"
	RetryGlyph = "↻"
	GateGlyph  = "⇢"

	// Chart connectors (detail chart view). ArrowDown terminates a normal
	// depends_on edge into a node; CondArrow marks a `when`-guarded edge;
	// ArrowLeft terminates a loop back-edge into its goto target.
	ArrowDownGlyph = "▼"
	CondArrowGlyph = "▽"
	ArrowLeftGlyph = "◄"
)
