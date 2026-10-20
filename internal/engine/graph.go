package engine

import "github.com/ryanflachman-liatrio/jig/internal/workflow"

// stepGraph is a workflow's static depends_on DAG with each step's dependents
// precomputed, so reachability walks visit each node and edge once (O(V+E))
// instead of rescanning every step until a fixed point. Edges point from a
// dependency to its dependents. Runtime fan-out children are not part of it.
type stepGraph struct {
	order      []string            // step ids in declaration order
	deps       map[string][]string // id → its depends_on
	dependents map[string][]string // id → steps that depend on it, in declaration order
}

func newStepGraph(wf *workflow.Workflow) stepGraph {
	g := stepGraph{
		order:      make([]string, 0, len(wf.Steps)),
		deps:       make(map[string][]string, len(wf.Steps)),
		dependents: make(map[string][]string, len(wf.Steps)),
	}
	for i := range wf.Steps {
		st := &wf.Steps[i]
		g.order = append(g.order, st.ID)
		g.deps[st.ID] = st.DependsOn
		for _, dep := range st.DependsOn {
			g.dependents[dep] = append(g.dependents[dep], st.ID)
		}
	}
	return g
}

// reach returns seeds plus every node reachable from them along edges that
// follow accepts. next yields a node's neighbors in the walk direction.
func reach(seeds []string, next func(string) []string, follow func(from, to string) bool) map[string]bool {
	seen := make(map[string]bool, len(seeds))
	queue := append([]string(nil), seeds...)
	for _, id := range seeds {
		seen[id] = true
	}
	for len(queue) > 0 {
		from := queue[0]
		queue = queue[1:]
		for _, to := range next(from) {
			if seen[to] || (follow != nil && !follow(from, to)) {
				continue
			}
			seen[to] = true
			queue = append(queue, to)
		}
	}
	return seen
}

// descendants returns seeds plus everything that transitively depends on
// them. follow, when non-nil, filters which dependency edges propagate.
func (g stepGraph) descendants(follow func(dep, dependent string) bool, seeds ...string) map[string]bool {
	return reach(seeds, func(id string) []string { return g.dependents[id] }, follow)
}

// ancestors returns seeds plus everything they transitively depend on.
func (g stepGraph) ancestors(seeds ...string) map[string]bool {
	return reach(seeds, func(id string) []string { return g.deps[id] }, nil)
}

// inOrder lists the members of set in declaration order.
func (g stepGraph) inOrder(set map[string]bool) []string {
	var out []string
	for _, id := range g.order {
		if set[id] {
			out = append(out, id)
		}
	}
	return out
}
