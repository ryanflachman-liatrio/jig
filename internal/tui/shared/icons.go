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

	// Status glyphs for the tool-exchange status-line header. These are the
	// slot-independent state marks: RenderStatusLine writes them into the icon
	// slot, ToolStatusIcon supplies them per state. Running and pending share
	// their glyph so a settling row does not twitch (epic CC-4). Slice 14 will
	// substitute them via its preset table; do not introduce a parallel
	// per-state literal at renderer call sites.
	IconStatusSuccess = "•"
	IconStatusError   = "✗"
	IconStatusRunning = "○"
	IconStatusPending = "○"
	IconStatusWarning = "!"

	// Per-tool signature glyphs rendered on settled successful exchanges. The
	// generic pending/running glyph remains until settling: the settled-success
	// swap is the only glyph change permitted, enforced by ToolStatusIcon.
	// Every glyph is single-cell (verified by a width audit in status_icon_test);
	// double-width variants belong to slice 14's preset table.
	IconToolRead   = "◈"
	IconToolEdit   = "✎"
	IconToolWrite  = "✎"
	IconToolSearch = "⌕"
	IconToolShell  = "$"
	IconToolWeb    = "↗"
	IconToolAgent  = "⊙"
	IconToolTodo   = "⊙"
	IconToolAsk    = "?"

	CollapsedMarker = "▸"
	ExpandedMarker  = "▾"

	BarThick     = "▌" // left accent bar on chat blocks
	CursorBar    = "▌" // selected-row marker
	RuleGlyph    = "─"
	LoopGlyph    = "↺"
	RetryGlyph   = "↻"
	GateGlyph    = "⇢"
	ForEachGlyph = "×" // compact "×N" runtime fan-out family annotation (A8)

	// Chart connectors (detail chart view). ArrowDown terminates a normal
	// depends_on edge into a node; CondArrow marks a `when`-guarded edge;
	// ArrowLeft terminates a loop back-edge into its goto target.
	ArrowDownGlyph = "▼"
	CondArrowGlyph = "▽"
	ArrowLeftGlyph = "◄"
)
