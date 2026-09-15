package shared

// Preset-indexed glyph vocabulary (omp-transcript-parity slice 14).
//
// The exported glyph identifiers in icons.go are read at render time by every
// TUI package. Slice 14 keeps their names identical so call sites do not
// change, but promotes them from compile-time consts to package-level vars
// whose values are copied from an active symbol table at process start (and
// again whenever SetPreset is called). The mechanism lives here so a single
// edit reskins every consumer, per the header comment in icons.go and epic
// decision CC-7.
//
// Two invariants drive the design:
//
//   - Every key present in one preset is present in the other. A key that
//     has no reasonable ASCII form (`""`) is declared explicitly so the
//     consumer elides the affordance rather than substituting a misleading
//     glyph (FR-14.7).
//
//   - Chart-grid glyphs (ArrowDown/CondArrow/ArrowLeft/LoopGlyph) MUST
//     remain single-cell in both presets because chart/render.go composes
//     them by taking `[]rune(glyph)[0]`; a multi-rune ASCII form would
//     overflow the grid. Non-grid glyphs may exceed one cell in the ASCII
//     preset (`[ok]` is 4 cells), and consumers must measure with
//     lipgloss.Width rather than assuming one cell (FR-14.6).

// SymbolPreset selects a named vocabulary. The Nerd Font preset from omp
// is intentionally omitted (epic Deferred Work).
type SymbolPreset int

const (
	// PresetUnicode is the default; every glyph matches the pre-slice-14
	// values so a run without --ascii renders byte-identically to main.
	PresetUnicode SymbolPreset = iota
	// PresetASCII substitutes ASCII-only forms and elides affordances
	// with no reasonable ASCII (FR-14.5/14.7). Every rendered rune under
	// this preset satisfies r < 0x80.
	PresetASCII
)

// symbolTable is the concrete vocabulary for one preset. One field per
// exported identifier in icons.go, plus the running-step thinking pulse
// frame set used by slice 10. Adding a key here without mirroring it in
// icons.go leaves it inaccessible; adding it to icons.go without
// mirroring it here would leave the var at the type's zero value (empty
// string) and TestSymbolTablesComplete fails at PR review.
type symbolTable struct {
	// Item-level indicators (line-anchored; multi-cell ASCII allowed).
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

	// Status-line header glyphs (slice 02).
	IconStatusSuccess string
	IconStatusError   string
	IconStatusRunning string
	IconStatusPending string
	IconStatusWarning string

	// Per-tool signature glyphs (settled-success marks; single-cell in both
	// presets so ToolStatusIcon's swap-on-settle stays width-invariant).
	IconToolRead   string
	IconToolEdit   string
	IconToolWrite  string
	IconToolSearch string
	IconToolShell  string
	IconToolWeb    string
	IconToolAgent  string
	IconToolTodo   string
	IconToolAsk    string

	// Structural markers.
	CollapsedMarker   string
	ExpandedMarker    string
	TreeBranchGlyph   string
	TreeLastGlyph     string
	TreeContinueGlyph string
	CursorBar         string
	RuleGlyph         string
	EllipsisGlyph     string

	// Chart-grid glyphs (single-cell in both presets).
	LoopGlyph      string
	RetryGlyph     string
	GateGlyph      string
	ForEachGlyph   string
	ArrowDownGlyph string
	CondArrowGlyph string
	ArrowLeftGlyph string

	// Box drawing (card and panel chrome).
	BoxCornerTL string
	BoxCornerTR string
	BoxCornerBL string
	BoxCornerBR string
	BoxTeeL     string
	BoxTeeR     string
	BoxVertical string
	DiffGutter  string

	// Slice-10 running-step thinking pulse frames. Both slices are
	// single-cell; the ASCII fallback is registered here so slice 14's
	// preset flip covers the pulse in one place (matching spinner.go's
	// SpinnerFrames.ASCII contract for slice 13's status rotor).
	PulseFrames []string
}

