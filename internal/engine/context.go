package engine

import (
	"slices"
	"strings"

	"jig/internal/step"
	"jig/internal/workflow"
)

// buildStepContext assembles the deterministic StepContext for an agent step
// from the static DAG (s.wf) and the scheduler's dispatch-time states. It is the
// engine-side projection of workflow/step data into internal/step's pure model,
// so that package keeps importing nothing.
//
// Ordering is fixed and load-bearing for the golden tests: upstream follows the
// step's depends_on order, downstream follows workflow declaration order, and
// nothing iterates a map into the result — so the rendered preamble is
// deterministic. This function populates only topology; run-state framing
// (iteration / rerun) is layered on by Unit 3.
func (s *scheduler) buildStepContext(st *workflow.Step) step.StepContext {
	ctx := step.StepContext{
		WorkflowName: s.wf.Meta.Name,
		StepID:       st.ID,
	}

	// Upstream = this step's depends_on, in declared order, each tagged with its
	// dispatch-time status. The status token is retained (not assumed
	// "succeeded") because depsReady admits a failed dep that declared
	// on_failure = "continue" (see depsReady), so an agent can legitimately see a
	// failed upstream and the preamble must be honest about it.
	for _, depID := range st.DependsOn {
		n := step.ContextNeighbor{ID: depID}
		if dep := s.stepByID(depID); dep != nil {
			n.Kind = string(dep.Type)
		}
		if ds := s.states[depID]; ds != nil {
			n.Status = string(ds.Status)
		}
		ctx.Upstream = append(ctx.Upstream, n)
	}

	// Downstream = every step that lists this one in depends_on, in workflow
	// declaration order. The data edge and the ordering edge are the same set:
	// the validator forces any @ref input to also appear in depends_on, so
	// ranging declaration order is both complete and deterministic.
	for i := range s.wf.Steps {
		consumer := &s.wf.Steps[i]
		if !slices.Contains(consumer.DependsOn, st.ID) {
			continue
		}
		n := step.ContextNeighbor{
			ID:     consumer.ID,
			Kind:   neighborKind(consumer.Type),
			Fields: consumedFields(consumer, st.ID),
		}
		// A guarded consumer may be when-skipped at runtime on output we cannot
		// see at assembly, so record the static guard and let the renderer note
		// the edge is conditional rather than overstate that it always runs.
		if consumer.When != "" {
			n.Conditional = consumer.When
		}
		ctx.Downstream = append(ctx.Downstream, n)
	}

	return ctx
}

// neighborKind maps a downstream consumer's step type to the neighbor Kind the
// renderer expects: a review step reads as a human ("human review" after
// KindLabel); agent/command pass through unchanged.
func neighborKind(t workflow.StepType) string {
	if t == workflow.StepReview {
		return "human"
	}
	return string(t)
}

// consumedFields returns the field name(s) of producerID that consumer reads, in
// the consumer's declaration order: from its @producerID.field inputs, plus —
// for a review consumer — its review = "@producerID.field" target. A bare
// reference (no field) contributes nothing, leaving the slice empty so the
// renderer emits the literal "output".
func consumedFields(consumer *workflow.Step, producerID string) []string {
	var fields []string
	for _, in := range consumer.Inputs {
		if in.Ref == producerID && len(in.RefField) > 0 {
			fields = append(fields, strings.Join(in.RefField, "."))
		}
	}
	if consumer.Type == workflow.StepReview {
		if tgt, path := parseReviewRef(consumer.Review); tgt == producerID && len(path) > 0 {
			fields = append(fields, strings.Join(path, "."))
		}
	}
	return fields
}

// parseReviewRef splits a review string like "@plan.summary" into the target
// step id and field path. It mirrors workflow.parseRef (unexported there) and
// returns an empty id for the non-ref "diff" and a nil path for a bare "@plan".
func parseReviewRef(review string) (stepID string, fields []string) {
	ref, ok := strings.CutPrefix(review, "@")
	if !ok {
		return "", nil
	}
	head, rest, found := strings.Cut(ref, ".")
	if !found {
		return head, nil
	}
	return head, strings.Split(rest, ".")
}
