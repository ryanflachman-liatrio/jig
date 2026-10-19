package monitor

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/ryanflachman-liatrio/jig/internal/toolcall"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

const executionSummaryWidth = 80

func executionSummary(activity *toolcall.Activity, width int) string {
	detail := sanitizeToolSummary(ansi.Strip(activity.Summary))
	command, _ := executionCommand(activity.Input)
	if detail == "" {
		title := sanitizeToolSummary(ansi.Strip(activity.Title))
		plainTitle := strings.ReplaceAll(strings.Trim(title, "`"), "\\`", "`")
		switch strings.ToLower(plainTitle) {
		case "", "bash", "shell", "run", "terminal", "execute", "run command", "run a command", "undefined":
		default:
			if plainTitle != sanitizeToolSummary(command) {
				detail = title
			}
		}
	}
	if detail == "" {
		first, rest, multiline := strings.Cut(strings.TrimSpace(command), "\n")
		detail = sanitizeToolSummary(ansi.Strip(first))
		if multiline && strings.TrimSpace(rest) != "" {
			detail += " " + shared.EllipsisGlyph
		}
	}
	limit := executionSummaryWidth
	// Compact grouping asks for a summary before the panel width is known.
	if width > 0 {
		limit = min(limit, width)
	}
	return shared.TruncateTitle(detail, limit)
}

// executionCommand only decodes a string command. Unknown input shapes remain
// available through the generic JSON view instead of guessing shell syntax.
func executionCommand(input json.RawMessage) (string, bool) {
	args := decodeToolArgs(input)
	var command string
	if json.Unmarshal(args["command"], &command) != nil || strings.TrimSpace(command) == "" {
		return "", false
	}
	return command, true
}

func (m *Model) writeExecutionInput(b *strings.Builder, activity *toolcall.Activity) bool {
	if canonicalActivityKind(activity) != "bash" {
		return false
	}
	command, ok := executionCommand(activity.Input)
	if !ok {
		return false
	}
	// Decoding JSON exposes literal controls. Keep shell line breaks, expand
	// tabs for predictable wrapping, and remove terminal control sequences.
	command = strings.ReplaceAll(ansi.Strip(command), "\t", "    ")
	command = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' {
			return -1
		}
		return r
	}, command)
	// Input is fixed even while output is streaming, so always show its head.
	m.writeItemDetail(b, "Command", command, detailAnchorHead)
	args := decodeToolArgs(activity.Input)
	delete(args, "command")
	if len(args) > 0 {
		rest, err := json.Marshal(args)
		if err == nil {
			m.writeItemDetail(b, "Arguments", prettyToolInput(rest), detailAnchorHead)
		}
	}
	return true
}
