package runexport

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"time"

	"jig/internal/toolcall"
	"jig/internal/transcript"
)

// transcriptLine is one decoded (or malformed) source line, kept minimal so
// structural mode can inspect availability/damage counts without retaining
// text (spec: "Structural mode may inspect transcript records for evidence
// availability and damage counts, but exports no transcript text").
type transcriptLine struct {
	entry     transcript.Entry
	malformed bool
}

// decodeTranscriptPrefix streams r one line at a time, skipping malformed
// complete lines and a torn final line while continuing with later
// well-formed records (spec FR-13: transcript decoding may skip and
// continue, unlike the journal's stop-at-first-malformed-record rule).
// gaps accumulates one Gap per skipped record (bounded by len(gaps) growth,
// which is itself bounded by the file's line count under maxStepInventory
// callers' control).
func decodeTranscriptPrefix(r io.Reader, stepAlias string) (lines []transcriptLine, gaps []Gap) {
	br := bufio.NewReaderSize(r, 64*1024)
	line := 0
	for {
		line++
		data, err := readBoundedLine(br, maxInputRecord)
		switch {
		case errors.Is(err, io.EOF):
			return lines, gaps
		case errors.Is(err, errLineTorn):
			gaps = append(gaps, Gap{Reason: GapTornRecord, Source: "transcript", Alias: stepAlias, Line: line})
			return lines, gaps
		case errors.Is(err, errLineOversized):
			gaps = append(gaps, Gap{Reason: GapOversizedRecord, Source: "transcript", Alias: stepAlias, Line: line})
			continue
		case err != nil:
			gaps = append(gaps, Gap{Reason: GapCorruptSource, Source: "transcript", Alias: stepAlias, Line: line})
			return lines, gaps
		}
		if len(data) == 0 {
			continue
		}
		var entry transcript.Entry
		if jsonErr := json.Unmarshal(data, &entry); jsonErr != nil {
			gaps = append(gaps, Gap{Reason: GapMalformedRecord, Source: "transcript", Alias: stepAlias, Line: line})
			continue
		}
		lines = append(lines, transcriptLine{entry: entry})
	}
}

// knownRoles/knownBlockTypes are the validated enums text mode retains as-is;
// anything else becomes the fixed "unknown" marker (spec FR-08/FR-10).
var knownRoles = map[transcript.Role]bool{
	transcript.RoleAssistant: true, transcript.RoleUser: true,
	transcript.RoleSystem: true, transcript.RoleResult: true,
}

// projectTranscriptEntry converts one accepted transcript entry into its
// text-mode record. sanitize is the caller-supplied full-replacement
// sanitizer (spec FR-09); toolScope keys tool-alias allocation.
func projectTranscriptEntry(stepAlias string, e transcript.Entry, anchor *timeAnchor, aliases *aliasTable, toolScope string, sanitize func(string) string) TranscriptRecord {
	role := string(e.Role)
	if !knownRoles[e.Role] {
		role = "unknown"
	}
	ts, _ := time.Parse(time.RFC3339, e.Ts)
	out := TranscriptRecord{
		StepAlias: stepAlias, Seq: e.Seq, TimeMS: anchor.relativeMS(ts),
		Iteration: e.Iteration, Attempt: e.Attempt, Generation: e.Generation, Role: role,
	}
	for _, b := range e.Blocks {
		out.Blocks = append(out.Blocks, projectBlock(b, aliases, toolScope, sanitize))
	}
	return out
}

func projectBlock(b transcript.Block, aliases *aliasTable, toolScope string, sanitize func(string) string) TranscriptBlock {
	switch b.Type {
	case transcript.BlockText:
		return TranscriptBlock{Type: "text", Text: sanitize(b.Text)}
	case transcript.BlockThinking:
		return TranscriptBlock{Type: "thinking", Omitted: "thinking_content"}
	case transcript.BlockToolUse, transcript.BlockToolResult:
		return projectToolBlock(string(b.Type), b.Activity(), aliases, toolScope, sanitize)
	default:
		return TranscriptBlock{Type: "unknown", Omitted: "unsupported_block"}
	}
}

func projectToolBlock(blockType string, act *toolcall.Activity, aliases *aliasTable, toolScope string, sanitize func(string) string) TranscriptBlock {
	out := TranscriptBlock{Type: blockType}
	if act == nil {
		return out
	}
	if act.ID != "" {
		out.ToolAlias = aliases.allocateTool(toolScope, act.ID)
	}
	out.ToolTitle = sanitize(act.Title)
	out.ToolStatus = act.Status
	if len(act.Input) > 0 {
		out.ToolInput = sanitizeJSONPayload(act.Input, sanitize)
	}
	if len(act.Output) > 0 {
		out.ToolOutput = sanitizeJSONPayload(act.Output, sanitize)
	}
	for _, content := range act.Content {
		if content.Diff != nil {
			d := &TranscriptDiff{Path: sanitize(content.Diff.Path), NewText: sanitize(content.Diff.NewText)}
			if content.Diff.OldText != nil {
				old := sanitize(*content.Diff.OldText)
				d.OldText = &old
			}
			out.Diff = d
			continue
		}
		if content.Type == "text" && content.Text != "" && out.ToolOutput == "" {
			out.ToolOutput = sanitize(content.Text)
		}
	}
	return out
}

// sanitizeJSONPayload parses raw tool JSON into ordinary values, recursively
// sanitizes string keys/values, and renders the result as stable plain text
// (spec FR-10: "the exported field shall be a JSON string, not a raw JSON
// insertion"). An invalid payload is never echoed — it becomes a fixed
// omission marker instead of raw bytes or a parser error.
func sanitizeJSONPayload(raw json.RawMessage, sanitize func(string) string) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "[omitted: malformed_payload]"
	}
	sanitized := sanitizeJSONValue(v, sanitize)
	data, err := json.Marshal(sanitized)
	if err != nil {
		return "[omitted: malformed_payload]"
	}
	return sanitize(string(data))
}

func sanitizeJSONValue(v any, sanitize func(string) string) any {
	switch val := v.(type) {
	case string:
		return sanitize(val)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, item := range val {
			out[sanitize(k)] = sanitizeJSONValue(item, sanitize)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = sanitizeJSONValue(item, sanitize)
		}
		return out
	default:
		return val
	}
}
