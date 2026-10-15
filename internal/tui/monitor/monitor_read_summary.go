package monitor

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ryanflachman-liatrio/jig/internal/toolcall"
)

// readSummaryForActivity uses the normalized activity's input, locations, or
// descriptive title. ACP reads need not have raw input; locations and titles
// remain presentation data and must not be turned into fabricated input.
func readSummaryForActivity(activity *toolcall.Activity) (string, bool) {
	if activity == nil {
		return "", false
	}
	kind := canonicalActivityKind(activity)
	if kind != "read" {
		return "", false
	}
	args := decodeToolArgs(activity.Input)
	target := sanitizeToolSummary(stringArg(args, "file_path", "path"))
	if target != "" {
		if !localReadPath(target) {
			return "", false
		}
		detail := shortFile(target)
		if selector := readSelector(activity); selector != "" {
			detail += ":" + selector
		}
		return detail, true
	}
	var locations []string
	for _, location := range activity.Locations {
		path := sanitizeToolSummary(location.Path)
		if path == "" {
			continue
		}
		if !localReadPath(path) {
			return "", false
		}
		detail := shortFile(path)
		if location.Line != nil && *location.Line > 0 {
			detail += ":" + strconv.Itoa(*location.Line)
		}
		locations = append(locations, detail)
	}
	if len(locations) > 0 {
		return strings.Join(locations, ", "), true
	}
	// A supplied descriptive title can summarize an operation such as a
	// directory listing even when no file location exists. Bare tool names
	// still carry no useful detail and must not qualify for compact grouping.
	title := sanitizeToolSummary(activity.Title)
	if title == "" || (!strings.Contains(title, " ") && canonicalToolName(title) != "") || strings.EqualFold(title, "tool") {
		return "", false
	}
	return title, true
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
