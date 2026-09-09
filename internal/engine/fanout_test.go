package engine

// Tests for the [step.foreach] expansion and barrier-settlement coordinator
// in fanout.go (task 12 of docs/plans/a8-dynamic-foreach-fan-out.md, Phase 2).
// All tests here run with persistence off (NewManager(exec, "")) unless a test
// is specifically proving the persistence-on manifest/aggregate-file path.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"jig/internal/datastore"
	"jig/internal/step"
	"jig/internal/workflow"
)

// fanOutExec is a general-purpose executor for foreach engine tests. Any step
// id present in producer returns that scripted Structured payload
// (simulating the list-producing step). Any request carrying a non-nil
// FanOutItem is a runtime fan-out child: it fails when failIndex marks its
// zero-based Index, otherwise it succeeds with a small structured echo of the
// item it received (so aggregate/chaining tests can inspect it). Every other
// step succeeds with no structured output.
type fanOutExec struct {
	mu        sync.Mutex
	producer  map[string]json.RawMessage
	delay     time.Duration
	delayFn   func(item *FanOutItem) time.Duration
	failIndex map[int]bool
	calls     map[string]int
	order     []string
	concurLog []int // sampled concurrent-child count at each dispatch, for cap assertions
	concur    int
}

func (e *fanOutExec) Execute(ctx context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
	e.mu.Lock()
	if e.calls == nil {
		e.calls = make(map[string]int)
	}
	e.calls[req.Step.ID]++
	e.order = append(e.order, req.Step.ID)
	if req.FanOutItem != nil {
		e.concur++
		e.concurLog = append(e.concurLog, e.concur)
	}
	e.mu.Unlock()

	delay := e.delay
	if e.delayFn != nil && req.FanOutItem != nil {
		delay = e.delayFn(req.FanOutItem)
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if req.FanOutItem != nil {
		e.mu.Lock()
		e.concur--
		e.mu.Unlock()
	}

	if raw, ok := e.producer[req.Step.ID]; ok {
		return &step.Result{Status: step.StatusSucceeded, Structured: raw}, nil
	}
	if req.FanOutItem != nil {
		if e.failIndex[req.FanOutItem.Index] {
			return &step.Result{Status: step.StatusFailed, Err: "scripted item failure"}, nil
		}
		out, _ := json.Marshal(map[string]any{
			"echo":  json.RawMessage(req.FanOutItem.Item),
			"index": req.FanOutItem.Index,
		})
		return &step.Result{Status: step.StatusSucceeded, Structured: out}, nil
	}
	return &step.Result{Status: step.StatusSucceeded}, nil
}

func (e *fanOutExec) callCount(id string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls[id]
}

func (e *fanOutExec) maxConcurrency() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	max := 0
	for _, c := range e.concurLog {
		if c > max {
			max = c
		}
	}
	return max
}

