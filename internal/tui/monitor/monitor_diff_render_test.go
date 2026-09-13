package monitor

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"jig/internal/tui/diffview"
)

// mustCompute is a small helper so table cases can construct
// projections without repeating boilerplate.
func mustCompute(t *testing.T, path, oldText, newText string) *diffProjection {
	t.Helper()
	proj, _, outcome := computeDiff(newDiff(path, oldText, newText))
	if outcome != computeOK {
		t.Fatalf("computeDiff outcome = %v, want computeOK", outcome)
	}
	return proj
}

// TestRenderDiffGutterShape verifies the fused-gutter shape:
// add/remove rows show `<marker><line-no>│<content>` with the marker
// and number as one right-aligned token, and context rows show only
// the line number (FR-07.6).
func TestRenderDiffGutterShape(t *testing.T) {
	proj := mustCompute(t, "sample.go", "alpha\nbeta\ngamma\n", "alpha\nBETA\ngamma\n")
	rows := renderDiffRows(proj, "sample.go", 60, true, nil, "space")
	if len(rows) == 0 {
		t.Fatal("expected at least one rendered row")
	}
	sawMarkerNumber := false
	for _, row := range rows {
		plain := stripANSI(row)
		if !strings.Contains(plain, "│") {
			t.Fatalf("row missing │ column: %q", plain)
		}
		gutter := plain[:strings.Index(plain, "│")]
		trimmed := strings.TrimLeft(gutter, " ")
		if trimmed == "" {
			continue // wrap-continuation row or context row with no visible number
		}
		if trimmed[0] == '+' || trimmed[0] == '-' {
			if len(trimmed) > 1 {
				if trimmed[1] < '0' || trimmed[1] > '9' {
					t.Fatalf("marker/number must be concatenated (no separator): %q", gutter)
				}
				sawMarkerNumber = true
			}
			continue
		}
		if trimmed[0] >= '0' && trimmed[0] <= '9' {
			continue // context row: unmarked line number
		}
		t.Fatalf("unexpected gutter shape %q", gutter)
	}
	if !sawMarkerNumber {
		t.Fatal("expected at least one row with a fused marker+number token")
	}
}

// TestRenderDiffWidthFloor verifies the gutter is floored at width 3
// on a 5-line file and grows to accommodate max line digits for a
// 1000-line file (FR-07.7).
func TestRenderDiffWidthFloor(t *testing.T) {
	small := mustCompute(t, "s.go", "1\n2\n3\n4\n5\n", "1\n2\nX\n4\n5\n")
	if gw := gutterWidth(small.Presentation); gw != 3 {
		t.Fatalf("5-line file gutter width = %d, want 3 (floor)", gw)
	}
	var oldB, newB strings.Builder
	for i := 1; i <= 1000; i++ {
		if i == 999 {
			oldB.WriteString("line-x\n")
			newB.WriteString("line-Y\n")
			continue
		}
		oldB.WriteString("line-x\n")
		newB.WriteString("line-x\n")
	}
	big := mustCompute(t, "b.go", oldB.String(), newB.String())
	if gw := gutterWidth(big.Presentation); gw != 5 {
		t.Fatalf("1000-line file gutter width = %d, want 5 (1 marker + 4 digits)", gw)
	}
}

// TestRenderDiffDuplicateSuppression verifies that a `-N` followed by
// `+N` renders the second row with only the marker (line number
// suppressed) and that suppression does not leak across hunks
// (FR-07.8).
func TestRenderDiffDuplicateSuppression(t *testing.T) {
	proj := mustCompute(t, "s.go", "a\nold\nc\n", "a\nnew\nc\n")
	rows := renderDiffRows(proj, "s.go", 60, true, nil, "space")
	// Look for a delete row on line 2 followed by an add row on line 2.
	var sawDelete2, sawAddSuppressed bool
	for _, row := range rows {
		plain := stripANSI(row)
		gutter := plain[:strings.Index(plain, "│")]
		trimmed := strings.TrimLeft(gutter, " ")
		if trimmed == "-2" {
			sawDelete2 = true
			continue
		}
		if sawDelete2 && trimmed == "+" {
			sawAddSuppressed = true
			break
		}
	}
	if !sawDelete2 || !sawAddSuppressed {
		t.Fatalf("expected -2 followed by suppressed + row; gutters were:\n%s", strings.Join(stripAllANSI(rows), "\n"))
	}
}

