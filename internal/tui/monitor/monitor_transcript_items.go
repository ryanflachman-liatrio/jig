package monitor

import "jig/internal/transcript"

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
					key:     transcriptItemKey{anchor: ref.key, kind: kind},
					kind:    kind,
					role:    entry.Role,
					primary: ref,
					coord:   toolCorrelationKey{generation: entry.Generation, iteration: entry.Iteration, attempt: entry.Attempt},
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
