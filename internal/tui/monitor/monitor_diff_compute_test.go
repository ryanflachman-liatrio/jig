package monitor

import (
	"fmt"
	"strings"
	"testing"

	"jig/internal/toolcall"
	"jig/internal/tui/diffview"
)

func stringPtr(s string) *string { return &s }

func newDiff(path, oldText, newText string) *toolcall.Diff {
	return &toolcall.Diff{Path: path, OldText: stringPtr(oldText), NewText: newText}
}

func TestComputeDiffSingleLine(t *testing.T) {
	old := "a\nb\nc\n"
	nw := "a\nB\nc\n"
	proj, stats, outcome := computeDiff(newDiff("sample.go", old, nw))
	if outcome != computeOK {
		t.Fatalf("outcome = %v, want computeOK", outcome)
	}
	if proj == nil || proj.Presentation == nil || proj.Presentation.ParseErr != nil {
		t.Fatalf("projection = %+v, want valid", proj)
	}
	pres := proj.Presentation
	if len(pres.Files) != 1 || pres.Files[0].Name != "sample.go" {
		t.Fatalf("files = %+v, want one 'sample.go'", pres.Files)
	}
	if len(pres.Hunks) != 1 {
		t.Fatalf("hunks = %d, want 1", len(pres.Hunks))
	}
	if stats != (diffStats{added: 1, removed: 1}) {
		t.Fatalf("stats = %+v, want +1/-1", stats)
	}
	if len(proj.PatchLines) == 0 {
		t.Fatal("expected patch lines to be retained on projection")
	}
}

func TestComputeDiffMultiLine(t *testing.T) {
	old := "a\nb\nc\n"
	nw := "a\nX\nY\n"
	_, stats, outcome := computeDiff(newDiff("m.go", old, nw))
	if outcome != computeOK {
		t.Fatalf("outcome = %v, want computeOK", outcome)
	}
	if stats.added < 2 || stats.removed < 2 {
		t.Fatalf("stats = %+v, want at least +2/-2", stats)
	}
}

func TestComputeDiffInsertOnly(t *testing.T) {
	old := "a\nb\n"
	nw := "a\nb\nd\n"
	_, stats, outcome := computeDiff(newDiff("i.go", old, nw))
	if outcome != computeOK {
		t.Fatalf("outcome = %v, want computeOK", outcome)
	}
	if stats.added != 1 || stats.removed != 0 {
		t.Fatalf("stats = %+v, want +1/-0", stats)
	}
}

func TestComputeDiffDeleteOnly(t *testing.T) {
	old := "a\nb\nc\n"
	nw := "a\nc\n"
	_, stats, outcome := computeDiff(newDiff("d.go", old, nw))
	if outcome != computeOK {
		t.Fatalf("outcome = %v, want computeOK", outcome)
	}
	if stats.added != 0 || stats.removed != 1 {
		t.Fatalf("stats = %+v, want +0/-1", stats)
	}
}

func TestComputeDiffNoChange(t *testing.T) {
	old := "a\nb\nc\n"
	proj, stats, outcome := computeDiff(newDiff("n.go", old, old))
	if outcome != computeOK {
		t.Fatalf("outcome = %v, want computeOK", outcome)
	}
	if proj == nil || proj.Presentation == nil {
		t.Fatalf("projection = nil, want empty non-nil for no-change case")
	}
	if len(proj.Presentation.Hunks) != 0 {
		t.Fatalf("hunks = %d, want 0 for identical input", len(proj.Presentation.Hunks))
	}
	if stats != (diffStats{}) {
		t.Fatalf("stats = %+v, want zero for identical input", stats)
	}
}

func TestComputeDiffFileCreation(t *testing.T) {
	d := &toolcall.Diff{Path: "new.go", OldText: nil, NewText: "created\n"}
	proj, stats, outcome := computeDiff(d)
	if outcome != computeFileCreation {
		t.Fatalf("outcome = %v, want computeFileCreation", outcome)
	}
	if proj != nil {
		t.Fatalf("projection = %+v, want nil for file creation", proj)
	}
	if stats != (diffStats{}) {
		t.Fatalf("stats = %+v, want zero for file creation", stats)
	}
}

func TestComputeDiffNilDiff(t *testing.T) {
	proj, _, outcome := computeDiff(nil)
	if outcome != computeFileCreation {
		t.Fatalf("outcome = %v, want computeFileCreation for nil diff", outcome)
	}
	if proj != nil {
		t.Fatalf("projection = %+v, want nil", proj)
	}
}

func TestComputeDiffOversizeByte(t *testing.T) {
	old := strings.Repeat("x", diffComputeMaxBytes)
	nw := strings.Repeat("y", 8*1024)
	proj, stats, outcome := computeDiff(newDiff("big.txt", old, nw))
	if outcome != computeSkippedOversize {
		t.Fatalf("outcome = %v, want computeSkippedOversize", outcome)
	}
	if proj != nil || stats != (diffStats{}) {
		t.Fatalf("expected nil projection and zero stats on oversize bypass")
	}
}

func TestComputeDiffOversizeLine(t *testing.T) {
	old := strings.Repeat("a\n", diffComputeMaxLines+1)
	nw := "a\n"
	_, _, outcome := computeDiff(newDiff("many.txt", old, nw))
	if outcome != computeSkippedOversize {
		t.Fatalf("outcome = %v, want computeSkippedOversize for over-line input", outcome)
	}
}

