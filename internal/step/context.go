package step

import (
	"fmt"
	"strings"
)

// ContextNeighbor is one graph neighbor of a step, projected into pure data for
// the deterministic "Workflow context" preamble. The engine maps a
// workflow.Step + its scheduler State into these fields at assembly time so that
// internal/step keeps importing nothing.
type ContextNeighbor struct {
	// ID is the neighbor step's declared id.
	ID string
	// Kind is the neighbor's step type as it should read in the preamble:
	// "agent", "command", "review", or "human". The engine maps a review step
	// to "human"; KindLabel renders both "review" and "human" as "human review".
	Kind string
	// Status is the neighbor's dispatch-time status (upstream only; "" for a
	// downstream neighbor). It is retained rather than assumed "succeeded"
	// because an on_failure = "continue" dependency can be "failed" yet still
	// let this step dispatch — the preamble must be honest about that.
	Status string
	// Purpose is the neighbor's author-declared [step.context].purpose,
	// propagated in (optional). When present it annotates an upstream bullet and
	// *replaces* the derived clause on a downstream bullet. Empty means the line
	// stays graph-derived — never a guessed description.
	Purpose string
	// Conditional is the neighbor's `when` guard string (downstream only). When
	// set, the downstream bullet gains a "(conditional on `<guard>`)" clause,
	// because whether a guarded consumer actually runs is unknowable at assembly
	// time (guards evaluate later, on runtime output).
	Conditional string
	// Fields are the field name(s) of *this* step that a downstream neighbor
	// consumes, in the consumer's input-declaration order (downstream only). An
	// empty slice renders the literal "output" (a bare @thisstep reference).
	Fields []string
}

// StepContext is the typed, engine-agnostic model of one step's position in a
// workflow. Its Render method produces the deterministic preamble prepended to
// an agent's single user turn. It carries only framing — ids, statuses,
// purposes, and run state — never upstream artifact bodies; content reaches the
// step through its @ref inputs.
//
// There is deliberately no block_on field: a block_on resume overrides the SDK
// query and continues the existing conversation, so it never re-runs
// buildAgentPrompt and needs no fresh preamble (see the spec's
// "Why no block_on round").
type StepContext struct {
	WorkflowName string
	StepID       string
	// Purpose and Notes are the step's own [step.context].purpose/notes (Unit
	// 5); both optional and emitted verbatim.
	Purpose string
	Notes   string
	// Iteration is 0-indexed (a first run is 0) and MaxIterations is the firing
	// loop's max_iterations. Render displays Iteration+1 and emits the iteration
	// clause only when Iteration > 0 (a genuine re-run).
	Iteration     int
	MaxIterations int
	// RerunReason is the engine-composed, pre-punctuated State-line body for a
	// loop re-run. It is empty on a first run, which omits the State line.
	RerunReason string
	// Upstream is ordered by the step's depends_on; Downstream is ordered by
	// workflow declaration. Both orderings are fixed and no map iteration leaks
	// into the rendered output — this determinism is what the golden test locks.
	Upstream   []ContextNeighbor
	Downstream []ContextNeighbor
}