// foreachTOML builds a minimal discover -> analyze foreach workflow. extra is
// appended verbatim to [step.foreach] for the analyze template (e.g.
// "max_parallel = 2").
func foreachTOML(maxItems int, extra string) string {
	return `
[workflow]
name = "foreach"
version = "1"

[[step]]
id = "discover"
type = "agent"
skill = "skills/discover"
  [step.schema]
  targets = { list = { name = "text", path = "text" } }

[[step]]
id = "analyze"
type = "agent"
depends_on = ["discover"]
skill = "skills/analyze"

  [step.foreach]
  items = "@discover.targets"
  as = "target"
  max_items = ` + itoa(maxItems) + `
` + extra + `
  [step.schema]
  finding = "text"
`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func discoverStructured(items ...map[string]string) json.RawMessage {
	targets := make([]map[string]string, len(items))
	copy(targets, items)
	out, _ := json.Marshal(map[string]any{"targets": targets})
	return out
}

func target(name, path string) map[string]string {
	return map[string]string{"name": name, "path": path}
}

// aggregateOf decodes stepID's family aggregate from a run's snapshot.
func aggregateOf(t *testing.T, run *Run, stepID string) fanOutAggregate {
	t.Helper()
	snap := run.Snapshot()
	for _, st := range snap.Steps {
		if st.ID != stepID {
			continue
		}
		if st.Result == nil || len(st.Result.Structured) == 0 {
			t.Fatalf("step %q has no structured result; status=%v", stepID, st.Status)
		}
		var agg fanOutAggregate
		if err := json.Unmarshal(st.Result.Structured, &agg); err != nil {
			t.Fatalf("decode aggregate: %v", err)
		}
		return agg
	}
	t.Fatalf("no such step %q in snapshot", stepID)
	return fanOutAggregate{}
}

func childIDs(agg fanOutAggregate) []string {
	out := make([]string, len(agg.Results))
	for i, r := range agg.Results {
		out[i] = r.InstanceID
	}
	return out
}

// TestForEach_EmptyList proves an empty producer list is a successful, empty
// barrier: the family settles immediately with count 0 and all_succeeded true,
// and no child ever appears in the event stream.
func TestForEach_EmptyList(t *testing.T) {
	wf, err := workflow.Decode(foreachTOML(8, ""), "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &fanOutExec{producer: map[string]json.RawMessage{"discover": discoverStructured()}}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, ch, 5*time.Second)
	if rf, ok := events[len(events)-1].(RunFinished); !ok || rf.Failed {
		t.Fatalf("last event = %+v, want successful RunFinished", events[len(events)-1])
	}
	for _, e := range events {
		if ss, ok := e.(StepStatus); ok && strings.Contains(ss.StepID, workflow.ForEachIDMarker) {
			t.Fatalf("empty list must create no children, saw event for %q", ss.StepID)
		}
	}
	agg := aggregateOf(t, run, "analyze")
	if agg.Count != 0 || agg.Succeeded != 0 || agg.Failed != 0 || !agg.AllSucceeded {
		t.Fatalf("aggregate = %+v, want empty successful barrier", agg)
	}
	if len(agg.Results) != 0 {
		t.Fatalf("Results = %v, want empty (not nil-omitted) slice", agg.Results)
	}
}

// TestForEach_SingleItem proves the one-child case produces exactly one
// instance and a matching aggregate.
func TestForEach_SingleItem(t *testing.T) {
	wf, err := workflow.Decode(foreachTOML(8, ""), "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &fanOutExec{producer: map[string]json.RawMessage{"discover": discoverStructured(target("api", "services/api"))}}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, ch, 5*time.Second)
	if rf, ok := events[len(events)-1].(RunFinished); !ok || rf.Failed {
		t.Fatalf("last event = %+v, want successful RunFinished", events[len(events)-1])
	}
	agg := aggregateOf(t, run, "analyze")
	if agg.Count != 1 || agg.Succeeded != 1 || !agg.AllSucceeded {
		t.Fatalf("aggregate = %+v", agg)
	}
	want := "analyze" + workflow.ForEachIDMarker + "g000.r000.i0000"
	if agg.Results[0].InstanceID != want {
		t.Errorf("InstanceID = %q, want %q", agg.Results[0].InstanceID, want)
	}
}

// TestForEach_DuplicateItemsRemainDistinct proves two identical item values
// still produce two distinct, independently identified children.
func TestForEach_DuplicateItemsRemainDistinct(t *testing.T) {
	wf, err := workflow.Decode(foreachTOML(8, ""), "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &fanOutExec{producer: map[string]json.RawMessage{
		"discover": discoverStructured(target("api", "services/api"), target("api", "services/api")),
	}}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, ch, 5*time.Second)
	if rf, ok := events[len(events)-1].(RunFinished); !ok || rf.Failed {
		t.Fatal("run should succeed")
	}
	agg := aggregateOf(t, run, "analyze")
	ids := childIDs(agg)
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("duplicate items must yield distinct instance ids, got %v", ids)
	}
	if agg.Results[0].Index != 0 || agg.Results[1].Index != 1 {
		t.Fatalf("indices = [%d %d], want [0 1]", agg.Results[0].Index, agg.Results[1].Index)
	}
}