// TestRenderDiffIntralineReverseVideo verifies a 1↔1 replacement
// receives reverse-video highlighting (SGR 7) on the changed span
// (FR-07.9).
func TestRenderDiffIntralineReverseVideo(t *testing.T) {
	proj := mustCompute(t, "s.go", "hello world\n", "hello sunny world\n")
	rows := renderDiffRows(proj, "s.go", 80, true, nil, "space")
	joined := strings.Join(rows, "\n")
	if !containsReverseVideo(joined) {
		t.Fatalf("expected reverse-video escape in 1↔1 diff output; got:\n%s", joined)
	}
}

// containsReverseVideo reports whether s contains an SGR sequence
// that enables reverse video (parameter 7), whether alone or combined
// with other parameters. Lipgloss v2 may fuse `Reverse(true)` with a
// foreground token as `\x1b[7;38;...m` so a naive `\x1b[7m` search
// misses the combined form.
func containsReverseVideo(s string) bool {
	for i := 0; i+2 < len(s); i++ {
		if s[i] != '\x1b' || s[i+1] != '[' {
			continue
		}
		end := i + 2
		for end < len(s) && s[end] != 'm' {
			end++
		}
		if end >= len(s) {
			continue
		}
		params := s[i+2 : end]
		for _, p := range strings.Split(params, ";") {
			if p == "7" {
				return true
			}
		}
		i = end
	}
	return false
}

// TestRenderDiffIntralineExcludesLeadingWhitespace verifies that a
// leading-whitespace-only difference does not receive reverse-video
// highlighting (FR-07.9 leading-whitespace exclusion).
func TestRenderDiffIntralineExcludesLeadingWhitespace(t *testing.T) {
	proj := mustCompute(t, "s.go", "foo\n", "  foo\n")
	rows := renderDiffRows(proj, "s.go", 80, true, nil, "space")
	joined := strings.Join(rows, "\n")
	if containsReverseVideo(joined) {
		t.Fatalf("leading-whitespace-only difference should not be reverse-video: %q", joined)
	}
}

// TestRenderDiffMultiLineNoIntraline verifies that a multi-line change
// block does not receive intra-line highlighting (FR-07.9).
func TestRenderDiffMultiLineNoIntraline(t *testing.T) {
	proj := mustCompute(t, "s.go", "a\nb\nc\n", "a\nX\nY\n")
	rows := renderDiffRows(proj, "s.go", 80, true, nil, "space")
	joined := strings.Join(rows, "\n")
	if containsReverseVideo(joined) {
		t.Fatalf("multi-line change should not be reverse-video: %q", joined)
	}
}

// TestRenderDiffIndentation asserts that leading spaces render as dim
// middle-dots and leading tabs as dim arrows (FR-07.11).
func TestRenderDiffIndentation(t *testing.T) {
	proj := mustCompute(t, "s.go", "\tx\n    y\n", "\tX\n    Y\n")
	rows := renderDiffRows(proj, "s.go", 60, true, nil, "space")
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "·") {
		t.Fatalf("expected · glyph for leading spaces:\n%s", joined)
	}
	if !strings.Contains(joined, "→") {
		t.Fatalf("expected → glyph for leading tabs:\n%s", joined)
	}
	if !strings.Contains(joined, "\x1b[2m·") && !strings.Contains(joined, "\x1b[2m→") {
		t.Fatalf("indentation glyphs should be dim; missing \x1b[2m in:\n%s", joined)
	}
}

// TestRenderDiffNoBackground asserts the renderer emits no `\x1b[48`
// background sequence — add/remove/context colors are foreground-only
// so the card's state tint owns the row background (FR-07.12).
func TestRenderDiffNoBackground(t *testing.T) {
	proj := mustCompute(t, "s.go", "a\nb\nc\n", "a\nB\nc\n")
	rows := renderDiffRows(proj, "s.go", 60, true, nil, "space")
	for _, row := range rows {
		if strings.Contains(row, "\x1b[48") {
			t.Fatalf("row carries a background SGR sequence:\n%q", row)
		}
	}
}

// TestRenderDiffWrapContinuation asserts a row wider than the content
// width wraps with a blank gutter continuation and each row terminates
// with the SGR closer (FR-07.13).
func TestRenderDiffWrapContinuation(t *testing.T) {
	long := strings.Repeat("x", 200) + "\n"
	proj := mustCompute(t, "s.go", "a\n"+long+"c\n", "a\ny\nc\n")
	rows := renderDiffRows(proj, "s.go", 40, true, nil, "space")
	sawWrap := false
	for _, row := range rows {
		if !strings.HasSuffix(row, sgrRowTerminator) {
			t.Fatalf("row missing SGR terminator: %q", row)
		}
		plain := stripANSI(row)
		gutter := plain[:strings.Index(plain, "│")]
		if strings.TrimSpace(gutter) == "" {
			sawWrap = true
		}
	}
	if !sawWrap {
		t.Fatalf("expected at least one wrap-continuation row with blank gutter; rows:\n%s", strings.Join(stripAllANSI(rows), "\n"))
	}
}

