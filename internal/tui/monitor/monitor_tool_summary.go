package monitor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"

	"jig/internal/toolcall"
	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

// toolCallSummary is the sanitized, slot-shaped input the status-line
// renderer (slice 02) consumes. `icon` is retained for callers that still
// want a per-kind glyph; the Monitor header uses `kind` with
// shared.ToolStatusIcon and does not read this field for the icon slot.
// `kind` is the canonical name (`read`, `edit`, ...); empty when the
// activity could not be classified. `meta` carries known-kind secondary
// arguments (epic slice 15, e.g. grep's `path`/`case`/`gitignore`) destined
// for the status line's Meta slot; it stays nil for every kind that has no
// such secondary detail, so existing rows render byte-for-byte unchanged.
type toolCallSummary struct {
	icon   string
	action string
	detail string
	kind   string
	meta   []string
}

func summarizeToolCall(blk transcript.Block, width int) toolCallSummary {
	return summarizeActivity(blk.Activity(), width)
}

// summarizeActivity classifies a tool activity into a toolCallSummary. width
// is the panel content width available for the fallback preview and any
// Meta-slot secondary arguments; both are budgeted with formatArgsInline
// rather than clipped after composing the row (epic slice 15, FR-15.2).
func summarizeActivity(activity *toolcall.Activity, width int) toolCallSummary {
	if activity == nil {
		return toolSummary(shared.IconToolCall, "Tool", "", "")
	}
	args := decodeToolArgs(activity.Input)
	kind := strings.ToLower(strings.TrimSpace(activity.Kind))
	if kind == "" {
		kind = canonicalToolName(activity.Title)
	}
	if kind == "edit" {
		for _, content := range activity.Content {
			if content.Diff != nil {
				return toolSummary(shared.IconToolEdit, "Edit", shortFile(content.Diff.Path), "edit")
			}
		}
		for _, location := range activity.Locations {
			if location.Path != "" {
				return toolSummary(shared.IconToolEdit, "Edit", shortFile(location.Path), "edit")
			}
		}
	}
	if kind == "" {
		kind = inferToolKind(args)
	}

	switch kind {
	case "read":
		return toolSummary(shared.IconToolRead, "Read", shortFile(stringArg(args, "file_path", "path")), kind)
	case "edit":
		return toolSummary(shared.IconToolEdit, "Edit", shortFile(stringArg(args, "file_path", "path")), kind)
	case "write":
		return toolSummary(shared.IconToolWrite, "Write", shortFile(stringArg(args, "file_path", "path")), kind)
	case "notebookedit":
		return toolSummary(shared.IconToolEdit, "Edit notebook", shortFile(stringArg(args, "notebook_path", "target_notebook", "path")), kind)
	case "glob":
		return toolSummary(shared.IconToolSearch, "Find", stringArg(args, "pattern", "glob_pattern"), kind)
	case "grep":
		s := toolSummary(shared.IconToolSearch, "Search", stringArg(args, "pattern", "query"), kind)
		s.meta = grepMetaArgs(args, width)
		return s
	case "bash":
		return toolSummary(shared.IconToolShell, "Run", stringArg(args, "command"), kind)
	case "websearch":
		return toolSummary(shared.IconToolWeb, "Search web", stringArg(args, "query", "search_term"), kind)
	case "webfetch":
		return toolSummary(shared.IconToolWeb, "Fetch", shortHost(stringArg(args, "url")), kind)
	case "task":
		return toolSummary(shared.IconToolAgent, taskAction(stringArg(args, "subagent_type")), stringArg(args, "description"), kind)
	case "todowrite":
		return toolSummary(shared.IconToolTodo, "Update", countLabel(arrayLen(args, "todos"), "task", "tasks"), kind)
	case "todoread":
		return toolSummary(shared.IconToolTodo, "Read todos", "", kind)
	case "askuserquestion":
		return toolSummary(shared.IconToolAsk, "Ask", firstQuestion(args), kind)
	case "skill":
		return toolSummary(shared.IconToolAgent, "Use skill", stringArg(args, "skill"), kind)
	}

	name := displayToolName(activity.Title)
	if name == "" {
		name = "Tool"
	}
	return toolSummary(shared.IconToolCall, name, formatArgsInline(args, width), kind)
}

// grepMetaArgs renders grep's secondary arguments (scope beyond the curated
// pattern/query detail already in Description) as a single Meta-slot entry,
// or nil when none of them are present.
func grepMetaArgs(args map[string]json.RawMessage, width int) []string {
	secondary := make(map[string]json.RawMessage, 3)
	for _, key := range []string{"path", "case", "gitignore"} {
		if raw, ok := args[key]; ok {
			secondary[key] = raw
		}
	}
	preview := formatArgsInline(secondary, width)
	if preview == "" {
		return nil
	}
	return []string{sanitizeToolSummary(preview)}
}

