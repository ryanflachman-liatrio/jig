package monitor

import (
	"time"

	"jig/internal/transcript"
)

// buildTranscriptItems turns only the currently loaded page into immutable
// conversation units. It never opens a reader or scans outside entries: a page
// edge is therefore represented honestly as a use-only or result-only item.
func buildTranscriptItems(entries []transcript.Entry, stepRunning bool) []transcriptItem {
	items := make([]transcriptItem, 0, len(entries))
	pendingUses := make(map[toolCorrelationKey][]int)
	pendingResults := make(map[toolCorrelationKey][]int)

	for entryIdx, entry := range entries {
		for blockIdx, block := range entry.Blocks {
			ref := transcriptBlockRef{
				key:      blockKey{seq: entry.Seq, block: blockIdx},
				entryIdx: entryIdx,
				blockIdx: blockIdx,
			}
			kind := itemKindForBlock(entry.Role, block)
			if block.Type != transcript.BlockToolUse && block.Type != transcript.BlockToolResult {
				items = append(items, transcriptItem{
					key:       transcriptItemKey{anchor: ref.key, kind: kind},
					kind:      kind,
					role:      entry.Role,
					primary:   ref,
					coord:     toolCorrelationKey{generation: entry.Generation, iteration: entry.Iteration, attempt: entry.Attempt},
					oversized: itemOversized(kind, entry.Role, block.Text),
				})
				continue
			}

			toolID := ""
			if activity := block.Activity(); activity != nil {
				toolID = activity.ID
			}
			coord := toolCorrelationKey{generation: entry.Generation, iteration: entry.Iteration, attempt: entry.Attempt, toolUseID: toolID}
			if coord.toolUseID == "" {
				items = append(items, standaloneToolItem(ref, entry.Role, coord, block, stepRunning))
				continue
			}

			if block.Type == transcript.BlockToolUse {
				if resultIndexes := pendingResults[coord]; len(resultIndexes) > 0 {
					resultIndex := resultIndexes[0]
					pendingResults[coord] = resultIndexes[1:]
					result := items[resultIndex]
					matched := transcriptItem{
						key:          transcriptItemKey{anchor: ref.key, kind: transcriptItemToolExchange},
						kind:         transcriptItemToolExchange,
						role:         entry.Role,
						primary:      ref,
						toolUse:      &ref,
						toolResult:   result.toolResult,
						displayState: toolStateForResult(entries, result.toolResult),
						coord:        coord,
					}
					// Move the completed exchange to its use position, preserving the
					// visual ordering contract even when an ACP result arrived first.
					items = append(items[:resultIndex], items[resultIndex+1:]...)
					for key, indexes := range pendingUses {
						for i, index := range indexes {
							if index > resultIndex {
								pendingUses[key][i] = index - 1
							}
						}
					}
					for key, indexes := range pendingResults {
						for i, index := range indexes {
							if index > resultIndex {
								pendingResults[key][i] = index - 1
							}
						}
					}
					items = append(items, matched)
					continue
				}
				item := standaloneToolItem(ref, entry.Role, coord, block, stepRunning)
				items = append(items, item)
				pendingUses[coord] = append(pendingUses[coord], len(items)-1)
				continue
			}

			if useIndexes := pendingUses[coord]; len(useIndexes) > 0 {
				useIndex := useIndexes[0]
				pendingUses[coord] = useIndexes[1:]
				use := items[useIndex]
				items[useIndex] = transcriptItem{
					key:          transcriptItemKey{anchor: use.primary.key, kind: transcriptItemToolExchange},
					kind:         transcriptItemToolExchange,
					role:         use.role,
					primary:      use.primary,
					toolUse:      use.toolUse,
					toolResult:   &ref,
					displayState: toolStateForBlock(block),
					coord:        coord,
				}
				continue
			}

			item := standaloneToolItem(ref, entry.Role, coord, block, stepRunning)
			items = append(items, item)
			pendingResults[coord] = append(pendingResults[coord], len(items)-1)
		}
	}
	// The active running thinking item is the trailing item in the built
	// sequence when the step is running (mirrors standaloneToolItem's
	// stepRunning check for an orphan tool item, which has no item after it
	// either). This drives the FR-10.4 pulse; every other thinking item keeps
	// the plain label (FR-10.7).
	if stepRunning && len(items) > 0 {
		if last := len(items) - 1; items[last].kind == transcriptItemThinking {
			items[last].running = true
		}
	}
	return items
}

// transcriptBoundary is a presentation-neutral execution transition. Renderers
// decide how to style it; normalization only reports changes visible in the
// loaded and filtered item sequence.
type transcriptBoundary struct {
	before int
	coord  toolCorrelationKey
}

func visibleExecutionBoundaries(items []transcriptItem) []transcriptBoundary {
	var boundaries []transcriptBoundary
	var previous toolCorrelationKey
	for i, item := range items {
		coord := item.coord
		if i == 0 {
			if coord.generation != 0 || coord.iteration != 0 || coord.attempt != 0 {
				boundaries = append(boundaries, transcriptBoundary{before: i, coord: coord})
			}
			previous = coord
			continue
		}
		if previous.generation != coord.generation || previous.iteration != coord.iteration || previous.attempt != coord.attempt {
			boundaries = append(boundaries, transcriptBoundary{before: i, coord: coord})
		}
		previous = coord
	}
	return boundaries
}

func itemMembers(item transcriptItem) []transcriptBlockRef {
	if item.kind == transcriptItemReadGroup || item.kind == transcriptItemToolGroup {
		members := make([]transcriptBlockRef, 0, len(item.groupMembers)*2)
		for _, member := range item.groupMembers {
			members = append(members, itemMembers(member)...)
		}
		return members
	}
	if item.toolUse == nil && item.toolResult == nil {
		return []transcriptBlockRef{item.primary}
	}
	members := make([]transcriptBlockRef, 0, 2)
	if item.toolUse != nil {
		members = append(members, *item.toolUse)
	}
	if item.toolResult != nil {
		members = append(members, *item.toolResult)
	}
	return members
}

