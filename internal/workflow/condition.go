package workflow

import (
	"fmt"
	"strings"
)

// CondOp is the comparison in a guard expression.
type CondOp string

const (
	CondTruthy CondOp = "truthy" // bare `stepid`, true when the bool verdict is true
	CondEq     CondOp = "=="
	CondNeq    CondOp = "!="
)

// Condition is a parsed `when` / `loop.when` guard. The grammar is intentionally
// tiny so guards stay statically analyzable:
//
//	when = "validate == 'valid'"       # scalar output_type verdict
//	when = "review != 'approve'"
//	when = "is_valid"                  # bare bool verdict
//	when = "research.status == 'done'" # a field of a producer step's schema
//
// The left-hand side is a step id, optionally followed by a dotted field path
// selecting into that step's structured (schema) output. An empty Field tests
// the step's scalar output_type verdict.
type Condition struct {
	Raw   string
	Step  string
	Field []string
	Op    CondOp
	Value string // for == / !=; unquoted
}

// ParseCondition parses a guard expression. It validates only the shape here;
// whether Step exists and Value is legal for that step's output type is checked
// by the validator, which has the whole workflow in hand.
func ParseCondition(raw string) (*Condition, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, fmt.Errorf("empty condition")
	}

	for _, op := range []CondOp{CondEq, CondNeq} {
		lhs, rhs, found := strings.Cut(s, string(op))
		if !found {
			continue
		}
		step, field, err := parseCondRef(strings.TrimSpace(lhs))
		if err != nil {
			return nil, fmt.Errorf("left of %s: %w", op, err)
		}
		val, err := unquote(strings.TrimSpace(rhs))
		if err != nil {
			return nil, err
		}
		if val == "" {
			return nil, fmt.Errorf("right of %s must be a non-empty value", op)
		}
		return &Condition{Raw: raw, Step: step, Field: field, Op: op, Value: val}, nil
	}

	// No operator: a bare reference testing a bool verdict/field.
	step, field, err := parseCondRef(s)
	if err != nil {
		return nil, fmt.Errorf("expected `id`, `id.field`, `id == 'value'`, or `id != 'value'`, got %q", raw)
	}
	return &Condition{Raw: raw, Step: step, Field: field, Op: CondTruthy}, nil
}

// parseCondRef splits a guard's left-hand side into a step id and an optional
// dotted field path, requiring each segment to be a well-formed identifier.
func parseCondRef(s string) (step string, field []string, err error) {
	step, field = parseRef(s)
	if !isIdent(step) {
		return "", nil, fmt.Errorf("%q is not a step id", s)
	}
	for _, seg := range field {
		if !isIdent(seg) {
			return "", nil, fmt.Errorf("%q has an invalid field segment %q", s, seg)
		}
	}
	return step, field, nil
}

// unquote strips a single pair of matching single or double quotes. A bare
// (unquoted) word is returned as-is so `true`/`false` also work.
func unquote(s string) (string, error) {
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1], nil
		}
	}
	if strings.ContainsAny(s, "'\"") {
		return "", fmt.Errorf("mismatched quotes in %q", s)
	}
	return s, nil
}

// isIdent reports whether s is a plausible step id: non-empty, made of letters,
// digits, `_` or `-`.
func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}
