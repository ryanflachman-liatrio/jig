package runner

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// baseFieldOrder defines the display order for base schema fields. Fields not
// in this list are custom and rendered after in alphabetical order.
var baseFieldOrder = []string{"status", "confidence", "summary", "assumptions", "issues", "block_reason"}

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

// structuredToMarkdown renders the full structured result. It is also the
// fallback deliverable when an agent supplies only base metadata.
// Returns "" if raw is empty or unparseable.
func structuredToMarkdown(raw json.RawMessage) string {
	return structuredObjectToMarkdown(decodeOutputObject(raw))
}

func decodeOutputObject(raw json.RawMessage) map[string]any {
	if !json.Valid(raw) {
		return nil
	}
	var obj map[string]any
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber() // Human-readable output must not round large integers or decimals.
	if err := d.Decode(&obj); err != nil {
		return nil
	}
	return obj
}

// metadataToMarkdown renders only the base status fields, keeping declared
// documents in output.md rather than duplicating them in metadata.md.
func metadataToMarkdown(raw json.RawMessage) string {
	obj := decodeOutputObject(raw)
	for name := range obj {
		if !workflow.BaseFieldNames[name] {
			delete(obj, name)
		}
	}
	return structuredObjectToMarkdown(obj)
}

// agentOutputMarkdown preserves final prose and renders declared fields as
// Markdown. The matching transport envelope is omitted; the transcript and
// output.json retain the original text and structured values respectively.
func agentOutputMarkdown(text string, raw json.RawMessage) string {
	body := stripOutputEnvelope(text, raw)
	if obj := decodeOutputObject(raw); len(obj) > 0 {
		for name := range obj {
			if workflow.BaseFieldNames[name] {
				delete(obj, name)
			}
		}
		if fields := structuredObjectToMarkdown(obj); fields != "" {
			if strings.TrimSpace(body) == "" {
				return fields
			}
			return strings.TrimRight(body, " \t\r\n") + "\n\n" + fields
		}
	}
	if strings.TrimSpace(body) != "" {
		return body
	}
	return structuredToMarkdown(raw)
}

// Remove only a complete, matching final JSON envelope. Earlier JSON examples,
// mismatched envelopes, and trailing prose remain part of the deliverable.
func stripOutputEnvelope(text string, raw json.RawMessage) string {
	if len(raw) == 0 {
		return text
	}
	equal := func(candidate string) bool {
		if !json.Valid([]byte(candidate)) || !json.Valid(raw) {
			return false
		}
		decode := func(s string) any {
			var value any
			d := json.NewDecoder(strings.NewReader(s))
			d.UseNumber()
			_ = d.Decode(&value) // Validated above; preserve large numeric values.
			return value
		}
		return reflect.DeepEqual(decode(candidate), decode(string(raw)))
	}
	trimmed := strings.TrimSpace(text)
	if equal(trimmed) {
		return ""
	}
	const opener = "```json"
	start := strings.LastIndex(trimmed, "\n"+opener)
	if start >= 0 {
		start++
	} else if strings.HasPrefix(trimmed, opener) {
		start = 0
	} else {
		return text
	}
	if strings.HasSuffix(trimmed, "\n```") {
		candidate := strings.TrimSpace(trimmed[start+len(opener) : len(trimmed)-3])
		if equal(candidate) {
			return strings.TrimRight(trimmed[:start], " \t\r\n")
		}
	}
	return text
}

func structuredObjectToMarkdown(obj map[string]any) string {

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
	case json.Number:
		fmt.Fprintf(sb, "## %s\n\n%s\n\n", heading, v)
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
	case json.Number:
		return string(x)
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