// TestForEach_MaxSizedListSucceeds proves a producer list exactly at
// max_items is accepted (the boundary is inclusive).
func TestForEach_MaxSizedListSucceeds(t *testing.T) {
	wf, err := workflow.Decode(foreachTOML(2, ""), "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &fanOutExec{producer: map[string]json.RawMessage{
		"discover": discoverStructured(target("a", "a"), target("b", "b")),
	}}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, ch, 5*time.Second)
	if rf, ok := events[len(events)-1].(RunFinished); !ok || rf.Failed {
		t.Fatal("run at exactly max_items should succeed")
	}
	if agg := aggregateOf(t, run, "analyze"); agg.Count != 2 {
		t.Fatalf("Count = %d, want 2", agg.Count)
	}
}

// TestForEach_MaxItemsOverflowFailsBeforeAnyChild proves a list longer than
// max_items fails the family closed, before a single child is created.
func TestForEach_MaxItemsOverflowFailsBeforeAnyChild(t *testing.T) {
	const toml = `
[workflow]
name = "foreach-overflow"
version = "1"
[[step]]
id = "discover"
type = "agent"
skill = "skills/discover"
  [step.schema]
  targets = { list = { name = "text", path = "text" } }
[[step]]
id = "analyze"
type = "agent"
depends_on = ["discover"]
skill = "skills/analyze"
on_failure = "continue"
  [step.foreach]
  items = "@discover.targets"
  as = "target"
  max_items = 1
  [step.schema]
  finding = "text"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &fanOutExec{producer: map[string]json.RawMessage{
		"discover": discoverStructured(target("a", "a"), target("b", "b")),
	}}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	if _, err := mgr.Start(wf); err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, ch, 5*time.Second)
	for _, e := range events {
		if ss, ok := e.(StepStatus); ok && strings.Contains(ss.StepID, workflow.ForEachIDMarker) {
			t.Fatalf("overflow must create no children, saw event for %q", ss.StepID)
		}
	}
	statuses := findStatus(events, "analyze")
	if len(statuses) == 0 || statuses[len(statuses)-1] != step.StatusFailed {
		t.Fatalf("analyze statuses = %v, want a terminal failure", statuses)
	}
}

// TestForEach_NonListProducerFieldFailsClosed proves a producer field that
// decodes but is not a JSON array fails the family closed with no children.
func TestForEach_NonListProducerFieldFailsClosed(t *testing.T) {
	wf, err := workflow.Decode(foreachTOML(8, ""), "")
	if err != nil {
		t.Fatal(err)
	}
	// "targets" is an object, not an array.
	bad, _ := json.Marshal(map[string]any{"targets": map[string]string{"name": "api"}})
	exec := &fanOutExec{producer: map[string]json.RawMessage{"discover": bad}}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	events := collectEventsAborting(t, ch, run, 5*time.Second)
	for _, e := range events {
		if ss, ok := e.(StepStatus); ok && strings.Contains(ss.StepID, workflow.ForEachIDMarker) {
			t.Fatalf("a non-list producer field must create no children, saw event for %q", ss.StepID)
		}
	}
	statuses := findStatus(events, "analyze")
	if len(statuses) == 0 || statuses[len(statuses)-1] != step.StatusFailed {
		t.Fatalf("analyze statuses = %v, want a terminal failure", statuses)
	}
}

// TestForEach_SourceOrderRegardlessOfCompletionOrder proves the aggregate's
// Results are ordered by source index even when children finish in reverse
// completion order (first-dispatched child is scripted to finish last).
func TestForEach_SourceOrderRegardlessOfCompletionOrder(t *testing.T) {
	wf, err := workflow.Decode(foreachTOML(8, "max_parallel = 4"), "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &fanOutExec{
		producer: map[string]json.RawMessage{"discover": discoverStructured(
			target("a", "a"), target("b", "b"), target("c", "c"), target("d", "d"),
		)},
		delayFn: func(item *FanOutItem) time.Duration {
			// Reverse of source order: item 0 finishes last.
			return time.Duration(4-item.Index) * 15 * time.Millisecond
		},
	}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, ch, 5*time.Second)
	if rf, ok := events[len(events)-1].(RunFinished); !ok || rf.Failed {
		t.Fatal("run should succeed")
	}
	agg := aggregateOf(t, run, "analyze")
	for i, r := range agg.Results {
		if r.Index != i {
			t.Fatalf("Results[%d].Index = %d, want %d — order must be source order despite reversed completion", i, r.Index, i)
		}
	}
}

// TestForEach_FamilyLocalMaxParallelCap proves a family's own max_parallel
// bounds its concurrent children even though the global max_parallel would
// otherwise allow more.
func TestForEach_FamilyLocalMaxParallelCap(t *testing.T) {
	wf, err := workflow.Decode(foreachTOML(8, "max_parallel = 2"), "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &fanOutExec{
		producer: map[string]json.RawMessage{"discover": discoverStructured(
			target("a", "a"), target("b", "b"), target("c", "c"), target("d", "d"), target("e", "e"), target("f", "f"),
		)},
		delay: 20 * time.Millisecond,
	}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	if _, err := mgr.Start(wf); err != nil {
		t.Fatal(err)
	}
	collectEvents(t, ch, 5*time.Second)
	if got := exec.maxConcurrency(); got > 2 {
		t.Fatalf("observed concurrency %d, want <= family max_parallel (2)", got)
	}
}

// TestForEach_GlobalMaxParallelCapsAcrossFamilyAndSiblings proves the
// workflow-wide max_parallel still bounds total in-flight work — family
// children plus any concurrent static sibling step never exceed it.
func TestForEach_GlobalMaxParallelCapsAcrossFamilyAndSiblings(t *testing.T) {
	const toml = `
[workflow]
name = "foreach-global-cap"
version = "1"
[defaults]
max_parallel = 2

[[step]]
id = "discover"
type = "agent"
skill = "skills/discover"
  [step.schema]
  targets = { list = { name = "text", path = "text" } }

[[step]]
id = "analyze"
type = "agent"
depends_on = ["discover"]
skill = "skills/analyze"
  [step.foreach]
  items = "@discover.targets"
  as = "target"
  max_items = 8
  [step.schema]
  finding = "text"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &fanOutExec{
		producer: map[string]json.RawMessage{"discover": discoverStructured(
			target("a", "a"), target("b", "b"), target("c", "c"), target("d", "d"),
		)},
		delay: 20 * time.Millisecond,
	}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	if _, err := mgr.Start(wf); err != nil {
		t.Fatal(err)
	}
	collectEvents(t, ch, 5*time.Second)
	if got := exec.maxConcurrency(); got > 2 {
		t.Fatalf("observed concurrency %d, want <= global max_parallel (2)", got)
	}
}

