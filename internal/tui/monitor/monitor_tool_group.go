package monitor

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ryanflachman-liatrio/jig/internal/toolcall"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

// toolGroupPolicy describes how one canonical tool kind groups. Reads are the
// one always-on policy: they group whether or not compact tool groups are
// enabled, and they admit running and failed members so an in-flight or broken
// read stays inside its run. Every other policy groups only settled successes,
// and only while compact tool groups are on.
type toolGroupPolicy struct {
	title  string
	kind   string
	always bool
}

var readGroupPolicy = toolGroupPolicy{title: "Read", kind: "read", always: true}

// quietRunKind is the run key shared by settled, successful exploration calls
// while compact tool groups are on, so an uninterrupted read/search/fetch burst
// folds into one group. A run whose members all share one kind keeps that
// kind's policy; a mixed run renders under exploreGroupPolicy.
const quietRunKind = "quiet"

var exploreGroupPolicy = toolGroupPolicy{title: "Explore", kind: quietRunKind}

var quietToolKinds = map[string]bool{
	"read": true, "glob": true, "grep": true, "websearch": true, "webfetch": true,
}

// quietMember reports whether item may join a mixed quiet run. A running or
// failed read is not quiet; it keeps the read policy and so ends the run.
func quietMember(item transcriptItem, policy toolGroupPolicy) bool {
	return quietToolKinds[policy.kind] && item.displayState == toolDisplaySuccess
}

var compactToolPolicies = map[string]toolGroupPolicy{
	"edit":         {title: "Edit", kind: "edit"},
	"write":        {title: "Write", kind: "write"},
	"notebookedit": {title: "Edit notebook", kind: "notebookedit"},
	"glob":         {title: "Find", kind: "glob"},
	"grep":         {title: "Search", kind: "grep"},
	"bash":         {title: "Run", kind: "bash"},
	"websearch":    {title: "Search web", kind: "websearch"},
	"webfetch":     {title: "Fetch", kind: "webfetch"},
	"task":         {title: "Agent", kind: "task"},
	"skill":        {title: "Use skill", kind: "skill"},
	"todowrite":    {title: "Update", kind: "todowrite"},
}

// compactToolRow is one collapsed-group preview row. prefix and suffix are
// pre-styled decorations around the target-styled text (a read row's state
// glyph and line selectors); other policies leave them empty.
type compactToolRow struct {
	prefix string
	text   string
	suffix string
}

func toolGroupCandidate(item transcriptItem, entries []transcript.Entry) (toolGroupPolicy, string, bool) {
	if target, ok := readTargetForItem(item, entries); ok {
		return readGroupPolicy, target.path, true
	}
	if item.kind != transcriptItemToolExchange || item.toolUse == nil || item.toolResult == nil || item.displayState != toolDisplaySuccess {
		return toolGroupPolicy{}, "", false
	}
	activity := compactToolActivity(item, entries)
	if activity == nil {
		return toolGroupPolicy{}, "", false
	}
	policy, ok := compactToolPolicies[canonicalActivityKind(activity)]
	if !ok {
		return toolGroupPolicy{}, "", false
	}
	detail := sanitizeToolSummary(summarizeActivity(activity, 0).detail)
	if detail == "" {
		return toolGroupPolicy{}, "", false
	}
	return policy, detail, true
}

func validBlockRef(ref transcriptBlockRef, entries []transcript.Entry) bool {
	return ref.entryIdx >= 0 && ref.entryIdx < len(entries) && ref.blockIdx >= 0 && ref.blockIdx < len(entries[ref.entryIdx].Blocks)
}

// compactToolActivity merges an exchange's result activity over its use so
// detail rendering sees the result's output alongside the use's title, kind,
// ID, and input.
func compactToolActivity(item transcriptItem, entries []transcript.Entry) *toolcall.Activity {
	if item.toolUse == nil || !validBlockRef(*item.toolUse, entries) {
		return nil
	}
	use := entries[item.toolUse.entryIdx].Blocks[item.toolUse.blockIdx].Activity()
	if use == nil {
		return nil
	}
	if item.toolResult == nil || !validBlockRef(*item.toolResult, entries) {
		return use
	}
	result := entries[item.toolResult.entryIdx].Blocks[item.toolResult.blockIdx].Activity()
	if result == nil {
		return use
	}
	activity := result.Clone()
	if activity.Title == "" {
		activity.Title = use.Title
	}
	if activity.Kind == "" {
		activity.Kind = use.Kind
	}
	if activity.ID == "" {
		activity.ID = use.ID
	}
	if len(activity.Input) == 0 {
		activity.Input = append(activity.Input[:0], use.Input...)
	}
	return activity
}

