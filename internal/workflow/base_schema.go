package workflow

// BaseSchema is the output contract automatically applied to every agent step.
// It guarantees that every agent always produces a minimum set of structured
// fields regardless of whether the step declares a [step.schema]. Declared
// schema fields are appended at dispatch time; field name collisions with the
// base are caught at jig validate time.
var BaseSchema = &Schema{
	Fields: []*Field{
		{Name: "assumptions", Type: FieldList, Elem: &Field{Name: "assumptions[]", Type: FieldText}},
		{Name: "confidence", Type: FieldEnum, Enum: []string{"high", "medium", "low"}},
		{Name: "issues", Type: FieldList, Elem: &Field{Name: "issues[]", Type: FieldText}},
		{Name: "status", Type: FieldEnum, Enum: []string{"succeeded", "partial", "failed", "blocked"}},
		{Name: "summary", Type: FieldText},
	},
}

// BaseFieldNames is the reserved set of top-level field names defined by
// BaseSchema. Used at load time to reject declared schemas that collide.
var BaseFieldNames = func() map[string]bool {
	m := make(map[string]bool, len(BaseSchema.Fields))
	for _, f := range BaseSchema.Fields {
		m[f.Name] = true
	}
	return m
}()

// MergedSchema returns a Schema containing the base fields plus any fields
// from declared. If declared is nil, BaseSchema is returned directly.
// The caller must not mutate the returned value.
func MergedSchema(declared *Schema) *Schema {
	if declared == nil {
		return BaseSchema
	}
	merged := &Schema{
		Fields: make([]*Field, 0, len(BaseSchema.Fields)+len(declared.Fields)),
	}
	merged.Fields = append(merged.Fields, BaseSchema.Fields...)
	merged.Fields = append(merged.Fields, declared.Fields...)
	return merged
}