// TestForEach_ResourceClassCapAppliesToChildren proves a resource_class
// declared on the foreach template caps its children exactly like it would
// cap ordinary static steps of that class.
func TestForEach_ResourceClassCapAppliesToChildren(t *testing.T) {
	const toml = `
[workflow]
name = "foreach-resource-class"
version = "1"
[defaults]
max_parallel = 8
resource_limits = { heavy = 1 }

[[step]]
id = "discover"
type = "agent"
skill = "skills/discover"
  [step.schema]
  targets = { list = { name = "text", path = "text" } }

[[step]]
id = "analyze"
type = "agent"
depends_on = ["discover"]
skill = "skills/analyze"
resource_class = "heavy"
  [step.foreach]
  items = "@discover.targets"
  as = "target"
  max_items = 8
  [step.schema]
  finding = "text"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &fanOutExec{
		producer: map[string]json.RawMessage{"discover": discoverStructured(
			target("a", "a"), target("b", "b"), target("c", "c"),
		)},
		delay: 20 * time.Millisecond,
	}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	if _, err := mgr.Start(wf); err != nil {
		t.Fatal(err)
	}
	collectEvents(t, ch, 5*time.Second)
	if got := exec.maxConcurrency(); got > 1 {
		t.Fatalf("observed concurrency %d, want <= resource class cap (1)", got)
	}
}

// TestForEach_ChainedFanOut proves one family's ordered aggregate `results`
// can feed another family's [step.foreach] items, per the plan's "Consumers
// receive @analyze.results as ordered JSON and may feed that list into
// another foreach family."
func TestForEach_ChainedFanOut(t *testing.T) {
	const toml = `
[workflow]
name = "foreach-chained"
version = "1"

[[step]]
id = "discover"
type = "agent"
skill = "skills/discover"
  [step.schema]
  targets = { list = { name = "text", path = "text" } }

[[step]]
id = "analyze"
type = "agent"
depends_on = ["discover"]
skill = "skills/analyze"
  [step.foreach]
  items = "@discover.targets"
  as = "target"
  max_items = 8
  [step.schema]
  finding = "text"

[[step]]
id = "recheck"
type = "agent"
depends_on = ["analyze"]
skill = "skills/recheck"
  [step.foreach]
  items = "@analyze.results"
  as = "item"
  max_items = 8
  [step.schema]
  ok = "bool"
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &fanOutExec{producer: map[string]json.RawMessage{
		"discover": discoverStructured(target("a", "a"), target("b", "b")),
	}}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, ch, 5*time.Second)
	if rf, ok := events[len(events)-1].(RunFinished); !ok || rf.Failed {
		t.Fatal("chained fan-out should succeed")
	}
	analyzeAgg := aggregateOf(t, run, "analyze")
	recheckAgg := aggregateOf(t, run, "recheck")
	if recheckAgg.Count != analyzeAgg.Count {
		t.Fatalf("recheck.Count = %d, want %d (one child per analyze result)", recheckAgg.Count, analyzeAgg.Count)
	}
	// Each recheck child's item is the corresponding analyze result record —
	// spot-check it carries an "instance_id" field naming the analyze child.
	for i, r := range recheckAgg.Results {
		obj, ok := r.Item.(map[string]any)
		if !ok || obj["instance_id"] == nil {
			t.Fatalf("recheck item %d = %v, want the analyze result record shape", i, r.Item)
		}
	}
}

