package monitor

import "github.com/ryanflachman-liatrio/jig/internal/toolcall"

type recordedDiffCache struct {
	old, new, path string
	hasOld         bool
	projection     *diffProjection
	stats          diffStats
	outcome        computeOutcome
}

// Retain pure diff computation for loaded immutable evidence. Source window
// navigation must not rerun Myers for the header badge or fallback decision.
func (m *Model) recordedDiff(d *toolcall.Diff) (*diffProjection, diffStats, computeOutcome) {
	if d == nil {
		return nil, diffStats{}, computeFileCreation
	}
	if m.sourceDiffs == nil {
		m.sourceDiffs = make(map[*toolcall.Diff]recordedDiffCache)
	}
	old := ""
	if d.OldText != nil {
		old = *d.OldText
	}
	c, ok := m.sourceDiffs[d]
	if !ok || c.old != old || c.new != d.NewText || c.path != d.Path || c.hasOld != (d.OldText != nil) {
		c = recordedDiffCache{old: old, new: d.NewText, path: d.Path, hasOld: d.OldText != nil}
		c.projection, c.stats, c.outcome = computeDiff(d)
		m.sourceDiffs[d] = c
	}
	return c.projection, c.stats, c.outcome
}

func (m *Model) recordedActivityStats(a *toolcall.Activity) (diffStats, bool) {
	var total diffStats
	if a == nil {
		return total, false
	}
	for _, content := range a.Content {
		_, stats, outcome := m.recordedDiff(content.Diff)
		if outcome == computeOK {
			total.added += stats.added
			total.removed += stats.removed
		}
	}
	return total, total.added > 0 || total.removed > 0
}

func (m *Model) pruneSourceDiffs() {
	loaded := make(map[*toolcall.Diff]bool)
	for _, entry := range m.chatEntries {
		for _, block := range entry.Blocks {
			if a := block.Activity(); a != nil {
				for _, c := range a.Content {
					if c.Diff != nil {
						loaded[c.Diff] = true
					}
				}
			}
		}
	}
	for d := range m.sourceDiffs {
		if !loaded[d] {
			delete(m.sourceDiffs, d)
		}
	}
}
