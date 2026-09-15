package shared

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
)

// TestSymbolTablesComplete locks FR-14.2 / FR-14.7: every key in one
// preset is present in the other, either with a concrete value or with
// an explicit empty string. A key added to symbolTable without a matching
// entry in both tables is a silent bug — the exported var in icons.go
// would zero-init and every consumer would render nothing. `reflect`
// walks the fields so a new key added later is checked without editing
// this test.
func TestSymbolTablesComplete(t *testing.T) {
	unicode := reflect.ValueOf(unicodeSymbols)
	ascii := reflect.ValueOf(asciiSymbols)
	typ := unicode.Type()

	for i := 0; i < unicode.NumField(); i++ {
		name := typ.Field(i).Name
		u := unicode.Field(i)
		a := ascii.Field(i)
		if u.Kind() != a.Kind() {
			t.Errorf("field %s: kind mismatch %v vs %v", name, u.Kind(), a.Kind())
			continue
		}
		switch u.Kind() {
		case reflect.String:
			// A glyph key MUST have a Unicode value; ASCII may be
			// empty (elides the affordance per FR-14.7).
			if u.String() == "" {
				t.Errorf("unicode.%s is empty; every vocabulary key needs a Unicode form", name)
			}
		case reflect.Slice:
			if u.Len() == 0 {
				t.Errorf("unicode.%s is empty; frame sets need at least one entry", name)
			}
			if a.Len() == 0 {
				t.Errorf("ascii.%s is empty; frame sets need at least one ASCII fallback entry", name)
			}
		default:
			t.Fatalf("field %s uses unsupported kind %v; extend TestSymbolTablesComplete", name, u.Kind())
		}
	}
}

// TestStatusGlyphWidthContracts locks FR-14.6 for the status-line
// vocabulary. Under Unicode every status glyph is single-cell; under
// ASCII the values may exceed one cell (e.g. `[ok]` is 4 cells) so
// the test asserts the value's declared width matches the audit table
// in docs/plans/omp-slice-14-glyph-presets.md. Values are checked by
// exact width rather than a range so a slice-14 author cannot silently
// widen `[!!]` to five cells and shift every consumer's padding.
func TestStatusGlyphWidthContracts(t *testing.T) {
	tests := []struct {
		name        string
		unicode     string
		ascii       string
		unicodeCell int
		asciiCell   int
	}{
		{"IconSuccess", unicodeSymbols.IconSuccess, asciiSymbols.IconSuccess, 1, 4},
		{"IconError", unicodeSymbols.IconError, asciiSymbols.IconError, 1, 4},
		{"IconPending", unicodeSymbols.IconPending, asciiSymbols.IconPending, 1, 3},
		{"IconRunning", unicodeSymbols.IconRunning, asciiSymbols.IconRunning, 1, 3},
		{"IconSkipped", unicodeSymbols.IconSkipped, asciiSymbols.IconSkipped, 1, 1},
		{"IconInput", unicodeSymbols.IconInput, asciiSymbols.IconInput, 1, 3},
		{"IconValidate", unicodeSymbols.IconValidate, asciiSymbols.IconValidate, 1, 2},
		{"IconStatusSuccess", unicodeSymbols.IconStatusSuccess, asciiSymbols.IconStatusSuccess, 1, 1},
		{"IconStatusError", unicodeSymbols.IconStatusError, asciiSymbols.IconStatusError, 1, 1},
		{"IconStatusRunning", unicodeSymbols.IconStatusRunning, asciiSymbols.IconStatusRunning, 1, 1},
		{"IconStatusPending", unicodeSymbols.IconStatusPending, asciiSymbols.IconStatusPending, 1, 1},
		{"IconStatusWarning", unicodeSymbols.IconStatusWarning, asciiSymbols.IconStatusWarning, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lipgloss.Width(tt.unicode); got != tt.unicodeCell {
				t.Errorf("unicode.%s = %q width = %d, want %d", tt.name, tt.unicode, got, tt.unicodeCell)
			}
			if got := lipgloss.Width(tt.ascii); got != tt.asciiCell {
				t.Errorf("ascii.%s = %q width = %d, want %d", tt.name, tt.ascii, got, tt.asciiCell)
			}
		})
	}
}

