// Package chart computes and renders the workflow DAG as a top-down flowchart.
// layout.go is the pure layout pass (no lipgloss, no I/O); render.go draws it.
package chart

import (
	"sort"
	"strconv"
	"strings"

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
//     longest-path computation; crossing reduction may reorder nodes only inside
//     a rank, with the Steps index as the deterministic final tie-breaker.
//   - Step.When is validated to reference a step already in depends_on, so it
//     DECORATES that existing edge rather than adding one (chartEdge.conditional).
//   - Step.Routes' Goto targets are bounded back-edges deliberately excluded from the
//     acyclic check; it is emitted as a distinct chartBackEdge, routed upward.
//   - Step.Validate is a node gate annotation (chartNode.gate); Step.Type picks
//     the node color from theme.Step.Types at render time.
//
// The workflow's own id->index map is unexported, so this builds its own.

// chartNode is one step positioned in the layered layout.
type chartNode struct {
	id         string // step id
	index      int    // position in wf.Steps (drives within-rank order)
	typ        string // step type: agent | command | review
	rank       int    // longest-path depth over depends_on (0 = no deps)
	gate       bool   // has a [step.validate] gate
	gateLabel  string // compact description of the gate's check (see gateLabel()); not yet drawn
	retry      bool   // on_failure = "retry"
	maxRetries int    // max_retries value (0 means use engine default of 1)
	loop       *chartLoop
	foreach    *chartForEach
}

// chartForEach annotates a declared [step.foreach] family node (A8). The
// author graph stays exactly one node per family — this never expands into a
// runtime-sized chart — so maxItems is the only cardinality the static chart
// can show; the actual runtime count is only known once the run executes.
type chartForEach struct {
	maxItems int
}

// chartLoop is the bounded back-edge hanging off a step, mirrored onto its node
// so the box can advertise the loop glyph next to the type.
type chartLoop struct {
	target  string // loop.goto step id
	maxIter int
}

// chartEdge is a depends_on edge from one node to another (always lower rank to
// higher rank, since rank strictly increases along a dependency). conditional
// marks the edge the step's `when` guard decorates; label is that guard in
// compact form, rendered as a compositor layer next to the ▽ arrowhead.
type chartEdge struct {
	from, to    int // wf.Steps indices
	conditional bool
	label       string
}

// chartBackEdge is a bounded loop back-edge from the looping step back to its
// goto target (higher up the graph). Drawn as a distinct class and routed in a
// dedicated right-side channel so it never tangles with the downward DAG edges.
// label is the loop `when` guard plus the ≤N iteration bound, rendered as a
// compositor layer to the right of the ↺ glyph at the channel midpoint.
type chartBackEdge struct {
	from, to int // wf.Steps indices: loop step -> goto target
	maxIter  int
	label    string
}

// chartLayout is the full deterministic layout: nodes indexed by Steps position,
// ranks bucketing node indices top-down, and the two edge classes.
type chartLayout struct {
	nodes     []chartNode
	ranks     [][]int // ranks[r] = node indices in rank r, in deterministic chart order
	edges     []chartEdge
	backEdges []chartBackEdge
}

// layoutChart computes the layered layout for a validated workflow. It is pure:
// no I/O, no globals, no clock — so it is directly table-testable and makes the
// renderer golden-testable.
func layoutChart(wf *workflow.Workflow) chartLayout {
	steps := wf.PublicSteps()

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
			id:         s.ID,
			index:      i,
			typ:        string(s.Type),
			rank:       rank[i],
			gate:       s.Validate != nil,
			retry:      s.OnFailure == workflow.FailRetry,
			maxRetries: s.MaxRetries,
		}
		if s.Validate != nil {
			n.gateLabel = gateLabel(s.Validate)
		}
		if len(s.Routes) > 0 {
			n.loop = &chartLoop{target: s.Routes[0].Goto, maxIter: s.Routes[0].MaxIterations}
		}
		if s.ForEach != nil {
			n.foreach = &chartForEach{maxItems: s.ForEach.MaxItems}
		}
		nodes[i] = n
		if rank[i] > maxRank {
			maxRank = rank[i]
		}
	}

	// Bucket into ranks in original order. Crossing reduction starts from this
	// stable baseline and never moves a node outside its longest-path rank.
	ranks := make([][]int, maxRank+1)
	for i := range steps {
		ranks[rank[i]] = append(ranks[rank[i]], i)
	}

	// depends_on edges, plus the when-decoration.
	var edges []chartEdge
	for i := range steps {
		s := steps[i]
		conditionalSteps := map[string]bool{}
		whenLabel := ""
		if s.When != "" {
			if cond, err := workflow.ParseCondition(s.When); err == nil {
				for _, ref := range cond.ReferencedSteps() {
					conditionalSteps[ref] = true
				}
				whenLabel = condLabel(cond)
			}
		}
		for _, dep := range s.DependsOn {
			j, ok := idIndex[dep]
			if !ok {
				continue
			}
			e := chartEdge{from: j, to: i, conditional: conditionalSteps[dep]}
			if e.conditional {
				e.label = whenLabel
			}
			edges = append(edges, e)
		}
	}
	reduceCrossings(ranks, nodes, edges)

	// Route back-edges: a distinct class routed upward by the renderer.
	var back []chartBackEdge
	for i := range steps {
		for _, route := range steps[i].Routes {
			if j, ok := idIndex[route.Goto]; ok {
				be := chartBackEdge{from: i, to: j, maxIter: route.MaxIterations}
				be.label = routeLabel(route)
				back = append(back, be)
			}
		}
	}

	return chartLayout{nodes: nodes, ranks: ranks, edges: edges, backEdges: back}
}