// itemSpacingBefore describes only structural whitespace between neighboring
// visible items. It deliberately has no theme dependency so filtered views and
// the default view retain the same conversation rhythm.
//
// Slice-04 discipline: the caller (itemTranscriptBody) applies this rule
// only between two items that both contributed content after edge trimming,
// so a filtered or zero-height item does not leave a ghost gap behind. The
// two-line execution-coordinate gap remains stronger than the ordinary
// one-line gap because slice 12's boundary banner will render inside it.
func itemSpacingBefore(previous, current transcriptItem) int {
	if previous.coord.generation != current.coord.generation ||
		previous.coord.iteration != current.coord.iteration ||
		previous.coord.attempt != current.coord.attempt {
		return 2
	}
	if previous.kind == transcriptItemText && current.kind == transcriptItemText && previous.role == current.role {
		return 0
	}
	return 1
}

// isResponseStart reports whether current begins a new assistant response
// within the same execution coordinate: an assistant text item arriving
// after something other than a continuing assistant text item (thinking, a
// tool call/group, or a different role). A coordinate change is handled by
// the boundary banner instead, so callers only consult this once
// itemSpacingBefore has already ruled that out. It never fires on the first
// visible item (there is no prior response to separate from).
func isResponseStart(previous, current transcriptItem) bool {
	if current.kind != transcriptItemText || current.role != transcript.RoleAssistant {
		return false
	}
	return !(previous.kind == transcriptItemText && previous.role == current.role)
}

// itemOversized reports whether a text or thinking item exceeds
// chatTextCollapseBytes and so collapses to the shared summary row (FR-09.4,
// FR-10.3). User-role text collapses only when authored by the operator;
// thinking blocks collapse regardless of role, since reasoning has no
// operator/assistant distinction.
func itemOversized(kind transcriptItemKind, role transcript.Role, text string) bool {
	switch kind {
	case transcriptItemText:
		return role == transcript.RoleUser && len(text) > chatTextCollapseBytes
	case transcriptItemThinking:
		return len(text) > chatTextCollapseBytes
	default:
		return false
	}
}

func itemKindForBlock(role transcript.Role, block transcript.Block) transcriptItemKind {
	switch block.Type {
	case transcript.BlockText:
		if role == transcript.RoleSystem || role == transcript.RoleResult {
			return transcriptItemSystem
		}
		return transcriptItemText
	case transcript.BlockThinking:
		return transcriptItemThinking
	default:
		return transcriptItemUnsupported
	}
}

func standaloneToolItem(ref transcriptBlockRef, role transcript.Role, coord toolCorrelationKey, block transcript.Block, stepRunning bool) transcriptItem {
	if block.Type == transcript.BlockToolUse {
		state := toolDisplayUnknownUse
		if stepRunning {
			state = toolDisplayRunning
		}
		return transcriptItem{key: transcriptItemKey{anchor: ref.key, kind: transcriptItemToolExchange}, kind: transcriptItemToolExchange, role: role, primary: ref, toolUse: &ref, displayState: state, coord: coord}
	}
	state := toolDisplayUnknownResult
	if activity := block.Activity(); (activity != nil && activity.Status == "failed") || block.IsError {
		state = toolDisplayError
	}
	return transcriptItem{key: transcriptItemKey{anchor: ref.key, kind: transcriptItemToolResult}, kind: transcriptItemToolResult, role: role, primary: ref, toolResult: &ref, displayState: state, coord: coord}
}

func toolStateForResult(entries []transcript.Entry, ref *transcriptBlockRef) toolDisplayState {
	if ref == nil || ref.entryIdx >= len(entries) || ref.blockIdx >= len(entries[ref.entryIdx].Blocks) {
		return toolDisplayUnknownResult
	}
	return toolStateForBlock(entries[ref.entryIdx].Blocks[ref.blockIdx])
}

func toolStateForBlock(block transcript.Block) toolDisplayState {
	if activity := block.Activity(); (activity != nil && activity.Status == "failed") || block.IsError {
		return toolDisplayError
	}
	return toolDisplaySuccess
}

// turnTimestamps folds the first and last parseable RFC3339 timestamps
// among entries at the given execution coordinate. It ignores the
// coord.toolUseID field so tool-use and result entries at the same
// generation/iteration/attempt fold into one turn, matching how
// itemSpacingBefore identifies a coord change.
//
// Returns (zero, zero, false) when the coordinate has no entries in
// the loaded page or when every candidate entry's Ts fails RFC3339
// parsing. When only one endpoint parses, both start and end return
// that same timestamp so the caller can still render a timestamp while
// dropping the elapsed field.
//
// Slice 11 (per-turn metadata row) consumes this: the interior boundary
// row derives its `time` and `Δ` fields from a single call keyed on the
// previous item's coord, and the step-end row derives them from a call
// keyed on the last visible item's coord.
func turnTimestamps(entries []transcript.Entry, coord toolCorrelationKey) (start, end time.Time, ok bool) {
	for _, entry := range entries {
		if entry.Generation != coord.generation || entry.Iteration != coord.iteration || entry.Attempt != coord.attempt {
			continue
		}
		t, err := time.Parse(time.RFC3339, entry.Ts)
		if err != nil {
			continue
		}
		if !ok {
			start, end, ok = t, t, true
			continue
		}
		if t.Before(start) {
			start = t
		}
		if t.After(end) {
			end = t
		}
	}
	return start, end, ok
}
