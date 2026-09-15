package shared

// Icon vocabulary. Centralized (crush-style) so glyphs stay consistent and a
// single edit re-skins every call site.
//
// Slice-14 note: the values below are populated at package init from the
// active symbol preset in symbols.go — do NOT reassign at call sites. Reads
// are unchanged; the identifiers keep their pre-slice-14 names so the
// vocabulary refactor is name-preserving. SetPreset(PresetASCII) swaps the
// active table and re-runs refreshVocabulary, which writes back into the
// vars declared here. The Unicode values in unicodeSymbols reproduce the
// literals this file carried on main so a run without --ascii is
// byte-identical to it.
var (
	IconSuccess  string
	IconError    string
	IconPending  string
	IconRunning  string
	IconSkipped  string
	IconReview   string
	IconInput    string
	IconValidate string

	IconThinking   string
	IconToolCall   string
	IconToolResult string

	// Step-indicator vocabulary (monitor_steps.go).
	IconRecovery     string
	IconStateUnknown string

	// Status glyphs for the tool-exchange status-line header. These are the
	// slot-independent state marks: RenderStatusLine writes them into the icon
	// slot, ToolStatusIcon supplies them per state. Running and pending share
	// their glyph so a settling row does not twitch (epic CC-4). Substitution
	// happens through the preset table in symbols.go; do not introduce a
	// parallel per-state literal at renderer call sites.
	IconStatusSuccess string
	IconStatusError   string
	IconStatusRunning string
	IconStatusPending string
	IconStatusWarning string

	// Per-tool signature glyphs rendered on settled successful exchanges. The
	// generic pending/running glyph remains until settling: the settled-success
	// swap is the only glyph change permitted, enforced by ToolStatusIcon.
	// Every glyph is single-cell (verified by a width audit in status_icon_test
	// and TestChartGlyphSingleCellUnderBothPresets); double-width variants are
	// forbidden.
	IconToolRead   string
	IconToolEdit   string
	IconToolWrite  string
	IconToolSearch string
	IconToolShell  string
	IconToolWeb    string
	IconToolAgent  string
	IconToolTodo   string
	IconToolAsk    string

	CollapsedMarker   string
	ExpandedMarker    string
	TreeBranchGlyph   string
	TreeLastGlyph     string
	TreeContinueGlyph string

	CursorBar     string // selected-row marker
	RuleGlyph     string
	EllipsisGlyph string
	CommentGlyph  string
	LoopGlyph     string
	RetryGlyph    string
	GateGlyph     string
	ForEachGlyph  string // compact "×N" runtime fan-out family annotation (A8)

	// Chart connectors (detail chart view). ArrowDown terminates a normal
	// depends_on edge into a node; CondArrow marks a `when`-guarded edge;
	// ArrowLeft terminates a loop back-edge into its goto target. All three
	// must remain single-rune under every preset because chart/render.go
	// composes them via `[]rune(glyph)[0]`.
	ArrowDownGlyph string
	CondArrowGlyph string
	ArrowLeftGlyph string

	// Box-drawing glyphs used by card and panel chrome. Kept alongside the
	// singleton vocabulary so slice 14's preset flip covers card corners,
	// section tees, and the vertical body edge in one place.
	BoxCornerTL string
	BoxCornerTR string
	BoxCornerBL string
	BoxCornerBR string
	BoxTeeL     string
	BoxTeeR     string
	BoxVertical string
	DiffGutter  string
)

// PulseFrames is the running-step thinking pulse's default single-cell
// animation frame set (slice 10, FR-10.4/10.5). PulseFramesASCII is its
// single-cell ASCII fallback — populated by slice 14's preset table so
// callers reach the ASCII form through the same identifier without a
// bespoke selector at each call site.
var (
	PulseFrames      []string
	PulseFramesASCII = []string{".", "o", "O", "o"}
)

// refreshVocabulary copies activeSymbols into the exported vars above. It
// runs at package init (via icons.go's init below) and again on every
// SetPreset call in symbols.go. Writes are in-place so callers that read
// through the same identifier pick up the new preset without holding a
// stale copy.
func refreshVocabulary() {
	t := activeSymbols

	IconSuccess = t.IconSuccess
	IconError = t.IconError
	IconPending = t.IconPending
	IconRunning = t.IconRunning
	IconSkipped = t.IconSkipped
	IconReview = t.IconReview
	IconInput = t.IconInput
	IconValidate = t.IconValidate

	IconThinking = t.IconThinking
	IconToolCall = t.IconToolCall
	IconToolResult = t.IconToolResult

	IconRecovery = t.IconRecovery
	IconStateUnknown = t.IconStateUnknown

	IconStatusSuccess = t.IconStatusSuccess
	IconStatusError = t.IconStatusError
	IconStatusRunning = t.IconStatusRunning
	IconStatusPending = t.IconStatusPending
	IconStatusWarning = t.IconStatusWarning

	IconToolRead = t.IconToolRead
	IconToolEdit = t.IconToolEdit
	IconToolWrite = t.IconToolWrite
	IconToolSearch = t.IconToolSearch
	IconToolShell = t.IconToolShell
	IconToolWeb = t.IconToolWeb
	IconToolAgent = t.IconToolAgent
	IconToolTodo = t.IconToolTodo
	IconToolAsk = t.IconToolAsk

	CollapsedMarker = t.CollapsedMarker
	ExpandedMarker = t.ExpandedMarker
	TreeBranchGlyph = t.TreeBranchGlyph
	TreeLastGlyph = t.TreeLastGlyph
	TreeContinueGlyph = t.TreeContinueGlyph

	CursorBar = t.CursorBar
	RuleGlyph = t.RuleGlyph
	EllipsisGlyph = t.EllipsisGlyph
	CommentGlyph = t.CommentGlyph
	LoopGlyph = t.LoopGlyph
	RetryGlyph = t.RetryGlyph
	GateGlyph = t.GateGlyph
	ForEachGlyph = t.ForEachGlyph

	ArrowDownGlyph = t.ArrowDownGlyph
	CondArrowGlyph = t.CondArrowGlyph
	ArrowLeftGlyph = t.ArrowLeftGlyph

	BoxCornerTL = t.BoxCornerTL
	BoxCornerTR = t.BoxCornerTR
	BoxCornerBL = t.BoxCornerBL
	BoxCornerBR = t.BoxCornerBR
	BoxTeeL = t.BoxTeeL
	BoxTeeR = t.BoxTeeR
	BoxVertical = t.BoxVertical
	DiffGutter = t.DiffGutter

	PulseFrames = t.PulseFrames
}

func init() {
	refreshVocabulary()
}
