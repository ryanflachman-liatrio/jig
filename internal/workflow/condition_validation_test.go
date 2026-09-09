package workflow

import (
	"strings"
	"testing"
)

func TestDecodeAcceptsCompoundConditionsOnEverySurface(t *testing.T) {
	const source = `
[workflow]
name = "conditions"
version = "1"

[[step]]
id = "facts"
type = "agent"
skill = "s"
  [step.schema]
  ready = "bool"
  count = "number"

[[step]]
id = "toggle"
type = "command"
output_type = "bool"
run = "true"

[[step]]
id = "guarded"
type = "command"
depends_on = ["facts", "toggle"]
when = "facts.ready && (facts.count >= 2 || toggle)"
run = "true"

[[step]]
id = "interactive"
type = "agent"
skill = "s"
block_on = "interactive.needs_input && interactive.rounds < 3"
  [step.schema]
  needs_input = "bool"
  rounds = "number"

[[step]]
id = "quality"
type = "check"
depends_on = ["facts", "toggle"]
applies_when = "facts.ready && (facts.count > 0 || toggle == true)"
output_type = { enum = ["pass", "fail", "skip", "error"] }
run = "true"
  [step.findings]
  schema_version = 1
  file = "findings.json"
  required_tools = ["sh"]
[[step.route]]
when = "quality == 'fail' || quality == 'error'"
goto = "facts"
max_iterations = 1
`
	if _, err := Decode(source, ""); err != nil {
		t.Fatalf("Decode: %v", err)
	}
}

func TestDecodeChecksEveryCompoundReference(t *testing.T) {
	const source = `
[workflow]
name = "conditions"
version = "1"
[[step]]
id = "a"
type = "command"
output_type = "bool"
run = "true"
[[step]]
id = "b"
type = "command"
output_type = "bool"
run = "true"
[[step]]
id = "consumer"
type = "command"
depends_on = ["a"]
when = "a && b"
run = "true"
`
	_, err := Decode(source, "")
	if err == nil || !strings.Contains(err.Error(), `references "b", which must be in its depends_on`) {
		t.Fatalf("Decode error = %v", err)
	}
}

func TestDecodeRejectsInvalidCompoundConditionTypes(t *testing.T) {
	base := `
[workflow]
name = "conditions"
version = "1"
[[step]]
id = "facts"
type = "agent"
skill = "s"
  [step.schema]
  enabled = "bool"
  score = "number"
  label = "text"
  state = { enum = ["ready", "blocked"] }
  items = { list = "text" }
[[step]]
id = "consumer"
type = "command"
depends_on = ["facts"]
when = %q
run = "true"
`
	tests := []struct {
		condition string
		want      string
	}{
		{condition: `facts.score > 'many'`, want: "finite JSON number"},
		{condition: `facts.label < 'z'`, want: "ordering comparisons require number"},
		{condition: `facts.enabled >= true`, want: "ordering comparisons require number"},
		{condition: `facts.state == 'missing'`, want: "not a valid value"},
		{condition: `facts.items == 'x'`, want: "cannot be compared"},
		{condition: `facts.score`, want: "requires it to be type bool"},
	}
	for _, tt := range tests {
		t.Run(tt.condition, func(t *testing.T) {
			_, err := Decode(sprintfTOML(base, tt.condition), "")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Decode error = %v, want %q", err, tt.want)
			}
		})
	}
}

func sprintfTOML(format, condition string) string {
	return strings.Replace(format, "%q", `"`+strings.ReplaceAll(condition, `"`, `\"`)+`"`, 1)
}

func TestDecodeCompoundRouteProofs(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "exhaustive enum disjunction",
			src: `
[workflow]
name = "routes"
version = "1"
[[step]]
id = "gate"
type = "command"
output_type = { enum = ["a", "b", "c"] }
run = "true"
[[step.route]]
when = "gate == 'a' || gate == 'b'"
goto = "gate"
max_iterations = 1
[[step.route]]
when = "gate == 'c'"
goto = "gate"
max_iterations = 1
`,
		},
		{
			name: "numeric ranges with fallback",
			src: `
[workflow]
name = "routes"
version = "1"
[[step]]
id = "gate"
type = "agent"
skill = "s"
  [step.schema]
  score = "number"
[[step.route]]
when = "gate.score < 5"
goto = "gate"
max_iterations = 1
[[step.route]]
when = "gate.score >= 5"
goto = "gate"
max_iterations = 1
[[step.route]]
fallback = true
goto = "gate"
max_iterations = 1
`,
		},
		{
			name: "overlapping conjunctions",
			src: `
[workflow]
name = "routes"
version = "1"
[[step]]
id = "gate"
type = "agent"
skill = "s"
  [step.schema]
  score = "number"
  ready = "bool"
[[step.route]]
when = "gate.score >= 2 && gate.ready"
goto = "gate"
max_iterations = 1
[[step.route]]
when = "gate.score < 5 && gate.ready"
goto = "gate"
max_iterations = 1
[[step.route]]
fallback = true
goto = "gate"
max_iterations = 1
`,
			want: "not mutually exclusive",
		},
		{
			name: "canonical duplicate",
			src: `
[workflow]
name = "routes"
version = "1"
[[step]]
id = "gate"
type = "command"
output_type = "bool"
run = "true"
[[step.route]]
when = "gate == true"
goto = "gate"
max_iterations = 1
[[step.route]]
when = "gate == 'true'"
goto = "gate"
max_iterations = 1
[[step.route]]
fallback = true
goto = "gate"
max_iterations = 1
`,
			want: "duplicate route guard",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(tt.src, "")
			if tt.want == "" && err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("Decode error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestDecodeRejectsCompoundBlockOnExternalReference(t *testing.T) {
	const source = `
[workflow]
name = "conditions"
version = "1"
[[step]]
id = "other"
type = "command"
output_type = "bool"
run = "true"
[[step]]
id = "agent"
type = "agent"
skill = "s"
block_on = "agent.needs_input && other"
  [step.schema]
  needs_input = "bool"
`
	_, err := Decode(source, "")
	if err == nil || !strings.Contains(err.Error(), "must reference this step's own output") {
		t.Fatalf("Decode error = %v", err)
	}
}