// Render produces the "Workflow context" section prepended to an agent's user
// turn. The exact byte layout is locked by the golden test in context_test.go;
// see the spec's "Exact rendering algorithm" for the authority.
//
// It returns "" for the zero value (StepID == ""), the only input that yields no
// preamble in practice — a live agent step always carries WorkflowName + StepID.
// Otherwise the string ends at the "---" delimiter with no trailing newline, so
// buildAgentPrompt appends "\n\n" before the skill body. Parts are joined with a
// single blank line ("\n\n"); an absent part contributes nothing, not even its
// blank line. Ordering is fixed and nothing iterates a map into the output, so
// the same input always renders identically (the determinism guarantee).
func (c StepContext) Render() string {
	if c.StepID == "" {
		return ""
	}

	var parts []string

	// 1. Header.
	parts = append(parts, "## Workflow context")

	// 2–4. Identity block: the position line plus the optional Purpose/Notes
	// lines, joined by single newlines so they sit on consecutive lines (no
	// blank line between them, unlike the blocks below).
	identity := []string{c.positionLine()}
	if c.Purpose != "" {
		identity = append(identity, "Purpose: "+c.Purpose)
	}
	if c.Notes != "" {
		identity = append(identity, "Notes: "+c.Notes)
	}
	parts = append(parts, strings.Join(identity, "\n"))

	// 5. Upstream block (only when there are upstream neighbors).
	if len(c.Upstream) > 0 {
		lines := []string{"Upstream (already complete):"}
		for _, n := range c.Upstream {
			lines = append(lines, n.upstreamBullet())
		}
		lines = append(lines, "These reach you as the inputs listed below; this section is orientation only.")
		parts = append(parts, strings.Join(lines, "\n"))
	}

	// 6. Downstream block (only when there are downstream consumers).
	if len(c.Downstream) > 0 {
		lines := []string{"Downstream (what your output feeds):"}
		for _, n := range c.Downstream {
			lines = append(lines, n.downstreamBullet())
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}

	// 7. State line (only for a loop re-run; RerunReason is pre-punctuated).
	if c.RerunReason != "" {
		parts = append(parts, "State: "+c.RerunReason)
	}

	// 8. Delimiter (separates jig-owned framing from the skill body).
	parts = append(parts, "---")

	return strings.Join(parts, "\n\n")
}

// positionLine renders "You are step `x` in workflow `y`" with an optional
// "(iteration N of M)" clause. Iteration is 0-indexed at the source but
// 1-indexed for display, so the first re-run (Iteration == 1) reads
// "iteration 2 of M"; a first run (Iteration == 0) omits the clause.
func (c StepContext) positionLine() string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are step `%s` in workflow `%s`", c.StepID, c.WorkflowName)
	if c.Iteration > 0 {
		fmt.Fprintf(&b, " (iteration %d of %d)", c.Iteration+1, c.MaxIterations)
	}
	b.WriteByte('.')
	return b.String()
}

// upstreamBullet renders "- `id` (status)" plus " — purpose" only when the
// neighbor declares a purpose.
func (n ContextNeighbor) upstreamBullet() string {
	s := fmt.Sprintf("- `%s` (%s)", n.ID, n.Status)
	if n.Purpose != "" {
		s += " — " + n.Purpose
	}
	return s
}

// downstreamBullet renders "- `id` (KindLabel) — clause". The clause is the
// neighbor's declared purpose verbatim when present (it replaces the derived
// clause), otherwise the graph-derived clause; a guarded consumer gains a
// trailing "(conditional on `<guard>`)".
func (n ContextNeighbor) downstreamBullet() string {
	clause := n.Purpose
	if clause == "" {
		clause = n.derivedClause()
	}
	if n.Conditional != "" {
		clause += fmt.Sprintf(" (conditional on `%s`)", n.Conditional)
	}
	return fmt.Sprintf("- `%s` (%s) — %s", n.ID, n.KindLabel(), clause)
}

// KindLabel maps a neighbor Kind to its preamble label: "human review" for a
// review/human neighbor, otherwise the kind verbatim ("agent"/"command").
func (n ContextNeighbor) KindLabel() string {
	switch n.Kind {
	case "review", "human":
		return "human review"
	default:
		return n.Kind
	}
}

// derivedClause is the graph-derived downstream clause used when the neighbor
// declares no purpose: "a person reviews your <fields>" for a review/human
// consumer, else "consumes your <fields>". <fields> is the backticked field
// name(s) in consumer order, or the literal "output" (no backticks) for a bare
// @thisstep reference with no field.
func (n ContextNeighbor) derivedClause() string {
	fields := "output"
	if len(n.Fields) > 0 {
		quoted := make([]string, len(n.Fields))
		for i, f := range n.Fields {
			quoted[i] = "`" + f + "`"
		}
		fields = strings.Join(quoted, ", ")
	}
	switch n.Kind {
	case "review", "human":
		return "a person reviews your " + fields
	default:
		return "consumes your " + fields
	}
}
