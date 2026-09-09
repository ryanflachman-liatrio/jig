package workflow

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestParseConditionCanonicalForms(t *testing.T) {
	tests := []struct {
		raw        string
		want       string
		steps      []string
		predicates int
	}{
		{raw: "ready", want: "ready", steps: []string{"ready"}, predicates: 1},
		{raw: "review == 'approve'", want: `review == "approve"`, steps: []string{"review"}, predicates: 1},
		{raw: "research.status != blocked", want: `research.status != "blocked"`, steps: []string{"research"}, predicates: 1},
		{raw: "count.value >= -1.5", want: "count.value >= -1.5", steps: []string{"count"}, predicates: 1},
		{raw: "a || b && c", want: "a || b && c", steps: []string{"a", "b", "c"}, predicates: 3},
		{raw: "(a || b) && c", want: "(a || b) && c", steps: []string{"a", "b", "c"}, predicates: 3},
		{raw: "build-step.ok && build-step.score < 10", want: "build-step.ok && build-step.score < 10", steps: []string{"build-step"}, predicates: 2},
		{raw: `note.value == "x && y || z >= 2"`, want: `note.value == "x && y || z >= 2"`, steps: []string{"note"}, predicates: 1},
		{raw: `note.value == "line\nnext"`, want: `note.value == "line\nnext"`, steps: []string{"note"}, predicates: 1},
		{raw: "flag == true || flag == 'false'", want: `flag == true || flag == "false"`, steps: []string{"flag"}, predicates: 2},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			condition, err := ParseCondition(tt.raw)
			if err != nil {
				t.Fatalf("ParseCondition: %v", err)
			}
			if got := condition.String(); got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
			if got := condition.ReferencedSteps(); !reflect.DeepEqual(got, tt.steps) {
				t.Fatalf("ReferencedSteps() = %v, want %v", got, tt.steps)
			}
			if got := len(condition.Predicates()); got != tt.predicates {
				t.Fatalf("Predicates() = %d, want %d", got, tt.predicates)
			}
			if reparsed, err := ParseCondition(condition.String()); err != nil || reparsed.String() != tt.want {
				t.Fatalf("canonical text did not round trip: condition=%v err=%v", reparsed, err)
			}
		})
	}
}

func TestConditionRewriteRefsPreservesSemantics(t *testing.T) {
	condition, err := ParseCondition("external.ready && (module.first == 'yes' || module.second >= 2)")
	if err != nil {
		t.Fatal(err)
	}
	got, err := condition.RewriteRefs(func(ref ConditionRef) (ConditionRef, error) {
		if ref.Step == "module" {
			ref.Step = "phase__producer"
		}
		return ref, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `external.ready && (phase__producer.first == "yes" || phase__producer.second >= 2)`
	if got != want {
		t.Fatalf("RewriteRefs() = %q, want %q", got, want)
	}
	if _, err := ParseCondition(got); err != nil {
		t.Fatalf("rewritten condition does not parse: %v", err)
	}
}

func TestParseConditionRejectsMalformedExpressions(t *testing.T) {
	tests := []string{
		"", "a &&", "|| a", "a & b", "a | b", "a = true", "a ! true",
		"(a || b", "a || b)", "a ==", "a == b == c", "a..field", `a == "unterminated`,
	}
	for _, raw := range tests {
		t.Run(fmt.Sprintf("%q", raw), func(t *testing.T) {
			if _, err := ParseCondition(raw); err == nil {
				t.Fatalf("ParseCondition(%q) succeeded", raw)
			} else if raw != "" && !strings.Contains(err.Error(), "offset") && !strings.Contains(err.Error(), "maximum") {
				t.Fatalf("error %q does not identify the syntax location", err)
			}
		})
	}
}

func TestParseConditionBounds(t *testing.T) {
	allowed := strings.Repeat("(", MaxConditionDepth) + "a" + strings.Repeat(")", MaxConditionDepth)
	if _, err := ParseCondition(allowed); err != nil {
		t.Fatalf("condition at nesting limit failed: %v", err)
	}
	deep := strings.Repeat("(", MaxConditionDepth+1) + "a" + strings.Repeat(")", MaxConditionDepth+1)
	if _, err := ParseCondition(deep); err == nil || !strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("deep condition error = %v", err)
	}

	parts := make([]string, MaxConditionNodes/2+2)
	for i := range parts {
		parts[i] = fmt.Sprintf("s%d", i)
	}
	if _, err := ParseCondition(strings.Join(parts, " || ")); err == nil || !strings.Contains(err.Error(), "syntax nodes") {
		t.Fatalf("large condition error = %v", err)
	}
}
