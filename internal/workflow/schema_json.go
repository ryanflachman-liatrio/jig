package workflow

import (
	"encoding/json"
	"fmt"
	"sort"
)

// JSONSchema renders the schema as a JSON Schema document, ready to hand to
// `claude -p --json-schema`. Inline [step.schema] tables are compiled here; a
// schema loaded from a file round-trips back to equivalent JSON.
func (sc *Schema) JSONSchema() ([]byte, error) {
	if sc == nil {
		return nil, fmt.Errorf("nil schema")
	}
	return json.Marshal(objectNode(sc.Fields))
}

// objectNode builds the JSON Schema node for an object with the given fields.
// Every declared field is required and additionalProperties is disabled: a
// producer's output contract is closed, which is what makes constrained
// decoding worth having.
func objectNode(fields []*Field) map[string]any {
	props := make(map[string]any, len(fields))
	required := make([]string, 0, len(fields))
	for _, f := range fields {
		props[f.Name] = fieldNode(f)
		required = append(required, f.Name)
	}
	sort.Strings(required)
	return map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             required,
		"additionalProperties": false,
	}
}

func fieldNode(f *Field) map[string]any {
	switch f.Type {
	case FieldText:
		return map[string]any{"type": "string"}
	case FieldNumber:
		return map[string]any{"type": "number"}
	case FieldBool:
		return map[string]any{"type": "boolean"}
	case FieldEnum:
		return map[string]any{"type": "string", "enum": toAnySlice(f.Enum)}
	case FieldList:
		return map[string]any{"type": "array", "items": fieldNode(f.Elem)}
	case FieldObject:
		return objectNode(f.Fields)
	default:
		return map[string]any{} // FieldAny: accept anything
	}
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// ParseJSONSchema converts a raw JSON Schema document into a Schema so that
// field references against a schema_file get the same type checking as an
// inline table. It maps the subset jig understands (object/string/number/
// boolean/array/enum) and treats anything else as opaque (FieldAny) rather than
// failing, so unusual schemas still load — they just skip deep ref checks.
func ParseJSONSchema(data []byte) (*Schema, error) {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	if t := schemaType(root); t != "object" {
		return nil, fmt.Errorf("top-level schema must be type \"object\", got %q", t)
	}
	return &Schema{Fields: objectFields(root)}, nil
}

// objectFields reads the "properties" table of an object node into Fields.
func objectFields(node map[string]any) []*Field {
	props, _ := node["properties"].(map[string]any)
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := make([]*Field, 0, len(names))
	for _, name := range names {
		sub, _ := props[name].(map[string]any)
		fields = append(fields, jsonField(name, sub))
	}
	return fields
}

func jsonField(name string, node map[string]any) *Field {
	if node == nil {
		return &Field{Name: name, Type: FieldAny}
	}
	if raw, ok := node["enum"]; ok {
		return &Field{Name: name, Type: FieldEnum, Enum: enumStrings(raw)}
	}
	switch schemaType(node) {
	case "string":
		return &Field{Name: name, Type: FieldText}
	case "number", "integer":
		return &Field{Name: name, Type: FieldNumber}
	case "boolean":
		return &Field{Name: name, Type: FieldBool}
	case "array":
		items, _ := node["items"].(map[string]any)
		return &Field{Name: name, Type: FieldList, Elem: jsonField(name+"[]", items)}
	case "object":
		return &Field{Name: name, Type: FieldObject, Fields: objectFields(node)}
	default:
		return &Field{Name: name, Type: FieldAny}
	}
}

// schemaType reads a node's "type", tolerating the JSON Schema union form
// (["string","null"]) by taking the first non-null entry.
func schemaType(node map[string]any) string {
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

func enumStrings(raw any) []string {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		out = append(out, fmt.Sprint(e))
	}
	return out
}
