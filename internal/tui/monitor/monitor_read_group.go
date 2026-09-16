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
	kind := canonicalActivityKind(activity)
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

// groupReadTranscriptItems wraps every uninterrupted run of eligible reads at
// the same execution coordinate in a read group, including singleton runs,
// so a lone read still opens and collapses the same way a multi-read group
// does. A run of a single failed read is the one exception: it stays a
// standalone exchange so its full error detail keeps rendering inline
// instead of collapsing behind a one-line group row.
func groupReadTranscriptItems(items []transcriptItem, entries []transcript.Entry) []transcriptItem {
	return groupTranscriptItemRuns(items, runGroupSpec{
		eligible: func(item transcriptItem) (string, bool) {
			_, ok := readTargetForItem(item, entries)
			return "read", ok
		},
		skip: func(run []transcriptItem) bool {
			return len(run) == 1 && run[0].displayState == toolDisplayError
		},
		build: func(run []transcriptItem) transcriptItem {
			first := run[0]
			members := append([]transcriptItem(nil), run...)
			return transcriptItem{
				key:          transcriptItemKey{anchor: first.primary.key, kind: transcriptItemReadGroup},
				kind:         transcriptItemReadGroup,
				role:         first.role,
				primary:      first.primary,
				groupMembers: members,
				displayState: aggregateReadGroupState(members),
				coord:        first.coord,
			}
		},
	})
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