// toolSummary sanitizes each slot so agent-controlled tool arguments cannot
// carry embedded control characters into the transcript row. It records the
// canonical `kind` alongside the presentation fields so the Monitor renderer
// can look up the settled-success signature glyph via shared.ToolStatusIcon
// without re-canonicalizing here.
func toolSummary(icon, action, detail, kind string) toolCallSummary {
	return toolCallSummary{
		icon:   sanitizeToolSummary(icon),
		action: sanitizeToolSummary(action),
		detail: sanitizeToolSummary(detail),
		kind:   strings.ToLower(strings.TrimSpace(kind)),
	}
}

func decodeToolArgs(raw json.RawMessage) map[string]json.RawMessage {
	var args map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &args) != nil {
		return nil
	}
	return args
}

func canonicalToolName(name string) string {
	name = displayToolName(name)
	lower := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.HasPrefix(lower, "web search"), strings.HasPrefix(lower, "search web"):
		return "websearch"
	case strings.HasPrefix(lower, "web fetch"), strings.HasPrefix(lower, "fetch web"):
		return "webfetch"
	}

	first, _, _ := strings.Cut(lower, " ")
	first = strings.Trim(first, "[]():")
	switch first {
	case "read", "readfile", "read_file":
		return "read"
	case "edit", "multiedit", "editfile", "edit_file":
		return "edit"
	case "write", "writefile", "write_file", "create":
		return "write"
	case "notebookedit", "notebook_edit":
		return "notebookedit"
	case "glob", "find", "file_search":
		return "glob"
	case "grep", "search", "grep_search":
		return "grep"
	case "bash", "shell", "run", "run_terminal_cmd":
		return "bash"
	case "websearch", "web_search":
		return "websearch"
	case "webfetch", "web_fetch":
		return "webfetch"
	case "task", "agent", "subagent":
		return "task"
	case "todowrite", "todo_write":
		return "todowrite"
	case "todoread", "todo_read":
		return "todoread"
	case "askuserquestion", "ask_user_question":
		return "askuserquestion"
	case "skill":
		return "skill"
	default:
		return ""
	}
}

func inferToolKind(args map[string]json.RawMessage) string {
	switch {
	case hasArg(args, "questions"):
		return "askuserquestion"
	case hasArg(args, "todos"):
		return "todowrite"
	case hasArg(args, "command"):
		return "bash"
	case hasArg(args, "url"):
		return "webfetch"
	case hasArg(args, "subagent_type"):
		return "task"
	case hasArg(args, "notebook_path", "target_notebook"):
		return "notebookedit"
	case hasArg(args, "old_string", "new_string"):
		return "edit"
	case hasArg(args, "content") && hasArg(args, "file_path", "path"):
		return "write"
	case hasArg(args, "file_path"):
		return "read"
	case hasArg(args, "search_term"):
		return "websearch"
	default:
		return ""
	}
}

func displayToolName(name string) string {
	if i := strings.LastIndex(name, "__"); i >= 0 {
		name = name[i+2:]
	}
	return sanitizeToolSummary(name)
}

// sanitizeToolSummary keeps collapsed activity rows terminal-safe. Raw tool
// names and inputs remain available only in expanded, verbatim detail views.
func sanitizeToolSummary(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

func stringArg(args map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		raw, ok := args[key]
		if !ok {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil {
			return strings.Join(strings.Fields(value), " ")
		}
	}
	return ""
}

func hasArg(args map[string]json.RawMessage, keys ...string) bool {
	for _, key := range keys {
		if _, ok := args[key]; ok {
			return true
		}
	}
	return false
}

func arrayLen(args map[string]json.RawMessage, key string) int {
	var values []json.RawMessage
	if json.Unmarshal(args[key], &values) != nil {
		return 0
	}
	return len(values)
}

func firstQuestion(args map[string]json.RawMessage) string {
	var questions []struct {
		Question string `json:"question"`
	}
	if json.Unmarshal(args["questions"], &questions) != nil || len(questions) == 0 {
		return ""
	}
	return strings.Join(strings.Fields(questions[0].Question), " ")
}

func countLabel(n int, singular, plural string) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "1 " + singular
	default:
		return strconv.Itoa(n) + " " + plural
	}
}

func taskAction(kind string) string {
	switch strings.ToLower(kind) {
	case "explore":
		return "Explore"
	case "computeruse", "computer_use":
		return "Test"
	case "videoreview", "video_review":
		return "Review video"
	case "bugbot":
		return "Review"
	case "security-review", "security_review":
		return "Security review"
	case "best-of-n-runner", "best_of_n_runner":
		return "Experiment"
	default:
		return "Agent"
	}
}

