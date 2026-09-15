package monitor

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"jig/internal/toolcall"
	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

type readTarget struct {
	path string
}

type readGroupRow struct {
	path      string
	selectors []string
	state     toolDisplayState
}

// readTargetForActivity accepts only backend-normalized read activities with a
// non-empty local path. Kind is authoritative when supplied; title fallback is
// retained for older normalized transcripts that predate Activity.Kind.
func readTargetForActivity(activity *toolcall.Activity) (readTarget, bool) {
	if activity == nil {
		return readTarget{}, false
	}
	kind := strings.ToLower(strings.TrimSpace(activity.Kind))
	if kind == "" {
		kind = canonicalToolName(activity.Title)
	}
	if kind != "read" {
		return readTarget{}, false
	}
	args := decodeToolArgs(activity.Input)
	target := sanitizeToolSummary(stringArg(args, "file_path", "path"))
	if target == "" || !localReadPath(target) {
		return readTarget{}, false
	}
	return readTarget{path: target}, true
}

func localReadPath(path string) bool {
	if strings.Contains(path, "://") || strings.HasPrefix(strings.ToLower(path), "data:") {
		return false
	}
	parsed, err := url.Parse(path)
	if err != nil {
		return false
	}
	// filepath.VolumeName preserves Windows drive paths while still rejecting
	// URI schemes such as ssh:, https:, and file:.
	return parsed.Scheme == "" || filepath.VolumeName(path) != ""
}

func readTargetForItem(item transcriptItem, entries []transcript.Entry) (readTarget, bool) {
	if item.kind != transcriptItemToolExchange || item.toolUse == nil {
		return readTarget{}, false
	}
	ref := *item.toolUse
	if ref.entryIdx < 0 || ref.entryIdx >= len(entries) || ref.blockIdx < 0 || ref.blockIdx >= len(entries[ref.entryIdx].Blocks) {
		return readTarget{}, false
	}
	return readTargetForActivity(entries[ref.entryIdx].Blocks[ref.blockIdx].Activity())
}

func sameExecutionCoordinate(a, b toolCorrelationKey) bool {
	return a.generation == b.generation && a.iteration == b.iteration && a.attempt == b.attempt
}

// groupReadTranscriptItems is a pure, page-local post-correlation pass. It
// groups only uninterrupted runs of at least two eligible reads at the same
// execution coordinate; singletons retain their original identity exactly.
func groupReadTranscriptItems(items []transcriptItem, entries []transcript.Entry) []transcriptItem {
	grouped := make([]transcriptItem, 0, len(items))
	run := make([]transcriptItem, 0, 4)
	flush := func() {
		switch len(run) {
		case 0:
		case 1:
			grouped = append(grouped, run[0])
		default:
			first := run[0]
			members := append([]transcriptItem(nil), run...)
			grouped = append(grouped, transcriptItem{
				key:          transcriptItemKey{anchor: first.primary.key, kind: transcriptItemReadGroup},
				kind:         transcriptItemReadGroup,
				role:         first.role,
				primary:      first.primary,
				groupMembers: members,
				displayState: aggregateReadGroupState(members),
				coord:        first.coord,
			})
		}
		run = run[:0]
	}

	for _, item := range items {
		if _, eligible := readTargetForItem(item, entries); !eligible {
			flush()
			grouped = append(grouped, item)
			continue
		}
		if len(run) > 0 && !sameExecutionCoordinate(run[0].coord, item.coord) {
			flush()
		}
		run = append(run, item)
	}
	flush()
	return grouped
}

func aggregateReadGroupState(members []transcriptItem) toolDisplayState {
	state := toolDisplaySuccess
	for _, member := range members {
		switch member.displayState {
		case toolDisplayError:
			return toolDisplayError
		case toolDisplayRunning:
			if state != toolDisplayError {
				state = toolDisplayRunning
			}
		case toolDisplayUnknownUse, toolDisplayUnknownResult:
			if state == toolDisplaySuccess {
				state = member.displayState
			}
		}
	}
	return state
}

func positiveIntArg(args map[string]json.RawMessage, key string) (int, bool) {
	raw, ok := args[key]
	if !ok {
		return 0, false
	}
	var value int
	if json.Unmarshal(raw, &value) != nil || value <= 0 {
		return 0, false
	}
	return value, true
}

func readSelector(activity *toolcall.Activity) string {
	if activity == nil {
		return ""
	}
	args := decodeToolArgs(activity.Input)
	if offset, ok := positiveIntArg(args, "offset"); ok {
		if limit, hasLimit := positiveIntArg(args, "limit"); hasLimit {
			return strconv.Itoa(offset) + "-" + strconv.Itoa(offset+limit-1)
		}
		return strconv.Itoa(offset)
	}
	for _, location := range activity.Locations {
		if location.Line != nil && *location.Line > 0 {
			return strconv.Itoa(*location.Line)
		}
	}
	return ""
}

func readGroupRows(group transcriptItem, entries []transcript.Entry) []readGroupRow {
	rows := make([]readGroupRow, 0, len(group.groupMembers))
	indexes := make(map[string]int, len(group.groupMembers))
	selectorSeen := make(map[string]map[string]struct{}, len(group.groupMembers))
	for _, member := range group.groupMembers {
		target, ok := readTargetForItem(member, entries)
		if !ok {
			continue
		}
		idx, found := indexes[target.path]
		if !found {
			idx = len(rows)
			indexes[target.path] = idx
			selectorSeen[target.path] = make(map[string]struct{})
			rows = append(rows, readGroupRow{path: target.path, state: member.displayState})
		} else {
			rows[idx].state = aggregateReadGroupState([]transcriptItem{{displayState: rows[idx].state}, member})
		}
		activity := entries[member.toolUse.entryIdx].Blocks[member.toolUse.blockIdx].Activity()
		selector := readSelector(activity)
		if selector == "" {
			continue
		}
		if _, duplicate := selectorSeen[target.path][selector]; duplicate {
			continue
		}
		selectorSeen[target.path][selector] = struct{}{}
		rows[idx].selectors = append(rows[idx].selectors, selector)
	}
	return rows
}

func compactReadSelectors(selectors []string) []string {
	if len(selectors) <= 3 {
		return append([]string(nil), selectors...)
	}
	return []string{selectors[0], selectors[1], shared.EllipsisGlyph, selectors[len(selectors)-1]}
}