const crossingReductionSweeps = 4

// reduceCrossings applies bounded barycentric sweeps to the forward DAG. A
// proposed rank order is retained only when it strictly reduces crossings, so
// stable inputs cannot be made worse merely because a heuristic score changed.
// Route back-edges are intentionally absent from this seam.
func reduceCrossings(ranks [][]int, nodes []chartNode, edges []chartEdge) {
	if len(edges) < 2 {
		return
	}

	for range crossingReductionSweeps {
		changed := false
		for rank := 1; rank < len(ranks); rank++ {
			changed = reorderRank(ranks, rank, nodes, edges, true) || changed
		}
		for rank := len(ranks) - 2; rank >= 0; rank-- {
			changed = reorderRank(ranks, rank, nodes, edges, false) || changed
		}
		if !changed {
			return
		}
	}
}

type rankScore struct {
	node  int
	sum   int
	count int
}

// reorderRank orders one rank by the average position of its incoming
// (downward sweep) or outgoing (upward sweep) neighbors. Integer
// cross-multiplication avoids floating-point ties; original step index resolves
// every equal score deterministically.
func reorderRank(ranks [][]int, rank int, nodes []chartNode, edges []chartEdge, incoming bool) bool {
	if len(ranks[rank]) < 2 {
		return false
	}

	positions := rankPositions(ranks)
	scores := make([]rankScore, len(ranks[rank]))
	for i, node := range ranks[rank] {
		scores[i].node = node
		for _, edge := range edges {
			neighbor := -1
			if incoming && edge.to == node {
				neighbor = edge.from
			} else if !incoming && edge.from == node {
				neighbor = edge.to
			}
			if neighbor >= 0 {
				scores[i].sum += positions[neighbor]
				scores[i].count++
			}
		}
	}

	sort.Slice(scores, func(i, j int) bool {
		a, b := scores[i], scores[j]
		switch {
		case a.count == 0 && b.count != 0:
			return false
		case a.count != 0 && b.count == 0:
			return true
		case a.count != 0 && b.count != 0:
			left, right := a.sum*b.count, b.sum*a.count
			if left != right {
				return left < right
			}
		}
		return nodes[a.node].index < nodes[b.node].index
	})

	before := countForwardCrossings(ranks, nodes, edges)
	original := append([]int(nil), ranks[rank]...)
	for i := range scores {
		ranks[rank][i] = scores[i].node
	}
	if countForwardCrossings(ranks, nodes, edges) >= before {
		copy(ranks[rank], original)
		return false
	}
	return true
}

func rankPositions(ranks [][]int) map[int]int {
	positions := make(map[int]int)
	for _, rank := range ranks {
		for position, node := range rank {
			positions[node] = position
		}
	}
	return positions
}

// countForwardCrossings counts inversions between edges spanning the same rank
// pair. Edges sharing an endpoint meet rather than cross and are excluded.
func countForwardCrossings(ranks [][]int, nodes []chartNode, edges []chartEdge) int {
	positions := rankPositions(ranks)
	crossings := 0
	for i := 0; i < len(edges); i++ {
		for j := i + 1; j < len(edges); j++ {
			a, b := edges[i], edges[j]
			if a.from == b.from || a.to == b.to ||
				nodes[a.from].rank != nodes[b.from].rank ||
				nodes[a.to].rank != nodes[b.to].rank {
				continue
			}
			fromOrder := positions[a.from] - positions[b.from]
			toOrder := positions[a.to] - positions[b.to]
			if fromOrder*toOrder < 0 {
				crossings++
			}
		}
	}
	return crossings
}

// condLabel renders a parsed guard back into a compact, readable form for the
// chart. It reconstructs from the parsed fields (not Raw) so spacing is uniform
// regardless of how the author wrote the TOML.
func condLabel(c *workflow.Condition) string {
	return c.String()
}

// loopLabel is a back-edge's caption: the re-run guard plus the ≤N iteration
// bound that makes the termination guarantee visible.
func routeLabel(l workflow.Route) string {
	var b strings.Builder
	if cond, err := workflow.ParseCondition(l.When); err == nil {
		b.WriteString(condLabel(cond))
	} else if l.When != "" {
		b.WriteString(l.When)
	}
	if l.MaxIterations > 0 {
		if b.Len() > 0 {
			b.WriteString("  ")
		}
		b.WriteString("≤" + strconv.Itoa(l.MaxIterations))
	}
	return b.String()
}

// gateLabel is a compact description of a [step.validate] gate's check, shown
// beside the gated node. Exactly one check field is set (enforced by the
// validator), so the first non-empty one wins.
func gateLabel(v *workflow.Validate) string {
	switch {
	case v.Command != "":
		return v.Command
	case v.OutputSchema != "":
		return "schema"
	case v.OutputContains != "":
		return "contains " + strconv.Quote(v.OutputContains)
	case v.OutputExists:
		return "exists"
	default:
		return ""
	}
}
