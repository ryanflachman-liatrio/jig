package monitor

import (
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

var nextLocationInstance atomic.Uint64

// OpenLocationOwner identifies a selection without retaining a mutable model.
// A new Monitor, even for the same run, invalidates old asynchronous requests.
func (m Model) OpenLocationOwner() shared.OpenLocationOwner {
	owner := shared.OpenLocationOwner{Instance: m.locationInstance, RunID: m.RunID, StepID: m.chatStep}
	if item, ok := m.selectedTranscriptItem(); ok && m.selKind != "file" {
		owner.Generation, owner.Iteration, owner.Attempt = item.coord.generation, item.coord.iteration, item.coord.attempt
		owner.Seq, owner.Block = item.key.anchor.seq, item.key.anchor.block
	}
	return owner
}

// selectedLocationTargets reads only normalized locations and edit paths from
// the selected exchange. Argument/origin and group selection belong to the
// subsequent grouped-location slice.
func (m Model) selectedLocationTargets() []shared.OpenLocationTarget {
	item, ok := m.selectedTranscriptItem()
	if !ok || (item.kind != transcriptItemToolExchange && item.kind != transcriptItemToolResult) {
		return nil
	}
	blocks := collectItemBlocks(item, m.chatEntries)
	var targets []shared.OpenLocationTarget
	seen := make(map[shared.OpenLocationTarget]bool)
	paths := make(map[string]bool)
	for _, block := range blocks {
		if activity := block.Activity(); activity != nil {
			for _, location := range activity.Locations {
				target := shared.OpenLocationTarget{Path: location.Path}
				if location.Line != nil {
					target.Line = max(0, *location.Line)
				}
				if location.Column != nil {
					target.Column = max(0, *location.Column)
				}
				if !seen[target] {
					targets = append(targets, target)
					seen[target] = true
					paths[target.Path] = true
				}
			}
		}
	}
	for _, block := range blocks {
		if activity := block.Activity(); activity != nil {
			for _, content := range activity.Content {
				if content.Diff != nil && !paths[content.Diff.Path] {
					targets = append(targets, shared.OpenLocationTarget{Path: content.Diff.Path})
					paths[content.Diff.Path] = true
				}
			}
		}
	}
	return targets
}

func (m Model) openLocationCmd() tea.Cmd {
	req := shared.OpenLocationRequest{Owner: m.OpenLocationOwner()}
	targets := m.selectedLocationTargets()
	switch len(targets) {
	case 0:
		req.Unavailable = "no recorded file target on this selection"
	case 1:
		req.Target = targets[0]
	default:
		req.Unavailable = "multiple recorded targets; single-target opening only"
	}
	return func() tea.Msg { return req }
}
