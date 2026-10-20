package engine

import (
	"reflect"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// diamondPlusIsland is a -> {b, c} -> d, with e disconnected.
func diamondPlusIsland() *workflow.Workflow {
	return &workflow.Workflow{Steps: []workflow.Step{
		{ID: "a"},
		{ID: "b", DependsOn: []string{"a"}},
		{ID: "c", DependsOn: []string{"a"}},
		{ID: "d", DependsOn: []string{"b", "c"}},
		{ID: "e"},
	}}
}

func TestStepGraphWalks(t *testing.T) {
	g := newStepGraph(diamondPlusIsland())
	cases := []struct {
		name string
		got  []string
		want []string
	}{
		{"descendants of root", g.inOrder(g.descendants(nil, "a")), []string{"a", "b", "c", "d"}},
		{"descendants of branch", g.inOrder(g.descendants(nil, "b")), []string{"b", "d"}},
		{"descendants of island", g.inOrder(g.descendants(nil, "e")), []string{"e"}},
		{"ancestors of join", g.inOrder(g.ancestors("d")), []string{"a", "b", "c", "d"}},
		{"filtered walk stops at c", g.inOrder(g.descendants(func(_, to string) bool { return to != "c" }, "a")), []string{"a", "b", "d"}},
		{"multiple seeds", g.inOrder(g.descendants(nil, "c", "e")), []string{"c", "d", "e"}},
	}
	for _, tc := range cases {
		if !reflect.DeepEqual(tc.got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

func TestResetClosureDiamondIsDeclarationOrdered(t *testing.T) {
	wf := diamondPlusIsland()
	for i := 0; i < 20; i++ { // map-order bugs show up across repeats
		if got := ResetClosure(wf, "a"); !reflect.DeepEqual(got, []string{"a", "b", "c", "d"}) {
			t.Fatalf("ResetClosure(a) = %v", got)
		}
	}
	if got := ResetClosure(wf, "missing"); got != nil {
		t.Fatalf("ResetClosure(missing) = %v, want nil", got)
	}
}
