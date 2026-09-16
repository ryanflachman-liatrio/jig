package monitor

import (
	"strconv"

	"jig/internal/toolcall"
	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

type toolGroupPolicy struct {
	title string
	kind  string
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

type compactToolRow struct {
	text string
}

func compactToolGroupCandidate(item transcriptItem, entries []transcript.Entry) (toolGroupPolicy, string, bool) {
	if item.kind != transcriptItemToolExchange || item.toolUse == nil || item.toolResult == nil || item.displayState != toolDisplaySuccess {
		return toolGroupPolicy{}, "", false
	}
	ref := *item.toolUse
	if ref.entryIdx < 0 || ref.entryIdx >= len(entries) || ref.blockIdx < 0 || ref.blockIdx >= len(entries[ref.entryIdx].Blocks) {
		return toolGroupPolicy{}, "", false
	}
	activity := compactToolActivity(item, entries)
	if activity == nil {
		return toolGroupPolicy{}, "", false
	}
	kind := canonicalActivityKind(activity)
	policy, ok := compactToolPolicies[kind]
	if !ok {
		return toolGroupPolicy{}, "", false
	}
	summary := summarizeActivity(activity, 0)
	detail := sanitizeToolSummary(summary.detail)
	if detail == "" {
		return toolGroupPolicy{}, "", false
	}
	return policy, detail, true
}

func compactToolActivity(item transcriptItem, entries []transcript.Entry) *toolcall.Activity {
	if item.toolUse == nil {
		return nil
	}
	useRef := *item.toolUse
	if useRef.entryIdx < 0 || useRef.entryIdx >= len(entries) || useRef.blockIdx < 0 || useRef.blockIdx >= len(entries[useRef.entryIdx].Blocks) {
		return nil
	}
	use := entries[useRef.entryIdx].Blocks[useRef.blockIdx].Activity()
	if use == nil {
		return nil
	}
	if item.toolResult == nil {
		return use
	}
	resultRef := *item.toolResult
	if resultRef.entryIdx < 0 || resultRef.entryIdx >= len(entries) || resultRef.blockIdx < 0 || resultRef.blockIdx >= len(entries[resultRef.entryIdx].Blocks) {
		return use
	}
	result := entries[resultRef.entryIdx].Blocks[resultRef.blockIdx].Activity()
	if result == nil {
		return use
	}
	activity := result.Clone()
	if use != nil {
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
	}
	return activity
}

func groupCompactToolTranscriptItems(items []transcriptItem, entries []transcript.Entry) []transcriptItem {
	return groupTranscriptItemRuns(items, runGroupSpec{
		eligible: func(item transcriptItem) (string, bool) {
			policy, _, ok := compactToolGroupCandidate(item, entries)
			return policy.kind, ok
		},
		build: func(run []transcriptItem) transcriptItem {
			first := run[0]
			return transcriptItem{
				key:          transcriptItemKey{anchor: first.primary.key, kind: transcriptItemToolGroup},
				kind:         transcriptItemToolGroup,
				role:         first.role,
				primary:      first.primary,
				groupMembers: append([]transcriptItem(nil), run...),
				displayState: toolDisplaySuccess,
				coord:        first.coord,
			}
		},
	})
}

func compactToolGroupPolicy(item transcriptItem, entries []transcript.Entry) (toolGroupPolicy, bool) {
	if item.kind != transcriptItemToolGroup || len(item.groupMembers) == 0 {
		return toolGroupPolicy{}, false
	}
	policy, _, ok := compactToolGroupCandidate(item.groupMembers[0], entries)
	return policy, ok
}

func compactToolGroupRows(item transcriptItem, entries []transcript.Entry) []compactToolRow {
	rows := make([]compactToolRow, 0, min(len(item.groupMembers), 5))
	appendMember := func(member transcriptItem) {
		_, detail, ok := compactToolGroupCandidate(member, entries)
		if ok {
			rows = append(rows, compactToolRow{text: detail})
		}
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

func pluralCount(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(n) + " " + plural
}