// TestChartGlyphSingleCellUnderBothPresets locks the chart-grid
// invariant: chart/render.go composes ArrowDown/CondArrow/ArrowLeft/
// LoopGlyph via `[]rune(glyph)[0]`, so a multi-rune ASCII form would
// overflow the grid. Also asserts single-cell width so the grid
// column arithmetic is stable across presets.
func TestChartGlyphSingleCellUnderBothPresets(t *testing.T) {
	chartKeys := []struct {
		name    string
		unicode string
		ascii   string
	}{
		{"ArrowDownGlyph", unicodeSymbols.ArrowDownGlyph, asciiSymbols.ArrowDownGlyph},
		{"CondArrowGlyph", unicodeSymbols.CondArrowGlyph, asciiSymbols.CondArrowGlyph},
		{"ArrowLeftGlyph", unicodeSymbols.ArrowLeftGlyph, asciiSymbols.ArrowLeftGlyph},
		{"LoopGlyph", unicodeSymbols.LoopGlyph, asciiSymbols.LoopGlyph},
		{"RetryGlyph", unicodeSymbols.RetryGlyph, asciiSymbols.RetryGlyph},
		{"GateGlyph", unicodeSymbols.GateGlyph, asciiSymbols.GateGlyph},
		{"ForEachGlyph", unicodeSymbols.ForEachGlyph, asciiSymbols.ForEachGlyph},
	}
	for _, tt := range chartKeys {
		t.Run(tt.name, func(t *testing.T) {
			if got := len([]rune(tt.unicode)); got != 1 {
				t.Errorf("unicode.%s = %q rune count = %d, want 1", tt.name, tt.unicode, got)
			}
			if got := len([]rune(tt.ascii)); got != 1 {
				t.Errorf("ascii.%s = %q rune count = %d, want 1 (chart grid invariant)", tt.name, tt.ascii, got)
			}
			if got := lipgloss.Width(tt.unicode); got != 1 {
				t.Errorf("unicode.%s = %q width = %d, want 1", tt.name, tt.unicode, got)
			}
			if got := lipgloss.Width(tt.ascii); got != 1 {
				t.Errorf("ascii.%s = %q width = %d, want 1", tt.name, tt.ascii, got)
			}
		})
	}
}

// TestPulseFramesSingleCellUnderBothPresets asserts every frame in
// the running-step thinking pulse set is single-cell under both
// presets (FR-10.5) so the reasoning row's width does not shift as
// frames advance.
func TestPulseFramesSingleCellUnderBothPresets(t *testing.T) {
	for i, frame := range unicodeSymbols.PulseFrames {
		if got := lipgloss.Width(frame); got != 1 {
			t.Errorf("unicode PulseFrames[%d] = %q width = %d, want 1", i, frame, got)
		}
	}
	for i, frame := range asciiSymbols.PulseFrames {
		if got := lipgloss.Width(frame); got != 1 {
			t.Errorf("ascii PulseFrames[%d] = %q width = %d, want 1", i, frame, got)
		}
	}
}

// TestSetPresetTogglesExportedVars proves SetPreset mutates the
// exported vars in place. A caller reading through the same identifier
// (e.g. `shared.IconSuccess`) after SetPreset(PresetASCII) picks up
// the new value without recompiling or restarting.
func TestSetPresetTogglesExportedVars(t *testing.T) {
	t.Cleanup(func() { SetPreset(PresetUnicode) })

	SetPreset(PresetUnicode)
	if got := IconSuccess; got != unicodeSymbols.IconSuccess {
		t.Errorf("after SetPreset(Unicode): IconSuccess = %q, want %q", got, unicodeSymbols.IconSuccess)
	}
	if got := CursorBar; got != unicodeSymbols.CursorBar {
		t.Errorf("after SetPreset(Unicode): CursorBar = %q, want %q", got, unicodeSymbols.CursorBar)
	}
	if got := BoxVertical; got != unicodeSymbols.BoxVertical {
		t.Errorf("after SetPreset(Unicode): BoxVertical = %q, want %q", got, unicodeSymbols.BoxVertical)
	}

	SetPreset(PresetASCII)
	if got := IconSuccess; got != asciiSymbols.IconSuccess {
		t.Errorf("after SetPreset(ASCII): IconSuccess = %q, want %q", got, asciiSymbols.IconSuccess)
	}
	if got := CursorBar; got != asciiSymbols.CursorBar {
		t.Errorf("after SetPreset(ASCII): CursorBar = %q, want %q", got, asciiSymbols.CursorBar)
	}
	if got := BoxVertical; got != asciiSymbols.BoxVertical {
		t.Errorf("after SetPreset(ASCII): BoxVertical = %q, want %q", got, asciiSymbols.BoxVertical)
	}

	SetPreset(PresetUnicode)
	if got := IconSuccess; got != unicodeSymbols.IconSuccess {
		t.Errorf("after round-trip: IconSuccess = %q, want %q", got, unicodeSymbols.IconSuccess)
	}
}

