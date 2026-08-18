package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// injectSchemaPrompt appends a structured output instruction to the user
// prompt. It generates both a human-readable field list (for models that
// respond better to plain-English descriptions) and the full JSON Schema (for
// precision). Together they make the instruction unambiguous even for models
// that do not parse JSON Schema natively.
func injectSchemaPrompt(prompt string, schema map[string]any) string {
	raw, err := json.Marshal(schema)
	if err != nil {
		return prompt
	}

	var sb strings.Builder
	sb.WriteString(prompt)
	sb.WriteString("\n\n## Required Output Format\n\n")
	sb.WriteString("Respond with ONLY a valid JSON object — no explanation, no prose, no markdown.\n\n")

	// Human-readable field list derived from the schema's properties.
	if props, ok := schema["properties"].(map[string]any); ok && len(props) > 0 {
		names := make([]string, 0, len(props))
		for name := range props {
			names = append(names, name)
		}
		sort.Strings(names)
		sb.WriteString("Required fields:\n")
		for _, name := range names {
			prop, _ := props[name].(map[string]any)
			sb.WriteString("- ")
			sb.WriteString(name)
			sb.WriteString(": ")
			sb.WriteString(describeSchemaNode(prop))
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("JSON Schema:\n")
	sb.WriteString(string(raw))
	return sb.String()
}

// retrySchemaPrompt builds a corrective follow-up for when the agent's first
// response was not valid JSON conforming to the schema.
func retrySchemaPrompt(schema map[string]any) string {
	raw, err := json.Marshal(schema)
	if err != nil {
		return "Your previous response was not valid JSON. Respond with only a valid JSON object. Output only the JSON, with no explanation or markdown."
	}
	return "Your previous response was not valid JSON matching the required schema. Respond with only a valid JSON object. No explanation, no prose, no markdown — just the raw JSON object.\n\nJSON Schema:\n" + string(raw)
}

// describeSchemaNode returns a short human-readable description of a JSON
// Schema node for the field list in injectSchemaPrompt.
func describeSchemaNode(node map[string]any) string {
	if node == nil {
		return "any value"
	}
	if enum, ok := node["enum"].([]any); ok {
		parts := make([]string, 0, len(enum))
		for _, e := range enum {
			parts = append(parts, fmt.Sprintf("%q", fmt.Sprint(e)))
		}
		return "one of " + strings.Join(parts, ", ")
	}
	switch schemaNodeType(node) {
	case "string":
		return "string"
	case "number", "integer":
		return "number"
	case "boolean":
		return "boolean (true or false)"
	case "array":
		items, _ := node["items"].(map[string]any)
		return "array of " + describeSchemaNode(items)
	case "object":
		return "object"
	default:
		return "any value"
	}
}

func schemaNodeType(node map[string]any) string {
	switch t := node["type"].(type) {
	case string:
		return t
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok && s != "null" {
				return s
			}
		}
	}
	return ""
}

// extractJSON attempts to parse text as a raw JSON value using a cascade of
// strategies:
//  1. Direct parse — the response is already clean JSON.
//  2. Strip a ```json fence — the model fenced it despite the instruction.
//  3. Strip a plain ``` fence.
//  4. Scan for embedded JSON — the model prefixed the JSON with prose.
//
// If all strategies fail the raw (trimmed) text is returned as-is; downstream
// JSON validation will surface the error clearly.
func extractJSON(text string) json.RawMessage {
	text = strings.TrimSpace(text)
	if json.Valid([]byte(text)) {
		return json.RawMessage(text)
	}
	if raw := extractFence(text, "```json"); raw != nil {
		return raw
	}
	if raw := extractFence(text, "```"); raw != nil {
		return raw
	}
	if raw := findEmbeddedJSON(text); raw != nil {
		return raw
	}
	return json.RawMessage(text)
}

func extractFence(text, marker string) json.RawMessage {
	start := strings.Index(text, marker)
	if start < 0 {
		return nil
	}
	after := text[start+len(marker):]
	// Skip optional language tag on the opening fence line.
	if nl := strings.IndexByte(after, '\n'); nl >= 0 {
		after = after[nl+1:]
	}
	end := strings.Index(after, "```")
	if end < 0 {
		return nil
	}
	inner := bytes.TrimSpace([]byte(after[:end]))
	if json.Valid(inner) {
		return inner
	}
	return nil
}

// findEmbeddedJSON scans text from left to right for the first `{` or `[`
// and tries to parse from that position forward. This recovers JSON that the
// model prefixed with prose (e.g. "Here is my response: {...}").
func findEmbeddedJSON(text string) json.RawMessage {
	for i := 0; i < len(text); i++ {
		if text[i] != '{' && text[i] != '[' {
			continue
		}
		candidate := bytes.TrimSpace([]byte(text[i:]))
		if json.Valid(candidate) {
			return candidate
		}
	}
	return nil
}