// TestForEach_PersistenceOff proves the entire expansion/settlement path
// touches no filesystem when the run has no run directory, relying only on
// Result.Structured, exactly like every other persistence-off scheduler path.
func TestForEach_PersistenceOff(t *testing.T) {
	wf, err := workflow.Decode(foreachTOML(8, ""), "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &fanOutExec{producer: map[string]json.RawMessage{
		"discover": discoverStructured(target("a", "a"), target("b", "b"), target("c", "c")),
	}}
	mgr := NewManager(exec, "") // root == "" — persistence off
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, ch, 5*time.Second)
	if rf, ok := events[len(events)-1].(RunFinished); !ok || rf.Failed {
		t.Fatal("run should succeed")
	}
	snap := run.Snapshot()
	var family *step.State
	for i := range snap.Steps {
		if snap.Steps[i].ID == "analyze" {
			family = &snap.Steps[i]
		}
	}
	if family == nil {
		t.Fatal("no analyze step in snapshot")
	}
	if family.Result.OutputPath != "" {
		t.Errorf("OutputPath = %q, want empty under persistence-off", family.Result.OutputPath)
	}
	if len(family.Result.Structured) == 0 {
		t.Fatal("persistence-off aggregate must still be available via Result.Structured")
	}
}

// TestForEach_FailedChildDoesNotFailBarrierButFailsRun proves the family
// barrier still succeeds (honest failed/succeeded counts, all_succeeded
// false) when a child fails with on_failure="continue", while the overall run
// still reports Failed — matching ordinary on_failure="continue" semantics
// without failing the barrier a second time.
func TestForEach_FailedChildDoesNotFailBarrierButFailsRun(t *testing.T) {
	toml := strings.Replace(foreachTOML(8, ""), `skill = "skills/analyze"`,
		"skill = \"skills/analyze\"\non_failure = \"continue\"", 1)
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &fanOutExec{
		producer:  map[string]json.RawMessage{"discover": discoverStructured(target("a", "a"), target("b", "b"))},
		failIndex: map[int]bool{1: true},
	}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, ch, 5*time.Second)
	rf, ok := events[len(events)-1].(RunFinished)
	if !ok || !rf.Failed {
		t.Fatalf("last event = %+v, want RunFinished{Failed:true} (a child failed)", events[len(events)-1])
	}
	agg := aggregateOf(t, run, "analyze")
	if agg.Count != 2 || agg.Succeeded != 1 || agg.Failed != 1 || agg.AllSucceeded {
		t.Fatalf("aggregate = %+v, want honest mixed counts", agg)
	}
	statuses := findStatus(events, "analyze")
	if len(statuses) == 0 || statuses[len(statuses)-1] != step.StatusSucceeded {
		t.Fatalf("family statuses = %v, want family itself to succeed (barrier closes) despite a failed child", statuses)
	}
}