// TestSetPresetIsIdempotent proves two consecutive SetPreset calls with
// the same preset leave the vars in the same state, and a
// Unicode→ASCII→Unicode round trip restores every value byte-for-byte.
func TestSetPresetIsIdempotent(t *testing.T) {
	t.Cleanup(func() { SetPreset(PresetUnicode) })
	captureAll := func() []string {
		return []string{
			IconSuccess, IconError, IconPending, IconRunning, IconSkipped,
			IconReview, IconInput, IconValidate, IconThinking, IconToolCall,
			IconToolResult, IconStatusSuccess, IconStatusError, IconStatusRunning,
			IconStatusPending, IconStatusWarning, IconToolRead, IconToolEdit,
			IconToolWrite, IconToolSearch, IconToolShell, IconToolWeb, IconToolAgent,
			IconToolTodo, IconToolAsk, CollapsedMarker, ExpandedMarker,
			TreeBranchGlyph, TreeLastGlyph, TreeContinueGlyph, CursorBar, RuleGlyph,
			EllipsisGlyph, LoopGlyph, RetryGlyph, GateGlyph, ForEachGlyph,
			ArrowDownGlyph, CondArrowGlyph, ArrowLeftGlyph, BoxCornerTL, BoxCornerTR,
			BoxCornerBL, BoxCornerBR, BoxTeeL, BoxTeeR, BoxVertical, DiffGutter,
		}
	}

	SetPreset(PresetUnicode)
	before := captureAll()
	SetPreset(PresetUnicode)
	afterIdempotent := captureAll()
	if !reflect.DeepEqual(before, afterIdempotent) {
		t.Fatal("SetPreset(Unicode) applied twice mutated the vars")
	}

	SetPreset(PresetASCII)
	SetPreset(PresetUnicode)
	afterRoundTrip := captureAll()
	if !reflect.DeepEqual(before, afterRoundTrip) {
		t.Fatal("Unicode → ASCII → Unicode round trip did not restore every var")
	}
}

// TestActivePresetReflectsSetPreset locks the invariant that
// activePreset (used by spinner.go's activeSpinnerFrames) tracks the
// SetPreset call. A drift here would leave the spinner rotor mismatched
// against the rest of the vocabulary.
func TestActivePresetReflectsSetPreset(t *testing.T) {
	t.Cleanup(func() { SetPreset(PresetUnicode) })

	SetPreset(PresetUnicode)
	if got := activePreset(); got != PresetUnicode {
		t.Errorf("after SetPreset(Unicode): activePreset() = %v, want PresetUnicode", got)
	}
	SetPreset(PresetASCII)
	if got := activePreset(); got != PresetASCII {
		t.Errorf("after SetPreset(ASCII): activePreset() = %v, want PresetASCII", got)
	}
}