// groupToolTranscriptItems wraps every uninterrupted run of same-policy calls
// at one execution coordinate in a tool group, including singleton runs, so a
// lone call opens and collapses the same way a multi-call group does. Only
// always-on policies (reads) group when compact is false; when compact is on,
// quiet exploration calls of different kinds share one run. A run of a single
// failed call stays a standalone exchange so its full error detail keeps
// rendering inline instead of collapsing behind a one-line group row.
func groupToolTranscriptItems(items []transcriptItem, entries []transcript.Entry, compact bool) []transcriptItem {
	return groupTranscriptItemRuns(items, runGroupSpec{
		eligible: func(item transcriptItem) (string, bool) {
			policy, _, ok := toolGroupCandidate(item, entries)
			if !ok {
				return "", false
			}
			if compact && quietMember(item, policy) {
				return quietRunKind, true
			}
			if !(compact || policy.always) {
				return "", false
			}
			return policy.kind, true
		},
		skip: func(run []transcriptItem) bool {
			return len(run) == 1 && run[0].displayState == toolDisplayError
		},
		build: func(run []transcriptItem) transcriptItem {
			first := run[0]
			members := append([]transcriptItem(nil), run...)
			return transcriptItem{
				key:          transcriptItemKey{anchor: first.primary.key, kind: transcriptItemToolGroup},
				kind:         transcriptItemToolGroup,
				role:         first.role,
				primary:      first.primary,
				groupMembers: members,
				displayState: aggregateToolGroupState(members),
				coord:        first.coord,
			}
		},
	})
}

func aggregateToolGroupState(members []transcriptItem) toolDisplayState {
	state := toolDisplaySuccess
	for _, member := range members {
		switch member.displayState {
		case toolDisplayError:
			return toolDisplayError
		case toolDisplayRunning:
			state = toolDisplayRunning
		case toolDisplayUnknownUse, toolDisplayUnknownResult:
			if state == toolDisplaySuccess {
				state = member.displayState
			}
		}
	}
	return state
}

func compactToolGroupPolicy(item transcriptItem, entries []transcript.Entry) (toolGroupPolicy, bool) {
	if item.kind != transcriptItemToolGroup || len(item.groupMembers) == 0 {
		return toolGroupPolicy{}, false
	}
	first, _, ok := toolGroupCandidate(item.groupMembers[0], entries)
	if !ok {
		return toolGroupPolicy{}, false
	}
	for _, member := range item.groupMembers[1:] {
		if policy, _, ok := toolGroupCandidate(member, entries); !ok || policy.kind != first.kind {
			return exploreGroupPolicy, true
		}
	}
	return first, true
}

// compactToolGroupRows renders a collapsed group's preview. Read groups merge
// members by path and list every target; other groups show at most the first
// three calls, an omitted-count row, and the final call. Mixed Explore rows
// carry each member's kind title as a muted label.
func compactToolGroupRows(item transcriptItem, entries []transcript.Entry) []compactToolRow {
	policy, ok := compactToolGroupPolicy(item, entries)
	if ok && policy.kind == readGroupPolicy.kind {
		return readCompactRows(item, entries)
	}
	mixed := ok && policy.kind == quietRunKind
	rows := make([]compactToolRow, 0, min(len(item.groupMembers), 5))
	appendMember := func(member transcriptItem) {
		memberPolicy, detail, ok := toolGroupCandidate(member, entries)
		if !ok {
			return
		}
		row := compactToolRow{text: previewText(detail)}
		if mixed {
			row.prefix = shared.Theme.Chat.ToolMeta.Render(memberPolicy.title) + " "
		}
		rows = append(rows, row)
	}
	if len(item.groupMembers) < 5 {
		for _, member := range item.groupMembers {
			appendMember(member)
		}
		return rows
	}
	for _, member := range item.groupMembers[:3] {
		appendMember(member)
	}
	rows = append(rows, compactToolRow{
		text: shared.EllipsisGlyph + " " + pluralCount(len(item.groupMembers)-4, "more call", "more calls"),
	})
	appendMember(item.groupMembers[len(item.groupMembers)-1])
	return rows
}

func readCompactRows(item transcriptItem, entries []transcript.Entry) []compactToolRow {
	targets := readGroupRows(item, entries)
	rows := make([]compactToolRow, 0, len(targets))
	for _, target := range targets {
		var row compactToolRow
		if target.state != toolDisplaySuccess {
			glyph, style := shared.ToolStatusIcon(target.state, readGroupPolicy.kind)
			row.prefix = style.Render(glyph) + " "
		}
		// target.path stays raw: it is the read-tree merge key, and masking it
		// could merge two distinct files. Only the displayed text is masked.
		row.text = previewText(shortFile(target.path))
		if selectors := compactReadSelectors(target.selectors); len(selectors) > 0 {
			row.suffix = shared.Theme.Chat.ToolMeta.Render(":" + strings.Join(selectors, ", "))
		}
		rows = append(rows, row)
	}
	return rows
}

func pluralCount(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(n) + " " + plural
}

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
	if activity == nil || canonicalActivityKind(activity) != "read" {
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
	if item.kind != transcriptItemToolExchange || item.toolUse == nil || !validBlockRef(*item.toolUse, entries) {
		return readTarget{}, false
	}
	return readTargetForActivity(entries[item.toolUse.entryIdx].Blocks[item.toolUse.blockIdx].Activity())
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
			rows[idx].state = aggregateToolGroupState([]transcriptItem{{displayState: rows[idx].state}, member})
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
