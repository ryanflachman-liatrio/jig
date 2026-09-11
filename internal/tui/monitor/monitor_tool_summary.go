package monitor

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"jig/internal/toolcall"
	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

// toolCallSummary is the sanitized, slot-shaped input the status-line
// renderer (slice 02) consumes. `icon` is retained for callers that still
// want a per-kind glyph; the Monitor header uses `kind` with
// shared.ToolStatusIcon and does not read this field for the icon slot.
// `kind` is the canonical name (`read`, `edit`, ...); empty when the
// activity could not be classified.
type toolCallSummary struct {
	icon   string
	action string
	detail string
	kind   string
}

func summarizeToolCall(blk transcript.Block) toolCallSummary {
	return summarizeActivity(blk.Activity())
}

func summarizeActivity(activity *toolcall.Activity) toolCallSummary {
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
		return toolSummary(shared.IconToolSearch, "Search", stringArg(args, "pattern", "query"), kind)
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
	return toolSummary(shared.IconToolCall, name, primaryToolArg(args), kind)
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

func primaryToolArg(args map[string]json.RawMessage) string {
	for _, key := range []string{"description", "title", "query", "search_term", "pattern", "command", "file_path", "path", "url", "skill"} {
		if value := stringArg(args, key); value != "" {
			switch key {
			case "file_path", "path":
				return shortFile(value)
			case "url":
				return shortHost(value)
			default:
				return value
			}
		}
	}
	return ""
}