// TestSetPresetASCIIFlipsSpinner proves slice 13's frame source
// consults the same preset selector. A run under PresetASCII gets the
// ASCII rotor without a separate opt-in.
func TestSetPresetASCIIFlipsSpinner(t *testing.T) {
	t.Cleanup(func() { SetPreset(PresetUnicode) })

	anchor := time.Unix(0, 0)

	SetPreset(PresetUnicode)
	unicodeGlyph, ok := SpinnerFrame("status", anchor)
	if !ok {
		t.Fatal("SpinnerFrame(status) unavailable")
	}
	inUnicode := false
	for _, want := range spinnerFrameSets["status"].Unicode {
		if unicodeGlyph == want {
			inUnicode = true
			break
		}
	}
	if !inUnicode {
		t.Fatalf("Unicode glyph %q not in registered Unicode set %v", unicodeGlyph, spinnerFrameSets["status"].Unicode)
	}

	SetPreset(PresetASCII)
	asciiGlyph, ok := SpinnerFrame("status", anchor)
	if !ok {
		t.Fatal("SpinnerFrame(status) unavailable after preset flip")
	}
	if unicodeGlyph == asciiGlyph {
		t.Fatalf("preset flip did not change the frame: unicode=%q ascii=%q", unicodeGlyph, asciiGlyph)
	}
	inASCII := false
	for _, want := range spinnerFrameSets["status"].ASCII {
		if asciiGlyph == want {
			inASCII = true
			break
		}
	}
	if !inASCII {
		t.Fatalf("ASCII glyph %q not in registered ASCII set %v", asciiGlyph, spinnerFrameSets["status"].ASCII)
	}
}

// TestSetPresetASCIIFlipsPulseFrames proves the running-step thinking
// pulse consumes the preset flip through the same PulseFrames identifier
// slice 10 already reads. A caller reading `shared.PulseFrames` after
// SetPreset(PresetASCII) gets the ASCII set without a bespoke selector.
func TestSetPresetASCIIFlipsPulseFrames(t *testing.T) {
	t.Cleanup(func() { SetPreset(PresetUnicode) })

	SetPreset(PresetUnicode)
	unicodeFrames := append([]string(nil), PulseFrames...)
	SetPreset(PresetASCII)
	asciiFrames := append([]string(nil), PulseFrames...)
	if reflect.DeepEqual(unicodeFrames, asciiFrames) {
		t.Fatal("SetPreset(ASCII) did not swap PulseFrames away from the Unicode set")
	}
	if !reflect.DeepEqual(asciiFrames, asciiSymbols.PulseFrames) {
		t.Fatalf("PulseFrames under ASCII = %v, want %v", asciiFrames, asciiSymbols.PulseFrames)
	}
}

// TestASCIIVocabularyIsASCII locks FR-14.5 at the vocabulary layer: a
// glyph in asciiSymbols must not carry a rune above U+007F. This is the
// input side of the FR-14.5 output check that
// TestVocabularyASCIIOutputHasNoUnicode covers in the monitor package.
// Reflect walks every string field so a new key added later is checked
// without editing this test.
func TestASCIIVocabularyIsASCII(t *testing.T) {
	ascii := reflect.ValueOf(asciiSymbols)
	typ := ascii.Type()
	for i := 0; i < ascii.NumField(); i++ {
		if ascii.Field(i).Kind() != reflect.String {
			continue
		}
		val := ascii.Field(i).String()
		for j, r := range val {
			if r >= 0x80 {
				t.Errorf("ascii.%s[%d] = %U is above U+007F in %q", typ.Field(i).Name, j, r, val)
			}
		}
	}
	for i, frame := range asciiSymbols.PulseFrames {
		for j, r := range frame {
			if r >= 0x80 {
				t.Errorf("ascii.PulseFrames[%d][%d] = %U is above U+007F in %q", i, j, r, frame)
			}
		}
	}
}

// TestVocabularyEllipsisIsUsable is a smoke test that a caller can
// substitute an ansi.Truncate marker via shared.EllipsisGlyph and get
// a stable-width result under both presets. Consumers of ansi.Truncate
// pass EllipsisGlyph rather than the literal "…" so the ASCII preset's
// "..." tail flows through the same identifier.
func TestVocabularyEllipsisIsUsable(t *testing.T) {
	t.Cleanup(func() { SetPreset(PresetUnicode) })

	SetPreset(PresetUnicode)
	if !strings.Contains(EllipsisGlyph, "…") {
		t.Errorf("unicode EllipsisGlyph = %q, want to contain …", EllipsisGlyph)
	}
	SetPreset(PresetASCII)
	if EllipsisGlyph != "..." {
		t.Errorf("ascii EllipsisGlyph = %q, want ...", EllipsisGlyph)
	}
}
