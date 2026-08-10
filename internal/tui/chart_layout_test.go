package tui

import (
	"testing"

	"jig/internal/workflow"
)

// mustDecode parses a workflow from TOML with structural-only validation (no
// baseDir, so skill dirs / scripts are not checked). It fails the test on any
// parse or validation error so the layout tests always start from a valid graph.
func mustDecode(t *testing.T, src string) *workflow.Workflow {
	t.Helper()
	wf, err := workflow.Decode(src, "")
	if err != nil {
		t.Fatalf("decode workflow: %v", err)
	}
	return wf
}

// nodeByID finds a laid-out node by step id.
func nodeByID(lay chartLayout, id string) chartNode {
	for _, n := range lay.nodes {
		if n.id == id {
			return n
		}
	}
	return chartNode{}
}

func TestLayoutChart(t *testing.T) {
	t.Run("longest-path ranks over depends_on", func(t *testing.T) {
		// a → b → d and a → d directly: d must sit at the longest path (rank 2),
		// not the shortest (the direct a→d edge).
		wf := mustDecode(t, `
[workflow]
name = "ranks"
version = "1"
[defaults]
max_parallel = 3
[[step]]
id = "a"
type = "command"
run = "x"
[[step]]
id = "b"
type = "command"
depends_on = ["a"]
run = "x"
[[step]]
id = "d"
type = "command"
depends_on = ["a", "b"]
run = "x"
`)
		lay := layoutChart(wf)
		if got := nodeByID(lay, "a").rank; got != 0 {
			t.Errorf("a rank = %d, want 0", got)
		}
		if got := nodeByID(lay, "b").rank; got != 1 {
			t.Errorf("b rank = %d, want 1", got)
		}
		if got := nodeByID(lay, "d").rank; got != 2 {
			t.Errorf("d rank = %d, want 2 (longest path, not the direct a→d edge)", got)
		}
		if len(lay.ranks) != 3 {
			t.Fatalf("ranks buckets = %d, want 3", len(lay.ranks))
		}
	})

	t.Run("within-rank order follows Steps index", func(t *testing.T) {
		// two siblings share rank 1; the bucket must list them in file order.
		wf := mustDecode(t, `
[workflow]
name = "order"
version = "1"
[defaults]
max_parallel = 3
[[step]]
id = "root"
type = "command"
run = "x"
[[step]]
id = "first"
type = "command"
depends_on = ["root"]
run = "x"
[[step]]
id = "second"
type = "command"
depends_on = ["root"]
run = "x"
`)
		lay := layoutChart(wf)
		rank1 := lay.ranks[1]
		if len(rank1) != 2 {
			t.Fatalf("rank 1 has %d nodes, want 2", len(rank1))
		}
		if lay.nodes[rank1[0]].id != "first" || lay.nodes[rank1[1]].id != "second" {
			t.Errorf("within-rank order = [%s %s], want [first second]",
				lay.nodes[rank1[0]].id, lay.nodes[rank1[1]].id)
		}
	})

	t.Run("fan-in produces one edge per parent", func(t *testing.T) {
		wf := mustDecode(t, `
[workflow]
name = "fanin"
version = "1"
[defaults]
max_parallel = 3
[[step]]
id = "l"
type = "command"
run = "x"
[[step]]
id = "r"
type = "command"
run = "x"
[[step]]
id = "join"
type = "command"
depends_on = ["l", "r"]
run = "x"
`)
		lay := layoutChart(wf)
		into := 0
		for _, e := range lay.edges {
			if lay.nodes[e.to].id == "join" {
				into++
			}
		}
		if into != 2 {
			t.Errorf("edges into join = %d, want 2", into)
		}
	})

	t.Run("when decorates the guarded edge only", func(t *testing.T) {
		// guarded depends on gate (bool) and other; only the gate→guarded edge
		// is conditional.
		wf := mustDecode(t, `
[workflow]
name = "when"
version = "1"
[defaults]
max_parallel = 3
[[step]]
id = "gate"
type = "review"
review = "diff"
output_type = "bool"
[[step]]
id = "other"
type = "command"
run = "x"
[[step]]
id = "guarded"
type = "command"
depends_on = ["gate", "other"]
when = "gate"
run = "x"
`)
		lay := layoutChart(wf)
		for _, e := range lay.edges {
			if lay.nodes[e.to].id != "guarded" {
				continue
			}
			from := lay.nodes[e.from].id
			if from == "gate" && !e.conditional {
				t.Errorf("gate→guarded edge should be conditional")
			}
			if from == "other" && e.conditional {
				t.Errorf("other→guarded edge should not be conditional")
			}
		}
	})

	t.Run("loop becomes a back-edge, not a depends_on edge", func(t *testing.T) {
		wf := mustDecode(t, `
[workflow]
name = "loop"
version = "1"
[defaults]
max_parallel = 3
[[step]]
id = "work"
type = "agent"
skill = "s"
[[step]]
id = "check"
type = "review"
depends_on = ["work"]
review = "@work.summary"
output_type = { enum = ["ok", "redo"] }
[step.loop]
when = "check == 'redo'"
goto = "work"
max_iterations = 4
`)
		lay := layoutChart(wf)
		if len(lay.backEdges) != 1 {
			t.Fatalf("back-edges = %d, want 1", len(lay.backEdges))
		}
		be := lay.backEdges[0]
		if lay.nodes[be.from].id != "check" || lay.nodes[be.to].id != "work" {
			t.Errorf("back-edge = %s→%s, want check→work",
				lay.nodes[be.from].id, lay.nodes[be.to].id)
		}
		if be.maxIter != 4 {
			t.Errorf("back-edge maxIter = %d, want 4", be.maxIter)
		}
		// The loop must not have leaked into the forward depends_on edge set as a
		// check→work edge (which would create a cycle in the layered layout).
		for _, e := range lay.edges {
			if lay.nodes[e.from].id == "check" && lay.nodes[e.to].id == "work" {
				t.Errorf("loop leaked into depends_on edges as check→work")
			}
		}
		// The node also carries the loop marker so its box can advertise it.
		if n := nodeByID(lay, "check"); n.loop == nil || n.loop.target != "work" {
			t.Errorf("check node loop marker = %+v, want target work", n.loop)
		}
	})

	t.Run("validate marks a gate node", func(t *testing.T) {
		wf := mustDecode(t, `
[workflow]
name = "gate"
version = "1"
[defaults]
max_parallel = 3
[[step]]
id = "build"
type = "command"
run = "x"
output = "out.txt"
[step.validate]
output_exists = true
`)
		lay := layoutChart(wf)
		if !nodeByID(lay, "build").gate {
			t.Errorf("build node should be marked as a gate")
		}
	})
}
