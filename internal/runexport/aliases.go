package runexport

import (
	"fmt"
	"sort"
	"time"
)

// aliasTable allocates and remembers the fixed run-1/workflow-1 identities,
// per-step aliases, and scoped tool-call aliases used throughout an archive.
// Original-to-alias maps live only in memory (spec FR-05) — nothing here is
// ever serialized as-is into an exported member.
type aliasTable struct {
	steps      map[string]string // original step id -> "step-0001"
	stepOrder  []string          // allocation order, for deterministic iteration
	tools      map[string]string // scope key + original tool id -> "tool-0001"
	toolOrder  map[string][]string
	nextStep   int
	nextToolID map[string]int
}

func newAliasTable() *aliasTable {
	return &aliasTable{
		steps:      make(map[string]string),
		tools:      make(map[string]string),
		toolOrder:  make(map[string][]string),
		nextToolID: make(map[string]int),
	}
}

const (
	runAlias      = "run-1"
	workflowAlias = "workflow-1"
)

// allocateStep returns id's alias, allocating a new one on first sight.
func (a *aliasTable) allocateStep(id string) string {
	if alias, ok := a.steps[id]; ok {
		return alias
	}
	a.nextStep++
	alias := fmt.Sprintf("step-%04d", a.nextStep)
	a.steps[id] = alias
	a.stepOrder = append(a.stepOrder, id)
	return alias
}

// stepAlias looks up an already-allocated alias without creating one. It
// returns "" when id is unknown — callers use this to omit a reference to a
// step outside the allocated set rather than inventing an alias for it.
func (a *aliasTable) stepAlias(id string) string {
	return a.steps[id]
}

// allocateTool returns the tool alias for id within scope, allocating a new
// one on first occurrence within that scope. Scope keys the alias space by
// step/generation/iteration/attempt so the same tool-call id in two different
// steps never collides.
func (a *aliasTable) allocateTool(scope, id string) string {
	key := scope + "\x00" + id
	if alias, ok := a.tools[key]; ok {
		return alias
	}
	a.nextToolID[scope]++
	alias := fmt.Sprintf("tool-%04d", a.nextToolID[scope])
	a.tools[key] = alias
	a.toolOrder[scope] = append(a.toolOrder[scope], id)
	return alias
}

// seedStepOrder allocates aliases for ids in order, skipping any already
// allocated. Used to apply the prescribed allocation precedence: declared
// RunStarted.Steps order, then first journal occurrence, then sorted
// direct-directory discovery (spec FR-05).
func (a *aliasTable) seedStepOrder(ids []string) {
	for _, id := range ids {
		a.allocateStep(id)
	}
}

// seedDiscoveredSteps allocates aliases for any step directory names not
// already seen, in sorted order, satisfying the final allocation tier.
func (a *aliasTable) seedDiscoveredSteps(names []string) {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	for _, name := range sorted {
		a.allocateStep(name)
	}
}

// timeAnchor resolves relative-millisecond offsets from a single anchor time
// (spec FR-05): the first valid accepted journal timestamp, or the earliest
// valid retained transcript timestamp when the journal has none. Signed
// offsets preserve clock reversal instead of clamping to zero.
type timeAnchor struct {
	anchor time.Time
	set    bool
}

// establish fixes the anchor to ts if not already set. Callers must call this
// in evidence order (journal first, transcript fallback) so the anchor is the
// *first* accepted timestamp rather than the minimum of all timestamps.
func (t *timeAnchor) establish(ts time.Time) {
	if !t.set && !ts.IsZero() {
		t.anchor = ts
		t.set = true
	}
}

// relativeMS returns the signed millisecond offset of ts from the anchor, or
// nil when ts is zero (absent) or no anchor could be established.
func (t *timeAnchor) relativeMS(ts time.Time) *int64 {
	if !t.set || ts.IsZero() {
		return nil
	}
	ms := ts.Sub(t.anchor).Milliseconds()
	return &ms
}
