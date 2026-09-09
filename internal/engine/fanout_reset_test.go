package engine

// Tests for scheduler-level reset and bounded-route rewind over [step.foreach]
// families (task 19 of docs/plans/a8-dynamic-foreach-fan-out.md, Phase 3 — see
// engine.go's expandFanOutClosure/handleReset/rewindPlan changes).

import (
	"context"
	"encoding/json"
	"errors"
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

// fanOutMutatingExec is an Executor for reset/route tests that need real git
// commits: a step id present in producer returns that scripted structured
// payload (round-robin across repeated dispatches, so a producer can change
// across generations — proving "changed list cardinality across
// generations"); every other request with a worktree writes one file whose
// content is stamped with that step id's own invocation count, so every
// re-run (reset or route) produces a genuinely distinct commit.
type fanOutMutatingExec struct {
	mu       sync.Mutex
	producer map[string][]json.RawMessage
	counts   map[string]int
}

func (e *fanOutMutatingExec) Execute(_ context.Context, req StepRequest, _ Reporter) (*step.Result, error) {
	e.mu.Lock()
	if e.counts == nil {
		e.counts = make(map[string]int)
	}
	n := e.counts[req.Step.ID]
	e.counts[req.Step.ID]++
	e.mu.Unlock()

	if seq, ok := e.producer[req.Step.ID]; ok {
		idx := n
		if idx >= len(seq) {
			idx = len(seq) - 1
		}
		return &step.Result{Status: step.StatusSucceeded, Structured: seq[idx]}, nil
	}
	if req.Worktree != "" {
		// One file per step id (never a fixed "out.txt") so two children writing
		// concurrently in their own worktrees never collide when squash-merged
		// into the shared run branch.
		name := sanitizeBranchName(req.Step.ID) + ".txt"
		content := []byte(req.Step.ID + "-run-" + itoa(n+1))
		if err := os.WriteFile(filepath.Join(req.Worktree, name), content, 0o644); err != nil {
			return nil, err
		}
	}
	return &step.Result{Status: step.StatusSucceeded}, nil
}

const foreachResetWorkflow = `
[workflow]
name = "foreach-reset"
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
isolation = "worktree"
  [step.foreach]
  items = "@discover.targets"
  as = "target"
  max_items = 8
  [step.schema]
  finding = "text"

[[step]]
id = "solo"
type = "command"
run = "true"
isolation = "worktree"

[[step]]
id = "gate"
type = "review"
depends_on = ["analyze"]
output_type = "bool"
  [[step.review]]
  source = "diff"
  label = "Changes"
`

// TestForEachReset_FamilyRemovesChildCommitsAndReExpands proves that resetting
// the family itself removes every current child's integration commit,
// replays the independent survivor ("solo") in original run-branch order,
// bumps the family's Generation (not Iteration), and re-expands fresh
// children on the next dependency-ready pass — while the prior generation's
// manifest remains on disk as untouched history.
func TestForEachReset_FamilyRemovesChildCommitsAndReExpands(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	initRepo(t, repo)

	wf, err := workflow.Decode(foreachResetWorkflow, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &fanOutMutatingExec{producer: map[string][]json.RawMessage{
		"discover": {discoverStructured(target("a", "a"), target("b", "b"))},
	}}
	root := filepath.Join(repo, ".jig")
	mgr := NewManager(exec, root)
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	waitForReviewRequest(t, ch, "gate", 10*time.Second)

	runBranch := runBranchName("foreach-reset", run.ID)
	preCommits, err := stepCommitsFromLog(repo, runBranch)
	if err != nil {
		t.Fatalf("stepCommitsFromLog pre-reset: %v", err)
	}
	// solo + 2 children = 3 commits (analyze itself, and the review "gate", are
	// zero-commit steps).
	if len(preCommits) != 3 {
		t.Fatalf("pre-reset commits = %v, want 3 (solo + 2 children)", preCommits)
	}
	soloPreSHA := preCommits["solo"]
	if soloPreSHA == "" {
		t.Fatal("solo has no pre-reset commit")
	}
	var preChildIDs []string
	for id := range preCommits {
		if strings.Contains(id, workflow.ForEachIDMarker) {
			preChildIDs = append(preChildIDs, id)
		}
	}
	if len(preChildIDs) != 2 {
		t.Fatalf("pre-reset child commits = %v, want 2", preChildIDs)
	}
	runDir := filepath.Join(root, "runs", run.ID)
	oldManifestPath := datastore.FanOutManifestPath(runDir, "analyze", 0, 0)
	if _, err := os.Stat(oldManifestPath); err != nil {
		t.Fatalf("generation-0 manifest missing before reset: %v", err)
	}

	result, err := run.Reset("analyze")
	if err != nil {
		t.Fatalf("Reset(analyze): %v", err)
	}
	for _, id := range preChildIDs {
		if !containsID(result.Closure, id) {
			t.Errorf("Reset result.Closure = %v, want it to include current child %q", result.Closure, id)
		}
	}

	// Second round: family re-expands (new generation, fresh coordinates),
	// children re-run, gate parks again.
	waitForReviewRequest(t, ch, "gate", 10*time.Second)

	snap := run.Snapshot()
	var famGen, famIter int
	found := false
	for _, st := range snap.Steps {
		if st.ID == "analyze" {
			famGen, famIter, found = st.Generation, st.Iteration, true
		}
	}
	if !found {
		t.Fatal("no analyze step in post-reset snapshot")
	}
	if famGen != 1 {
		t.Errorf("post-reset analyze Generation = %d, want 1 (manual reset bumps generation)", famGen)
	}
	if famIter != 0 {
		t.Errorf("post-reset analyze Iteration = %d, want 0 (reset starts a fresh generation at iteration 0)", famIter)
	}

	postCommits, err := stepCommitsFromLog(repo, runBranch)
	if err != nil {
		t.Fatalf("stepCommitsFromLog post-reset: %v", err)
	}
	if len(postCommits) != 3 {
		t.Fatalf("post-reset commits = %v, want 3 (solo survivor + 2 fresh children)", postCommits)
	}
	if postCommits["solo"] == "" {
		t.Error("solo has no post-reset commit — an independent commit outside the family's closure must survive the reset")
	}
	for _, id := range preChildIDs {
		if sha, ok := postCommits[id]; ok {
			t.Errorf("old child %q still has a commit (%s) after family reset — its commit must be removed", id, sha)
		}
	}
	var postChildIDs []string
	for id := range postCommits {
		if strings.Contains(id, workflow.ForEachIDMarker) {
			postChildIDs = append(postChildIDs, id)
			if !strings.Contains(id, ".g001.") {
				t.Errorf("post-reset child id %q does not carry the new generation coordinate g001", id)
			}
		}
	}
	if len(postChildIDs) != 2 {
		t.Fatalf("post-reset child commits = %v, want 2 fresh children", postChildIDs)
	}

	// The old generation's manifest is immutable history: still present, still
	// exactly what it was, even though the family has moved on to generation 1.
	if _, err := os.Stat(oldManifestPath); err != nil {
		t.Errorf("generation-0 manifest was removed by reset, want it kept as history: %v", err)
	}
	newManifestPath := datastore.FanOutManifestPath(runDir, "analyze", 1, 0)
	if _, err := os.Stat(newManifestPath); err != nil {
		t.Errorf("generation-1 manifest missing after re-expansion: %v", err)
	}

	run.Cancel()
	run.Wait()
}

// TestForEachReset_RejectsDirectChildReset proves an operator cannot reset one
// runtime child in isolation — only the family, which replaces the whole
// current expansion atomically.
func TestForEachReset_RejectsDirectChildReset(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	initRepo(t, repo)

	wf, err := workflow.Decode(foreachResetWorkflow, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &fanOutMutatingExec{producer: map[string][]json.RawMessage{
		"discover": {discoverStructured(target("a", "a"), target("b", "b"))},
	}}
	root := filepath.Join(repo, ".jig")
	mgr := NewManager(exec, root)
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	waitForReviewRequest(t, ch, "gate", 10*time.Second)

	var childID string
	for _, st := range run.Snapshot().Steps {
		if st.ParentID == "analyze" {
			childID = st.ID
			break
		}
	}
	if childID == "" {
		t.Fatal("no fan-out child found in snapshot")
	}

	_, resetErr := run.Reset(childID)
	var typed *ResetError
	if !errors.As(resetErr, &typed) || typed.Code != "fanout_child" {
		t.Fatalf("Reset(child) = %v, want a fanout_child ResetError", resetErr)
	}

	// The guard must be a pure no-op: nothing about the run's state changes.
	snap := run.Snapshot()
	for _, st := range snap.Steps {
		if st.ID == childID && st.Status != step.StatusSucceeded {
			t.Errorf("child %q status = %s after rejected reset, want unchanged Succeeded", childID, st.Status)
		}
	}
	run.Cancel()
	run.Wait()
}

// TestForEachReset_UpstreamProducerResetChangesCardinality proves resetting
// the family's upstream producer re-expands with whatever list the producer
// returns the second time — a different item count is a legitimate new
// generation, not an error.
func TestForEachReset_UpstreamProducerResetChangesCardinality(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	initRepo(t, repo)

	wf, err := workflow.Decode(foreachResetWorkflow, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &fanOutMutatingExec{producer: map[string][]json.RawMessage{
		"discover": {
			discoverStructured(target("a", "a"), target("b", "b")),
			discoverStructured(target("a", "a"), target("b", "b"), target("c", "c")),
		},
	}}
	root := filepath.Join(repo, ".jig")
	mgr := NewManager(exec, root)
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	waitForReviewRequest(t, ch, "gate", 10*time.Second)
	if agg := aggregateOf(t, run, "analyze"); agg.Count != 2 {
		t.Fatalf("first-generation aggregate Count = %d, want 2", agg.Count)
	}

	// Reset "discover": its closure transitively includes the family (and
	// gate), so the family's current children are removed too even though the
	// operator targeted the producer, not the family.
	if _, err := run.Reset("discover"); err != nil {
		t.Fatalf("Reset(discover): %v", err)
	}
	waitForReviewRequest(t, ch, "gate", 10*time.Second)

	agg := aggregateOf(t, run, "analyze")
	if agg.Count != 3 {
		t.Fatalf("second-generation aggregate Count = %d, want 3 (producer returned a longer list)", agg.Count)
	}
	if !agg.AllSucceeded {
		t.Fatalf("second-generation aggregate = %+v, want all succeeded", agg)
	}
	run.Cancel()
	run.Wait()
}

// TestForEachRoute_RewindReExpandsFamilyWithBumpedIteration proves a bounded
// route rewind through the family (not an operator reset) keeps Generation
// fixed and bumps Iteration instead, producing a fresh expansion with the
// route-rewind coordinate baked into every new child id.
func TestForEachRoute_RewindReExpandsFamilyWithBumpedIteration(t *testing.T) {
	const toml = `
[workflow]
name = "foreach-route"
version = "1"

[[step]]
id = "discover"
type = "agent"
skill = "skills/discover"
  [step.schema]
  targets = { list = { name = "text", path = "text" } }

[[step]]
id = "ready"
type = "command"
output_type = "bool"
run = "true"

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
id = "quality"
type = "check"
depends_on = ["ready", "analyze"]
applies_when = "ready"
output_type = { enum = ["pass", "fail", "skip", "error"] }
run = "true"
  [step.findings]
  schema_version = 1
  file = "quality.json"
  required_tools = ["sh"]

[[step.route]]
when = "quality != 'pass'"
goto = "analyze"
max_iterations = 2
`
	wf, err := workflow.Decode(toml, "")
	if err != nil {
		t.Fatal(err)
	}
	exec := &routeCheckExec{fanOutExec: &fanOutExec{producer: map[string]json.RawMessage{
		"discover": discoverStructured(target("a", "a"), target("b", "b")),
	}}}
	mgr := NewManager(exec, "")
	_, ch := mgr.Subscribe()
	run, err := mgr.Start(wf)
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, ch, 5*time.Second)
	if rf, ok := events[len(events)-1].(RunFinished); !ok || rf.Failed {
		t.Fatalf("last event = %+v, want a successful RunFinished (route recovers to pass on round 2)", events[len(events)-1])
	}

	var famGen, famIter int
	found := false
	for _, st := range run.Snapshot().Steps {
		if st.ID == "analyze" {
			famGen, famIter, found = st.Generation, st.Iteration, true
		}
	}
	if !found {
		t.Fatal("no analyze step in snapshot")
	}
	if famGen != 0 {
		t.Errorf("Generation = %d after a route rewind, want 0 (route rewinds must not bump Generation)", famGen)
	}
	if famIter != 1 {
		t.Errorf("Iteration = %d after one route rewind, want 1", famIter)
	}

	agg := aggregateOf(t, run, "analyze")
	if agg.Count != 2 || !agg.AllSucceeded {
		t.Fatalf("final aggregate = %+v, want 2 succeeded children from the last (post-rewind) expansion", agg)
	}
	for _, id := range childIDs(agg) {
		if !strings.Contains(id, ".r001.") {
			t.Errorf("final child id %q does not carry the route-rewind iteration coordinate r001", id)
		}
	}
	if exec.qualityRuns() < 2 {
		t.Fatalf("quality ran %d times, want at least 2 (fail, then pass after remediation)", exec.qualityRuns())
	}
}

// routeCheckExec wraps fanOutExec, special-casing a "quality" check step to
// fail once and pass thereafter, so its route back to the foreach family
// fires exactly once.
type routeCheckExec struct {
	*fanOutExec
	mu      sync.Mutex
	quality int
}

func (e *routeCheckExec) Execute(ctx context.Context, req StepRequest, rep Reporter) (*step.Result, error) {
	if req.Step.ID == "ready" {
		return &step.Result{Status: step.StatusSucceeded, Verdict: "true"}, nil
	}
	if req.Step.ID == "quality" {
		e.mu.Lock()
		e.quality++
		n := e.quality
		e.mu.Unlock()
		verdict := "fail"
		if n > 1 {
			verdict = "pass"
		}
		return &step.Result{Status: step.StatusSucceeded, Verdict: verdict}, nil
	}
	return e.fanOutExec.Execute(ctx, req, rep)
}

func (e *routeCheckExec) qualityRuns() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.quality
}

func containsID(list []string, target string) bool {
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
}
