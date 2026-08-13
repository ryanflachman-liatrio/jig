package runner

import (
	"encoding/json"
	"fmt"
	"strings"
)

// baseFieldOrder defines the display order for base schema fields. Fields not
// in this list are custom and rendered after in alphabetical order.
var baseFieldOrder = []string{"status", "confidence", "summary", "assumptions", "issues"}

// headingOverrides maps field names to their display headings when the default
// humanize() output is not the right label.
var headingOverrides = map[string]string{}

var baseFieldSet = func() map[string]bool {
	m := make(map[string]bool, len(baseFieldOrder))
	for _, f := range baseFieldOrder {
		m[f] = true
	}
	return m
}()

// structuredToMarkdown converts a step's structured JSON output (output.json)
// into a human-readable markdown document for output.md. Base schema fields
// are rendered in a fixed order; custom declared fields follow alphabetically.
// Returns "" if raw is empty or unparseable.
func structuredToMarkdown(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || len(obj) == 0 {
		return ""
	}

	var sb strings.Builder

	// Base fields in canonical order.
	for _, name := range baseFieldOrder {
		val, ok := obj[name]
		if !ok {
			continue
		}
		renderField(&sb, name, val)
	}

	// Custom declared fields in sorted order.
	customs := make([]string, 0)
	for name := range obj {
		if !baseFieldSet[name] {
			customs = append(customs, name)
		}
	}
	sortStrings(customs)
	for _, name := range customs {
		renderField(&sb, name, obj[name])
	}

	return sb.String()
}

// renderField writes one named field as a markdown section. Empty/nil/zero
// values are skipped.
func renderField(sb *strings.Builder, name string, val any) {
	if val == nil {
		return
	}
	heading := headingOverrides[name]
	if heading == "" {
		heading = humanize(name)
	}

	switch v := val.(type) {
	case string:
		if v == "" {
			return
		}
		fmt.Fprintf(sb, "## %s\n\n%s\n\n", heading, v)
	case bool:
		fmt.Fprintf(sb, "## %s\n\n%v\n\n", heading, v)
	case float64:
		// JSON numbers decode as float64; render without trailing zeros when integral.
		if v == float64(int64(v)) {
			fmt.Fprintf(sb, "## %s\n\n%d\n\n", heading, int64(v))
		} else {
			fmt.Fprintf(sb, "## %s\n\n%g\n\n", heading, v)
		}
	case []any:
		if len(v) == 0 {
			return
		}
		fmt.Fprintf(sb, "## %s\n\n", heading)
		for _, item := range v {
			fmt.Fprintf(sb, "- %s\n", anyToString(item))
		}
		sb.WriteString("\n")
	case map[string]any:
		if len(v) == 0 {
			return
		}
		fmt.Fprintf(sb, "## %s\n\n", heading)
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sortStrings(keys)
		for _, k := range keys {
			fmt.Fprintf(sb, "- **%s:** %s\n", humanize(k), anyToString(v[k]))
		}
		sb.WriteString("\n")
	}
}

// humanize converts a snake_case field name to Title Case for display.
func humanize(s string) string {
	words := strings.Split(s, "_")
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// anyToString converts a JSON value to a compact string for list items.
func anyToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return fmt.Sprintf("%v", x)
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// sortStrings sorts a string slice in place (insertion sort — avoids importing
// sort for a small helper).
func sortStrings(ss []string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j] < ss[j-1]; j-- {
			ss[j], ss[j-1] = ss[j-1], ss[j]
		}
	}
}