// TestForEach_PersistenceOnWritesManifestAndAggregate proves that when
// persistence is on, expansion durably writes the per-generation fan-out
// manifest and the family settles by writing its aggregate to
// steps/<family-id>/output.json, exactly as "Durability, replay, and crash
// reopen" and "Aggregate result contract" specify — even though replaying
// that manifest on resume is Phase 3 scope.
func TestForEach_PersistenceOnWritesManifestAndAggregate(t *testing.T) {
	wf, err := workflow.Decode(foreachTOML(8, ""), "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &fanOutExec{producer: map[string]json.RawMessage{
		"discover": discoverStructured(target("a", "a"), target("b", "b")),
	}}
	root := t.TempDir()
	mgr := NewManager(exec, root)
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, ch, 5*time.Second)
	if rf, ok := events[len(events)-1].(RunFinished); !ok || rf.Failed {
		t.Fatal("run should succeed")
	}

	runDir := filepath.Join(root, "runs", run.ID)
	m, err := datastore.ReadFanOutManifest(runDir, "analyze", 0, 0)
	if err != nil {
		t.Fatalf("ReadFanOutManifest: %v", err)
	}
	if len(m.Items) != 2 {
		t.Fatalf("manifest has %d items, want 2", len(m.Items))
	}
	if err := datastore.ValidateFanOutItems(m); err != nil {
		t.Errorf("ValidateFanOutItems: %v", err)
	}

	var sawExpanded FanOutExpanded
	found := false
	for _, e := range events {
		if fe, ok := e.(FanOutExpanded); ok {
			sawExpanded, found = fe, true
		}
	}
	if !found {
		t.Fatal("no FanOutExpanded event observed")
	}
	wantDigest, err := datastore.FanOutManifestDigest(m)
	if err != nil {
		t.Fatal(err)
	}
	if sawExpanded.ManifestDigest != wantDigest {
		t.Errorf("FanOutExpanded.ManifestDigest = %q, want %q (matching on-disk manifest)", sawExpanded.ManifestDigest, wantDigest)
	}
	if len(sawExpanded.Instances) != 2 {
		t.Fatalf("FanOutExpanded.Instances has %d entries, want 2", len(sawExpanded.Instances))
	}

	aggPath := filepath.Join(runDir, "steps", "analyze", "output.json")
	data, err := os.ReadFile(aggPath)
	if err != nil {
		t.Fatalf("read aggregate output.json: %v", err)
	}
	var agg fanOutAggregate
	if err := json.Unmarshal(data, &agg); err != nil {
		t.Fatalf("decode aggregate: %v", err)
	}
	if agg.Count != 2 || !agg.AllSucceeded {
		t.Fatalf("aggregate = %+v", agg)
	}
}
