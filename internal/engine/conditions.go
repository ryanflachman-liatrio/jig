package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// structuredField decodes a completed step's structured output once and walks
// a declared field path. Inputs and review targets share it so both read the
// same value from the same dispatch-time result cache.
func (s *scheduler) structuredField(stepID string, fields []string) (any, error) {
	state := s.states[stepID]
	if state == nil || state.Result == nil {
		return nil, fmt.Errorf("step %q has no result", stepID)
	}
	m := s.rt(stepID).structured
	if m == nil {
		if len(state.Result.Structured) == 0 {
			return nil, fmt.Errorf("step %q has no structured output", stepID)
		}
		if err := json.Unmarshal(state.Result.Structured, &m); err != nil {
			return nil, fmt.Errorf("decode structured output for step %q: %w", stepID, err)
		}
		s.rt(stepID).structured = m
	}
	var value any = m
	for _, field := range fields {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("step %q field %q is not an object", stepID, strings.Join(fields, "."))
		}
		var found bool
		value, found = object[field]
		if !found {
			return nil, fmt.Errorf("step %q has no field %q", stepID, strings.Join(fields, "."))
		}
	}
	return value, nil
}

// runGate evaluates the [step.validate] block for wfStep synchronously.
// Returns (true, detail) on pass, (false, detail) on failure.
// executionDir, when non-empty, is the dispatch snapshot used by gate commands
// and relative output checks. It is a mutation worktree for writers and a
// read-only execution view for readers.
func (s *scheduler) runGate(wfStep *workflow.Step, executionDir string) (bool, string) {
	v := wfStep.Validate

	// Command gate: run via sh -c, check exit code 0.
	if v.Command != "" {
		cmd := exec.Command("sh", "-c", v.Command)
		if executionDir != "" {
			cmd.Dir = executionDir
		}
		out, err := cmd.CombinedOutput()
		outStr := strings.TrimSpace(string(out))
		if err != nil {
			return false, fmt.Sprintf("gate command failed: %v — %s", err, outStr)
		}
		return true, "gate command passed"
	}

	// OutputExists gate: verify the step's output file was written.
	if v.OutputExists {
		outputPath, err := gateOutputPath(executionDir, wfStep.Output)
		if err != nil {
			return false, fmt.Sprintf("output_exists gate: resolve output: %v", err)
		}
		if outputPath == "" {
			return false, "output_exists gate: step has no output field"
		}
		if _, err := os.Stat(outputPath); err != nil {
			return false, fmt.Sprintf("output_exists gate: %v", err)
		}
		// Fall through to also check OutputContains if set.
	}

	// OutputContains gate: check that the output file contains a substring.
	if v.OutputContains != "" {
		outputPath, err := gateOutputPath(executionDir, wfStep.Output)
		if err != nil {
			return false, fmt.Sprintf("output_contains gate: resolve output: %v", err)
		}
		if outputPath == "" {
			return false, "output_contains gate: step has no output field"
		}
		data, err := os.ReadFile(outputPath)
		if err != nil {
			return false, fmt.Sprintf("output_contains gate: read file: %v", err)
		}
		if !strings.Contains(string(data), v.OutputContains) {
			return false, fmt.Sprintf("output_contains gate: %q not found in output", v.OutputContains)
		}
	}

	return true, "gate passed"
}

func gateOutputPath(executionDir, output string) (string, error) {
	if output == "" {
		return "", nil
	}
	return workflow.ExecutionPath(executionDir, output)
}

// evalGuard evaluates a when-guard condition against the current step results.
// The condition was syntactically and semantically validated at load time, so
// missing steps or bad field paths return false (treat as "not yet ready").
func (s *scheduler) evalGuard(cond *workflow.Condition) bool {
	if cond == nil {
		return false
	}
	return s.evalGuardExpr(cond.Root)
}

func (s *scheduler) evalGuardExpr(expr *workflow.ConditionExpr) bool {
	if expr == nil {
		return false
	}
	switch expr.Op {
	case workflow.CondAnd:
		return s.evalGuardExpr(expr.Left) && s.evalGuardExpr(expr.Right)
	case workflow.CondOr:
		return s.evalGuardExpr(expr.Left) || s.evalGuardExpr(expr.Right)
	}
	value, fieldType, scalarKind, ok := s.resolveGuardValue(expr.Ref)
	if !ok {
		return false
	}
	if expr.Op == workflow.CondTruthy {
		actual, ok := value.(bool)
		return ok && actual
	}
	return compareGuardValue(value, fieldType, scalarKind, expr.Op, expr.Literal)
}

