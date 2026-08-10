package tui

import (
	"jig/internal/workflow"
)

// chart_layout.go turns a validated *workflow.Workflow into a pure, deterministic
// layered layout the renderer draws. It is deliberately free of any lipgloss or
// terminal concern so it can be unit-tested in isolation and golden-tested via
// the renderer: same workflow in, same layout out, every time.
//
// GRAPH MODEL (see internal/workflow/schema.go + validate.go):
//   - Step.DependsOn is the only edge set laid out. validate.checkAcyclic proves
//     it is a DAG, and Steps is in deterministic file order, so ranks are a
//     longest-path computation and within-rank order is just the Steps index.
//   - Step.When is validated to reference a step already in depends_on, so it
//     DECORATES that existing edge rather than adding one (chartEdge.conditional).
//   - Step.Loop.Goto is a bounded back-edge deliberately excluded from the
//     acyclic check; it is emitted as a distinct chartBackEdge, routed upward.
//   - Step.Validate is a node gate annotation (chartNode.gate); Step.Type picks
//     the node color from theme.Step.Types at render time.
//
// The workflow's own id->index map is unexported, so this builds its own.

// chartNode is one step positioned in the layered layout.
type chartNode struct {
	id    string // step id
	index int    // position in wf.Steps (drives within-rank order)
	typ   string // step type: agent | command | review
	rank  int    // longest-path depth over depends_on (0 = no deps)
	gate  bool   // has a [step.validate] gate
	loop  *chartLoop
}

// chartLoop is the bounded back-edge hanging off a step, mirrored onto its node
// so the box can advertise the loop glyph next to the type.
type chartLoop struct {
	target  string // loop.goto step id
	maxIter int
}

// chartEdge is a depends_on edge from one node to another (always lower rank to
// higher rank, since rank strictly increases along a dependency). conditional
// marks the edge the step's `when` guard decorates.
type chartEdge struct {
	from, to    int // wf.Steps indices
	conditional bool
}

// chartBackEdge is a bounded loop back-edge from the looping step back to its
// goto target (higher up the graph). Drawn as a distinct class and routed in a
// dedicated right-side channel so it never tangles with the downward DAG edges.
type chartBackEdge struct {
	from, to int // wf.Steps indices: loop step -> goto target
	maxIter  int
}

// chartLayout is the full deterministic layout: nodes indexed by Steps position,
// ranks bucketing node indices top-down, and the two edge classes.
type chartLayout struct {
	nodes     []chartNode
	ranks     [][]int // ranks[r] = node indices in rank r, ordered by Steps index
	edges     []chartEdge
	backEdges []chartBackEdge
}

// layoutChart computes the layered layout for a validated workflow. It is pure:
// no I/O, no globals, no clock — so it is directly table-testable and makes the
// renderer golden-testable.
func layoutChart(wf *workflow.Workflow) chartLayout {
	steps := wf.Steps

	// The workflow's index map is unexported; build our own id->position map.
	idIndex := make(map[string]int, len(steps))
	for i := range steps {
		idIndex[steps[i].ID] = i
	}

	// Longest-path rank over depends_on, memoized. The graph is a proven DAG
	// (loops are excluded from depends_on), so the recursion always terminates.
	rank := make([]int, len(steps))
	for i := range rank {
		rank[i] = -1
	}
	var rankOf func(i int) int
	rankOf = func(i int) int {
		if rank[i] >= 0 {
			return rank[i]
		}
		r := 0
		for _, dep := range steps[i].DependsOn {
			if j, ok := idIndex[dep]; ok {
				if v := rankOf(j) + 1; v > r {
					r = v
				}
			}
		}
		rank[i] = r
		return r
	}
	for i := range steps {
		rankOf(i)
	}

	nodes := make([]chartNode, len(steps))
	maxRank := 0
	for i := range steps {
		s := steps[i]
		n := chartNode{
			id:    s.ID,
			index: i,
			typ:   string(s.Type),
			rank:  rank[i],
			gate:  s.Validate != nil,
		}
		if s.Loop != nil {
			n.loop = &chartLoop{target: s.Loop.Goto, maxIter: s.Loop.MaxIterations}
		}
		nodes[i] = n
		if rank[i] > maxRank {
			maxRank = rank[i]
		}
	}

	// Bucket into ranks. Iterating i ascending appends in Steps order, so each
	// rank is deterministically ordered by original index (MVP order; no
	// crossing-minimization).
	ranks := make([][]int, maxRank+1)
	for i := range steps {
		ranks[rank[i]] = append(ranks[rank[i]], i)
	}

	// depends_on edges, plus the when-decoration. A guard is validated to
	// reference a step in depends_on, so at most one incoming edge is marked.
	var edges []chartEdge
	for i := range steps {
		s := steps[i]
		whenStep := ""
		if s.When != "" {
			if cond, err := workflow.ParseCondition(s.When); err == nil {
				whenStep = cond.Step
			}
		}
		for _, dep := range s.DependsOn {
			j, ok := idIndex[dep]
			if !ok {
				continue
			}
			edges = append(edges, chartEdge{from: j, to: i, conditional: dep == whenStep})
		}
	}

	// Loop back-edges: a distinct class routed upward by the renderer.
	var back []chartBackEdge
	for i := range steps {
		if steps[i].Loop == nil {
			continue
		}
		if j, ok := idIndex[steps[i].Loop.Goto]; ok {
			back = append(back, chartBackEdge{from: i, to: j, maxIter: steps[i].Loop.MaxIterations})
		}
	}

	return chartLayout{nodes: nodes, ranks: ranks, edges: edges, backEdges: back}
}