// unicodeSymbols reproduces the pre-slice-14 vocabulary verbatim so a run
// without --ascii is byte-identical to main.
var unicodeSymbols = symbolTable{
	IconSuccess:  "✓",
	IconError:    "✗",
	IconPending:  "○",
	IconRunning:  "●",
	IconSkipped:  "—",
	IconReview:   "?",
	IconInput:    "⊙",
	IconValidate: "⇢",

	IconThinking:   "◇",
	IconToolCall:   "▸",
	IconToolResult: "↳",

	IconStatusSuccess: "•",
	IconStatusError:   "✗",
	IconStatusRunning: "○",
	IconStatusPending: "○",
	IconStatusWarning: "!",

	IconToolRead:   "◈",
	IconToolEdit:   "✎",
	IconToolWrite:  "✎",
	IconToolSearch: "⌕",
	IconToolShell:  "$",
	IconToolWeb:    "↗",
	IconToolAgent:  "⊙",
	IconToolTodo:   "⊙",
	IconToolAsk:    "?",

	CollapsedMarker:   "▸",
	ExpandedMarker:    "▾",
	TreeBranchGlyph:   "├─",
	TreeLastGlyph:     "└─",
	TreeContinueGlyph: "│ ",
	CursorBar:         "▌",
	RuleGlyph:         "─",
	EllipsisGlyph:     "…",

	LoopGlyph:      "↺",
	RetryGlyph:     "↻",
	GateGlyph:      "⇢",
	ForEachGlyph:   "×",
	ArrowDownGlyph: "▼",
	CondArrowGlyph: "▽",
	ArrowLeftGlyph: "◄",

	BoxCornerTL: "╭",
	BoxCornerTR: "╮",
	BoxCornerBL: "╰",
	BoxCornerBR: "╯",
	BoxTeeL:     "├",
	BoxTeeR:     "┤",
	BoxVertical: "│",
	DiffGutter:  "│",

	PulseFrames: []string{"·", "•", "●", "•"},
}

// asciiSymbols is the ASCII-only vocabulary consulted when the operator
// invokes `jig --ascii`. Non-grid keys may exceed one cell (`[ok]`);
// chart-grid keys stay single-rune (see the comment on symbolTable). A key
// with no reasonable ASCII form (`""`) elides the affordance.
var asciiSymbols = symbolTable{
	IconSuccess:  "[ok]",
	IconError:    "[!!]",
	IconPending:  "[ ]",
	IconRunning:  "[o]",
	IconSkipped:  "-",
	IconReview:   "?",
	IconInput:    "[i]",
	IconValidate: "->",

	IconThinking:   "~",
	IconToolCall:   ">",
	IconToolResult: "->",

	IconStatusSuccess: "*",
	IconStatusError:   "!",
	IconStatusRunning: ".",
	IconStatusPending: ".",
	IconStatusWarning: "!",

	IconToolRead:   "r",
	IconToolEdit:   "e",
	IconToolWrite:  "w",
	IconToolSearch: "s",
	IconToolShell:  "$",
	IconToolWeb:    "@",
	IconToolAgent:  "A",
	IconToolTodo:   "T",
	IconToolAsk:    "?",

	CollapsedMarker:   ">",
	ExpandedMarker:    "v",
	TreeBranchGlyph:   "|-",
	TreeLastGlyph:     "'-",
	TreeContinueGlyph: "| ",
	CursorBar:         "|",
	RuleGlyph:         "-",
	EllipsisGlyph:     "...",

	LoopGlyph:      "L",
	RetryGlyph:     "R",
	GateGlyph:      "G",
	ForEachGlyph:   "x",
	ArrowDownGlyph: "v",
	CondArrowGlyph: "V",
	ArrowLeftGlyph: "<",

	BoxCornerTL: "+",
	BoxCornerTR: "+",
	BoxCornerBL: "+",
	BoxCornerBR: "+",
	BoxTeeL:     "+",
	BoxTeeR:     "+",
	BoxVertical: "|",
	DiffGutter:  "|",

	PulseFrames: []string{".", "o", "O", "o"},
}

// activeSymbols points at the vocabulary currently reflected by the vars
// in icons.go. SetPreset swaps the pointer; the exported vars are
// re-populated by refreshVocabulary so no call site needs to consult
// activeSymbols directly.
var activeSymbols = &unicodeSymbols

// activePreset reports which SymbolPreset activeSymbols reflects. Kept
// package-private so callers cannot branch on the preset ad-hoc; the two
// predicates that need it (spinner.go's activeSpinnerFrames, and the
// pulse fallback the monitor consumes through PulseFrames) live in this
// package.
func activePreset() SymbolPreset {
	if activeSymbols == &asciiSymbols {
		return PresetASCII
	}
	return PresetUnicode
}

// SetPreset selects the active glyph vocabulary and re-populates the
// exported vars in icons.go so subsequent renders use the new preset.
// Safe to call before any TUI code initializes; idempotent. cmd/jig
// calls it exactly once at process start when --ascii is present.
// Toggling mid-run works for tests but is not a supported operator
// workflow — cached renders in the monitor are not invalidated on the
// switch, so a live-run flip would show a mixed panel until the next
// natural repaint.
func SetPreset(p SymbolPreset) {
	switch p {
	case PresetASCII:
		activeSymbols = &asciiSymbols
	default:
		activeSymbols = &unicodeSymbols
	}
	refreshVocabulary()
}