// TestRenderDiffCollapseBudget verifies the collapsed-diff footer
// composes hunk and line counts through the slice-06 vocabulary with
// a single leading ellipsis (FR-07.14).
func TestRenderDiffCollapseBudget(t *testing.T) {
	// Build many well-separated hunks by placing large runs of shared
	// context between each single-line change so unified-diff hunk
	// coalescing does not fuse them.
	var oldB, newB strings.Builder
	const separatorLen = 20
	for i := 0; i < 12; i++ {
		for j := 0; j < separatorLen; j++ {
			line := "shared-" + itoa(i) + "-" + itoa(j) + "\n"
			oldB.WriteString(line)
			newB.WriteString(line)
		}
		oldB.WriteString("old-" + itoa(i) + "\n")
		newB.WriteString("new-" + itoa(i) + "\n")
	}
	proj := mustCompute(t, "many.go", oldB.String(), newB.String())
	if len(proj.Presentation.Hunks) <= diffCollapsedHunks {
		t.Fatalf("expected >%d hunks to exercise collapse; got %d", diffCollapsedHunks, len(proj.Presentation.Hunks))
	}
	rows := renderDiffRows(proj, "many.go", 80, false, nil, "space")
	if len(rows) == 0 {
		t.Fatal("expected some collapsed rows")
	}
	footer := stripANSI(rows[len(rows)-1])
	if !strings.Contains(footer, "more") {
		t.Fatalf("expected collapse footer with `more` phrase; got %q", footer)
	}
	if strings.Count(footer, "…") != 1 {
		t.Fatalf("footer must have exactly one leading ellipsis; got %q", footer)
	}
	if !strings.Contains(footer, "Expand") {
		t.Fatalf("footer must carry the live expand-key hint; got %q", footer)
	}
}

// TestRenderDiffCollapseFooterVocabulary asserts the collapse footer
// text is built from the slice-06 helpers verbatim (FR-07.14 + slice-06
// grep-lock).
func TestRenderDiffCollapseFooterVocabulary(t *testing.T) {
	cases := []struct {
		hh, hl int
		want   string
	}{
		{3, 12, "… 3 more hunks, 12 more lines [space: Expand]"},
		{0, 12, "… 12 more lines [space: Expand]"},
		{3, 0, "… 3 more hunks [space: Expand]"},
	}
	for _, tc := range cases {
		got := stripANSI(buildCollapseFooter(tc.hh, tc.hl, false, "space"))
		if got != tc.want {
			t.Fatalf("footer(%d,%d) = %q, want %q", tc.hh, tc.hl, got, tc.want)
		}
	}
	if got := buildCollapseFooter(0, 0, false, "space"); stripANSI(got) != "" {
		t.Fatalf("footer(0,0) = %q, want empty", got)
	}
}

// TestRenderDiffRowsNilInputs guards the nil-safe path.
func TestRenderDiffRowsNilInputs(t *testing.T) {
	if got := renderDiffRows(nil, "", 40, true, nil, ""); got != nil {
		t.Fatalf("renderDiffRows(nil) = %v, want nil", got)
	}
	empty := &diffProjection{Presentation: &diffview.Presentation{}}
	if got := renderDiffRows(empty, "", 40, true, nil, ""); got != nil {
		t.Fatalf("renderDiffRows(empty) = %v, want nil", got)
	}
}

// TestRenderDiffRowsHasVisibleWidth checks that no rendered row
// exceeds the requested width (post-wrap).
func TestRenderDiffRowsHasVisibleWidth(t *testing.T) {
	proj := mustCompute(t, "s.go", "alpha\nbeta\ngamma\n", "alpha\nBETA\ngamma\n")
	width := 40
	rows := renderDiffRows(proj, "s.go", width, true, nil, "space")
	for _, row := range rows {
		if got := lipgloss.Width(row); got > width {
			t.Fatalf("row width %d > requested %d: %q", got, width, row)
		}
	}
}

// itoa is a tiny helper so test cases can build synthetic sources
// without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return sign + string(digits[i:])
}

// stripAllANSI applies stripANSI to each element and returns the
// list. Handy for readable test failure messages.
func stripAllANSI(rows []string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = stripANSI(r)
	}
	return out
}
