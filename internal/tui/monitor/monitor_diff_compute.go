package monitor

import (
	"fmt"
	"strings"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"

	"jig/internal/toolcall"
	"jig/internal/tui/diffview"
)

// diffComputeMaxBytes caps the combined size of OldText + NewText handed to
// the Myers producer. Payloads over this bound bypass computation because
// Myers is O(ND) in the input length and edit distance; the transcript
// panel's 100 ms throttled repaint cannot absorb a 1 MiB rewrite. The value
// mirrors the transcript writer's per-block aggregate clamp so a payload
// that made it through the writer untruncated always fits here.
const diffComputeMaxBytes = 128 * 1024

// diffComputeMaxLines caps line count on either side of the diff. Its
// rationale is the same as the byte cap: a pathological line count would
// spike Myers's runtime regardless of aggregate bytes.
const diffComputeMaxLines = 4000

// diffStats counts the visible add and remove rows produced by a computed
// diff. It is small on purpose so the header-badge helper can carry it
// through composeToolHeader without touching the presentation itself.
type diffStats struct {
	added   int
	removed int
}

// computeOutcome discriminates the four paths through computeDiff. The
// caller renders the resulting-source card as a fallback for every outcome
// other than computeOK; each fallback carries a different hint.
type computeOutcome int

const (
	// computeOK — a diff was computed and parsed cleanly. The presentation
	// may still be empty when OldText == NewText; the caller treats an
	// empty presentation as "no visible change" without emitting a diff.
	computeOK computeOutcome = iota
	// computeFileCreation — Diff.OldText is nil, so there is no prior
	// version to compare against. Slice 07 preserves the resulting-source
	// card unchanged for this case (the case writeNewCodeCards's original
	// comment was genuinely serving).
	computeFileCreation
	// computeSkippedOversize — Diff exceeded diffComputeMaxBytes or
	// diffComputeMaxLines. The caller renders the resulting-source card
	// and prepends shared.DiffUnavailableHint().
	computeSkippedOversize
	// computeFailed — Myers emitted a patch that diffview.Parse could not
	// project. The caller renders the resulting-source card and prepends
	// shared.DiffUnavailableHint().
	computeFailed
)

// diffProjection carries the projected presentation alongside the raw
// patch line source so the renderer can look up each row's textual
// content by its PatchLine index. diffview.Row does not itself store
// content (that would require a wire change); the projection is
// preserved here as a lightweight scratch structure keyed on the
// presentation for the duration of one render pass.
type diffProjection struct {
	Presentation *diffview.Presentation
	// PatchLines holds the raw patch text split on '\n'. Row i's
	// content is patchLines[Row.PatchLine-1][1:] — the leading
	// marker character is stripped by the renderer.
	PatchLines []string
}

// computeDiff turns a Diff payload into a projected diffProjection plus
// its aggregate add/remove counts. The function is pure with respect to
// (OldText, NewText, Path) and the package-private caps: the same input
// deterministically produces the same output on every call, which lets
// the Monitor's per-item render cache hit on repeat frames rather than
// recompute.
func computeDiff(d *toolcall.Diff) (*diffProjection, diffStats, computeOutcome) {
	if d == nil || d.OldText == nil {
		return nil, diffStats{}, computeFileCreation
	}
	oldText, newText := *d.OldText, d.NewText
	if len(oldText)+len(newText) > diffComputeMaxBytes {
		return nil, diffStats{}, computeSkippedOversize
	}
	if lineCount(oldText) > diffComputeMaxLines || lineCount(newText) > diffComputeMaxLines {
		return nil, diffStats{}, computeSkippedOversize
	}

	// Neutral filename mirrors diffview.FileName's fallback so a
	// path-less Diff still surfaces as a coherent single-file diff.
	path := d.Path
	if path == "" {
		path = "file"
	}

	// Use a synthetic absolute URI so span.URIFromPath does not shell
	// out to os.Getwd during myers.ComputeEdits — computation stays
	// deterministic across processes and cwds.
	uri := span.URIFromPath("/" + strings.TrimPrefix(path, "/"))
	edits := myers.ComputeEdits(uri, oldText, newText)
	if len(edits) == 0 {
		// Identical inputs: no visible change. Return an empty but
		// non-nil projection so the caller can distinguish "computed
		// nothing" from "did not compute".
		return &diffProjection{Presentation: &diffview.Presentation{}}, diffStats{}, computeOK
	}
	unified := gotextdiff.ToUnified("a/"+path, "b/"+path, oldText, edits)
	patch := fmt.Sprint(unified)
	pres := diffview.Parse(patch)
	if pres.ParseErr != nil {
		return nil, diffStats{}, computeFailed
	}
	proj := &diffProjection{Presentation: pres, PatchLines: strings.Split(patch, "\n")}
	return proj, presentationStats(pres), computeOK
}

// lineCount returns the number of newline-separated lines in s without
// materializing a slice. An empty string counts as one line so a
// single-line file with no trailing newline is not falsely flagged as
// zero-line and mis-routed through the cap.
func lineCount(s string) int {
	if s == "" {
		return 1
	}
	n := 1
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			n++
		}
	}
	return n
}

// presentationStats counts add and remove rows over the projected
// presentation. Context and hunk-header rows are ignored.
func presentationStats(pres *diffview.Presentation) diffStats {
	if pres == nil {
		return diffStats{}
	}
	var s diffStats
	for _, row := range pres.Rows {
		switch row.Kind {
		case diffview.RowAdd:
			s.added++
		case diffview.RowDelete:
			s.removed++
		}
	}
	return s
}

// projectionStats is presentationStats over a projection.
func projectionStats(p *diffProjection) diffStats {
	if p == nil {
		return diffStats{}
	}
	return presentationStats(p.Presentation)
}

// activityDiffStats aggregates diff counts across every Content entry on
// an activity that carries a computable Diff. It is used by the header
// path to decide whether to emit the +N/-M badge; the projection itself
// is discarded here so composeToolHeader stays free of rendering state.
func activityDiffStats(a *toolcall.Activity) (diffStats, bool) {
	if a == nil {
		return diffStats{}, false
	}
	var total diffStats
	var any bool
	for _, content := range a.Content {
		_, stats, outcome := computeDiff(content.Diff)
		if outcome != computeOK {
			continue
		}
		total.added += stats.added
		total.removed += stats.removed
		if stats.added > 0 || stats.removed > 0 {
			any = true
		}
	}
	return total, any
}