func shortFile(path string) string {
	path = strings.TrimRight(strings.TrimSpace(path), `/\`)
	if path == "" {
		return ""
	}
	if i := max(strings.LastIndex(path, "/"), strings.LastIndex(path, `\`)); i >= 0 {
		return path[i+1:]
	}
	return path
}

func shortHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return rawURL
}

// argKeyPriority orders the keys formatArgsInline is most likely to find
// informative first; every other key present follows in lexicographic order.
// Go map iteration order is randomized, so this ordering — not map
// iteration — is what keeps the rendered preview stable across renders
// (epic slice 15, jig-side addition since omp relies on JS object insertion
// order, which Go's map cannot reproduce).
var argKeyPriority = []string{"path", "file_path", "command", "pattern", "query", "url"}

// secretArgKeyWords are matched case-insensitively as substrings of an
// argument key name. A matching key's value is never rendered, regardless
// of remaining width budget, because the key name alone suggests a
// credential. This is a formatter-local heuristic distinct from
// internal/runner's redactSecrets, which replaces known configured secret
// string values wherever they occur in text; it has no concept of argument
// key names and would not catch a secret-shaped value it was never
// configured to know about.
var secretArgKeyWords = []string{"token", "key", "password", "secret"}

func isHiddenArgKey(key string) bool {
	return strings.HasPrefix(key, "__")
}

func isSecretArgKey(key string) bool {
	lower := strings.ToLower(key)
	for _, word := range secretArgKeyWords {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

// orderArgKeys returns args' keys in a deterministic order: the priority
// list first (for whichever of those keys are present), then every
// remaining non-hidden key sorted lexicographically.
func orderArgKeys(args map[string]json.RawMessage) []string {
	seen := make(map[string]bool, len(args))
	ordered := make([]string, 0, len(args))
	for _, key := range argKeyPriority {
		if _, ok := args[key]; ok && !isHiddenArgKey(key) {
			ordered = append(ordered, key)
			seen[key] = true
		}
	}
	rest := make([]string, 0, len(args))
	for key := range args {
		if seen[key] || isHiddenArgKey(key) {
			continue
		}
		rest = append(rest, key)
	}
	sort.Strings(rest)
	return append(ordered, rest...)
}

// formatArgsInline renders tool arguments as a "key=value, key=value"
// preview that never exceeds maxWidth. Before spending width on a key it
// reserves the minimal footprint of every key still pending (separator +
// key name + "=" + a short value stand-in), so one long value cannot starve
// the keys that follow it — the fair-share budget from the omp reference
// (docs/epics/omp-transcript-parity/slices/15-inline-arg-formatting.md,
// tools/json-tree.ts:53-92), adapted to use orderArgKeys instead of object
// insertion order. When the budget runs out before every key is placed, the
// result ends in an ellipsis; shared.TruncateTitle is the final safety net
// so the width invariant holds even at pathologically narrow widths.
func formatArgsInline(args map[string]json.RawMessage, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	keys := orderArgKeys(args)
	if len(keys) == 0 {
		return ""
	}

	pieces := make([]string, 0, len(keys))
	width := 0
	for i, key := range keys {
		sepWidth := 0
		if i > 0 {
			sepWidth = 2 // ", "
		}
		tailReserve := 0
		for _, pending := range keys[i+1:] {
			tailReserve += 2 + lipgloss.Width(pending) + 1 + 4
		}
		pieceBudget := maxWidth - width - sepWidth - tailReserve

		exhausted := pieceBudget < 1
		var piece string
		if !exhausted {
			if isSecretArgKey(key) {
				piece = key + "=<redacted>"
			} else {
				valueMaxLen := pieceBudget - lipgloss.Width(key) - 1 // "="
				if valueMaxLen < 1 {
					valueMaxLen = 1
				}
				piece = key + "=" + formatScalarArg(args[key], valueMaxLen)
			}
			exhausted = lipgloss.Width(piece) > pieceBudget
		}

		if exhausted {
			joined := strings.Join(pieces, ", ") + shared.EllipsisGlyph
			return shared.TruncateTitle(joined, maxWidth)
		}

		pieces = append(pieces, piece)
		width += sepWidth + lipgloss.Width(piece)
	}
	return strings.Join(pieces, ", ")
}

// formatScalarArg renders one argument value within valueMaxLen display
// cells. It inspects the raw JSON's leading byte rather than unmarshaling
// into a generic interface{}, so an array or object value is summarized by
// its shallow element/key count without decoding (and therefore fully
// allocating) large nested content such as a big `content` payload.
func formatScalarArg(raw json.RawMessage, valueMaxLen int) string {
	if valueMaxLen < 1 {
		valueMaxLen = 1
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return shared.TruncateTitle(`""`, valueMaxLen)
	}
	switch trimmed[0] {
	case '"':
		var s string
		if json.Unmarshal(trimmed, &s) != nil {
			return shared.TruncateTitle(string(trimmed), valueMaxLen)
		}
		escaped := strings.NewReplacer("\n", "\\n", "\t", "\\t").Replace(s)
		return shared.TruncateTitle(`"`+escaped+`"`, valueMaxLen)
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(trimmed, &items) != nil {
			return shared.TruncateTitle("[items]", valueMaxLen)
		}
		return shared.TruncateTitle(fmt.Sprintf("[%d items]", len(items)), valueMaxLen)
	case '{':
		var obj map[string]json.RawMessage
		if json.Unmarshal(trimmed, &obj) != nil {
			return shared.TruncateTitle("{keys}", valueMaxLen)
		}
		return shared.TruncateTitle(fmt.Sprintf("{%d keys}", len(obj)), valueMaxLen)
	case 'n':
		return shared.TruncateTitle("null", valueMaxLen)
	default:
		// true, false, and numbers all round-trip as their own literal text.
		return shared.TruncateTitle(string(trimmed), valueMaxLen)
	}
}