func (s *scheduler) resolveGuardValue(ref workflow.ConditionRef) (any, workflow.FieldType, workflow.OutputKind, bool) {
	depState := s.states[ref.Step]
	if depState == nil || depState.Result == nil {
		return nil, "", "", false
	}
	producer := s.stepByID(ref.Step)
	if len(ref.Field) == 0 {
		if producer == nil {
			return nil, "", "", false
		}
		switch producer.OutputType.Kind {
		case workflow.OutputBool:
			value, err := strconv.ParseBool(depState.Result.Verdict)
			return value, "", workflow.OutputBool, err == nil
		case workflow.OutputEnum:
			return depState.Result.Verdict, "", workflow.OutputEnum, true
		default:
			return nil, "", producer.OutputType.Kind, false
		}
	}

	m := s.rt(ref.Step).structured
	if m == nil && len(depState.Result.Structured) > 0 {
		if err := json.Unmarshal(depState.Result.Structured, &m); err == nil {
			s.rt(ref.Step).structured = m
		}
	}
	var cur any = m
	for _, seg := range ref.Field {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, "", "", false
		}
		cur, ok = obj[seg]
		if !ok {
			return nil, "", "", false
		}
	}
	if producer == nil {
		return nil, "", "", false
	}
	field, ok := runtimeSchemaField(producer.ReferenceSchema(), ref.Field)
	if !ok {
		return nil, "", "", false
	}
	return cur, field.Type, "", true
}

// stepStringField reads a top-level string field from stepID's own structured
// output, populating the stepRuntime.structured cache the same way resolveGuardValue
// does. Used to surface the agent-authored `block_reason` base-schema field
// in a block_on gate without requiring a workflow-authored field reference.
func (s *scheduler) stepStringField(stepID, field string) string {
	depState := s.states[stepID]
	if depState == nil || depState.Result == nil {
		return ""
	}
	m := s.rt(stepID).structured
	if m == nil && len(depState.Result.Structured) > 0 {
		if err := json.Unmarshal(depState.Result.Structured, &m); err == nil {
			s.rt(stepID).structured = m
		}
	}
	value, _ := m[field].(string)
	return value
}

func runtimeSchemaField(schema *workflow.Schema, path []string) (*workflow.Field, bool) {
	if schema == nil {
		return nil, false
	}
	fields := schema.Fields
	var current *workflow.Field
	for _, segment := range path {
		current = nil
		for _, field := range fields {
			if field.Name == segment {
				current = field
				break
			}
		}
		if current == nil {
			return nil, false
		}
		fields = current.Fields
	}
	return current, current != nil
}

func compareGuardValue(value any, fieldType workflow.FieldType, scalarKind workflow.OutputKind, op workflow.CondOp, literal workflow.ConditionLiteral) bool {
	if scalarKind == workflow.OutputBool || fieldType == workflow.FieldBool {
		actual, ok := value.(bool)
		want, err := strconv.ParseBool(literal.Value)
		return ok && err == nil && compareOrderedBool(actual, want, op)
	}
	if scalarKind == workflow.OutputEnum || fieldType == workflow.FieldEnum || fieldType == workflow.FieldText {
		actual, ok := value.(string)
		return ok && compareOrderedString(actual, literal.Value, op)
	}
	if fieldType == workflow.FieldNumber {
		actual, ok := value.(float64)
		want, err := strconv.ParseFloat(literal.Value, 64)
		return ok && err == nil && compareOrderedNumber(actual, want, op)
	}
	if fieldType == workflow.FieldAny {
		switch actual := value.(type) {
		case string:
			if !literal.Quoted && (literal.Value == "true" || literal.Value == "false") {
				return false
			}
			if !literal.Quoted {
				if _, err := strconv.ParseFloat(literal.Value, 64); err == nil {
					return false
				}
			}
			return compareOrderedString(actual, literal.Value, op)
		case bool:
			want, err := strconv.ParseBool(literal.Value)
			return !literal.Quoted && err == nil && compareOrderedBool(actual, want, op)
		case float64:
			want, err := strconv.ParseFloat(literal.Value, 64)
			return !literal.Quoted && err == nil && compareOrderedNumber(actual, want, op)
		}
	}
	return false
}

func compareOrderedBool(actual, want bool, op workflow.CondOp) bool {
	switch op {
	case workflow.CondEq:
		return actual == want
	case workflow.CondNeq:
		return actual != want
	default:
		return false
	}
}

func compareOrderedString(actual, want string, op workflow.CondOp) bool {
	switch op {
	case workflow.CondEq:
		return actual == want
	case workflow.CondNeq:
		return actual != want
	default:
		return false
	}
}

func compareOrderedNumber(actual, want float64, op workflow.CondOp) bool {
	switch op {
	case workflow.CondEq:
		return actual == want
	case workflow.CondNeq:
		return actual != want
	case workflow.CondLT:
		return actual < want
	case workflow.CondLTE:
		return actual <= want
	case workflow.CondGT:
		return actual > want
	case workflow.CondGTE:
		return actual >= want
	default:
		return false
	}
}

// evalBlockOn evaluates the step's block_on condition against its own output.
func (s *scheduler) evalBlockOn(stepID string, wfStep *workflow.Step) bool {
	cond, err := workflow.ParseCondition(wfStep.BlockOn)
	if err != nil {
		return false
	}
	return s.evalGuard(cond)
}
