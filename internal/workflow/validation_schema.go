package workflow

import (
	"bytes"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type capturedSchemaLoader struct{}

func (capturedSchemaLoader) Load(string) (any, error) {
	return nil, fmt.Errorf("external schema resolution is disabled")
}

// CompileValidationSchema accepts Draft 2020-12 and document-local references.
// No file or network loader participates, including during meta-validation.
// Errors intentionally omit schema contents and reference values.
func CompileValidationSchema(data []byte) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("output_schema must be valid JSON")
	}
	if !validationDialectSupported(doc) {
		return nil, fmt.Errorf("output_schema requires JSON Schema Draft 2020-12")
	}

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(capturedSchemaLoader{})
	const uri = "https://jig.invalid/captured-schema.json"
	if err := c.AddResource(uri, doc); err != nil {
		return nil, fmt.Errorf("invalid captured output_schema")
	}
	schema, err := c.Compile(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid output_schema or external reference")
	}
	return schema, nil
}

// Check embedded schema resources too. Literal JSON values are instance data,
// so $schema keys inside const/enum/default/examples do not select a dialect.
func validationDialectSupported(value any) bool {
	switch node := value.(type) {
	case map[string]any:
		if dialect, exists := node["$schema"]; exists && dialect != "https://json-schema.org/draft/2020-12/schema" && dialect != "https://json-schema.org/draft/2020-12/schema#" {
			return false
		}
		for key, child := range node {
			switch key {
			case "const", "enum", "default", "examples":
				continue
			}
			if !validationDialectSupported(child) {
				return false
			}
		}
	case []any:
		for _, child := range node {
			if !validationDialectSupported(child) {
				return false
			}
		}
	}
	return true
}