func TestComputeDiffEmptyPathDefaultsToFile(t *testing.T) {
	proj, _, outcome := computeDiff(newDiff("", "a\n", "b\n"))
	if outcome != computeOK {
		t.Fatalf("outcome = %v, want computeOK", outcome)
	}
	pres := proj.Presentation
	if len(pres.Files) != 1 || pres.Files[0].Name != "file" {
		t.Fatalf("files = %+v, want one 'file' fallback", pres.Files)
	}
}

func TestComputeDiffDeterministic(t *testing.T) {
	d := newDiff("det.go", "a\nb\nc\n", "a\nB\nc\n")
	proj1, stats1, out1 := computeDiff(d)
	proj2, stats2, out2 := computeDiff(d)
	if out1 != out2 || stats1 != stats2 {
		t.Fatalf("determinism failed: outcomes/stats differ")
	}
	pres1, pres2 := proj1.Presentation, proj2.Presentation
	if len(pres1.Rows) != len(pres2.Rows) || len(pres1.Hunks) != len(pres2.Hunks) {
		t.Fatalf("determinism failed: presentation shape differs")
	}
	for i := range pres1.Rows {
		if pres1.Rows[i] != pres2.Rows[i] {
			t.Fatalf("determinism failed: row %d differs", i)
		}
	}
	if strings.Join(proj1.PatchLines, "\n") != strings.Join(proj2.PatchLines, "\n") {
		t.Fatalf("determinism failed: patch source differs")
	}
}

func TestComputeDiffRoundTripsThroughDiffview(t *testing.T) {
	cases := []struct {
		name string
		old  string
		nw   string
	}{
		{name: "single-line", old: "a\nb\nc\n", nw: "a\nB\nc\n"},
		{name: "multi-line", old: "a\nb\nc\n", nw: "a\nX\nY\n"},
		{name: "insert", old: "a\nb\n", nw: "a\nb\nc\n"},
		{name: "delete", old: "a\nb\nc\n", nw: "a\nc\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proj, _, outcome := computeDiff(newDiff("rt.go", tc.old, tc.nw))
			if outcome != computeOK {
				t.Fatalf("outcome = %v, want computeOK", outcome)
			}
			if proj == nil || proj.Presentation == nil || proj.Presentation.ParseErr != nil {
				t.Fatalf("projection = %+v", proj)
			}
			pres := proj.Presentation
			if len(pres.Hunks) == 0 {
				t.Fatalf("hunks = 0, want at least 1 for %s", tc.name)
			}
			var seen bool
			for _, row := range pres.Rows {
				if row.Kind == diffview.RowAdd || row.Kind == diffview.RowDelete {
					seen = true
					break
				}
			}
			if !seen {
				t.Fatalf("no add/remove rows found in projection for %s", tc.name)
			}
		})
	}
}

func TestPresentationStatsCountsRowKinds(t *testing.T) {
	proj, _, outcome := computeDiff(newDiff("s.go", "a\nb\nc\n", "a\nX\nc\nY\n"))
	if outcome != computeOK {
		t.Fatalf("outcome = %v, want computeOK", outcome)
	}
	got := presentationStats(proj.Presentation)
	if got.added < 1 || got.removed < 1 {
		t.Fatalf("stats = %+v, want at least +1/-1", got)
	}
	if got != projectionStats(proj) {
		t.Fatalf("projectionStats disagrees with presentationStats")
	}
}

func TestPresentationStatsHandlesNil(t *testing.T) {
	if got := presentationStats(nil); got != (diffStats{}) {
		t.Fatalf("stats(nil) = %+v, want zero value", got)
	}
}

func TestActivityDiffStatsAggregatesAcrossContent(t *testing.T) {
	activity := &toolcall.Activity{
		Kind: "edit",
		Content: []toolcall.Content{
			{Diff: newDiff("a.go", "1\n2\n3\n", "1\nX\n3\n")},
			{Diff: newDiff("b.go", "x\ny\nz\n", "x\ny\nz\nQ\n")},
		},
	}
	stats, ok := activityDiffStats(activity)
	if !ok {
		t.Fatalf("expected activityDiffStats to report a diff exists")
	}
	if stats.added < 2 || stats.removed < 1 {
		t.Fatalf("stats = %+v, want aggregate across both diffs", stats)
	}
}

func TestActivityDiffStatsHandlesNil(t *testing.T) {
	if _, ok := activityDiffStats(nil); ok {
		t.Fatalf("expected no diff for nil activity")
	}
}

func TestActivityDiffStatsIgnoresFileCreation(t *testing.T) {
	activity := &toolcall.Activity{
		Kind: "edit",
		Content: []toolcall.Content{
			{Diff: &toolcall.Diff{Path: "new.go", OldText: nil, NewText: "hello\n"}},
		},
	}
	if _, ok := activityDiffStats(activity); ok {
		t.Fatalf("expected no aggregate stats for file-creation only activity")
	}
}

// TestComputeDiffPatchStringIsDeterministic samples the emitted patch string
// directly so a change to the library's output shape is caught before it
// silently rewires diffview projections.
func TestComputeDiffPatchStringIsDeterministic(t *testing.T) {
	// Reproduce the exact call the production path makes so the test
	// captures library-emit determinism, not our normalization.
	d := newDiff("d.go", "alpha\nbeta\ngamma\n", "alpha\nBETA\ngamma\n")
	pres, _, outcome := computeDiff(d)
	if outcome != computeOK {
		t.Fatalf("outcome = %v", outcome)
	}
	// Reproduce twice — deterministic across calls.
	pres2, _, _ := computeDiff(d)
	if fmt.Sprintf("%+v", pres.Presentation.Hunks) != fmt.Sprintf("%+v", pres2.Presentation.Hunks) {
		t.Fatalf("hunks differ across calls; expected byte-equal output")
	}
}
