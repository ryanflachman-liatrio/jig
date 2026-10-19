package datastore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCreate_CreatesLayout(t *testing.T) {
	root := t.TempDir()
	runID := "20260730-120000-abcdef01"

	got, err := Open(root).Create(runID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Run must address <root>/runs/<runID>.
	want := filepath.Join(root, "runs", runID)
	if got.Dir() != want {
		t.Errorf("Create dir: want %q, got %q", want, got.Dir())
	}

	// steps/ and artifacts/ subdirectories must exist.
	for _, sub := range []string{"steps", "artifacts"} {
		p := filepath.Join(got.Dir(), sub)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected subdirectory %q to exist: %v", p, err)
		}
	}
	if got.Artifacts() != filepath.Join(want, "artifacts") {
		t.Errorf("Artifacts = %q", got.Artifacts())
	}
}

func TestResolve(t *testing.T) {
	root := t.TempDir()
	runID := "20260908-120000-safe1234"
	want, err := Open(root).Create(runID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Open(root).Resolve(runID)
	if err != nil || got != want {
		t.Fatalf("Resolve = %q, %v; want %q", got.Dir(), err, want.Dir())
	}
	for _, bad := range invalidIDs(filepath.Join(root, "runs", runID)) {
		if _, err := Open(root).Resolve(bad); err == nil {
			t.Errorf("Resolve(%q) succeeded", bad)
		}
	}
	if _, err := Open(root).Resolve("missing"); err == nil {
		t.Error("missing run succeeded")
	}
	if _, err := (Store{}).Resolve(runID); err == nil {
		t.Error("disabled store succeeded")
	}
}

func TestResolveRejectsSymlinkAndFile(t *testing.T) {
	root := t.TempDir()
	runs := filepath.Join(root, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root).Resolve("file"); err == nil {
		t.Error("file target succeeded")
	}
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(runs, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root).Resolve("link"); err == nil {
		t.Error("symlink target succeeded")
	}
}

func TestCreate_DisabledStoreReturnsError(t *testing.T) {
	if _, err := (Store{}).Create("any-id"); err == nil {
		t.Error("expected error for disabled store, got nil")
	}
}

func TestCreate_Idempotent(t *testing.T) {
	store := Open(t.TempDir())
	// Calling twice should not error.
	if _, err := store.Create("idempotent-run"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := store.Create("idempotent-run"); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

func TestLocate(t *testing.T) {
	root := t.TempDir()
	got, err := Open(root).Locate("not-created")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "runs", "not-created"); got.Dir() != want {
		t.Errorf("Locate = %q, want %q", got.Dir(), want)
	}
	if _, err := os.Stat(got.Dir()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Locate created the run: %v", err)
	}
	if got, err := (Store{}).Locate("x"); err != nil || got.Enabled() {
		t.Errorf("disabled Locate = %+v, %v; want disabled run, nil", got, err)
	}
}

func TestList(t *testing.T) {
	root := t.TempDir()
	store := Open(root)
	// Create run dirs out of order; List must return them sorted ascending
	// (chronological, since run IDs are timestamp-prefixed).
	for _, id := range []string{"20260731-121054-c", "20260731-114852-a", "20260731-121049-b"} {
		if _, err := store.Create(id); err != nil {
			t.Fatalf("Create %q: %v", id, err)
		}
	}
	// A stray file in runs/ must be ignored — only directories are runs.
	if err := os.WriteFile(filepath.Join(root, "runs", "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"20260731-114852-a", "20260731-121049-b", "20260731-121054-c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List: want %v, got %v", want, got)
	}
}

func TestList_MissingAndDisabled(t *testing.T) {
	// No runs/ directory yet (fresh repo): nil, no error.
	got, err := Open(t.TempDir()).List()
	if err != nil {
		t.Fatalf("List missing runs dir: %v", err)
	}
	if got != nil {
		t.Errorf("missing runs dir: want nil, got %v", got)
	}
	// Persistence off: nil, no error — same as "no runs".
	got, err = Store{}.List()
	if err != nil {
		t.Fatalf("List disabled: %v", err)
	}
	if got != nil {
		t.Errorf("disabled: want nil, got %v", got)
	}
}

func TestDelete(t *testing.T) {
	root := t.TempDir()
	store := Open(root)
	run, err := store.Create("doomed")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("doomed"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(run.Dir()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("run still exists after Delete: %v", err)
	}
	if err := store.Delete("never-existed"); err != nil {
		t.Errorf("Delete missing run: %v", err)
	}
	if err := (Store{}).Delete("x"); err != nil {
		t.Errorf("disabled Delete: %v", err)
	}
}

// TestDeleteRejectsEscapingIDs guards the RemoveAll: no id may widen the
// removal beyond runs/.
func TestDeleteRejectsEscapingIDs(t *testing.T) {
	root := t.TempDir()
	sentinel := filepath.Join(root, "keep")
	if err := os.WriteFile(sentinel, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, bad := range invalidIDs(sentinel) {
		if err := Open(root).Delete(bad); err == nil {
			t.Errorf("Delete(%q) succeeded", bad)
		}
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("Delete escaped runs/: %v", err)
	}
}

func TestStepEnsure(t *testing.T) {
	run, err := Open(t.TempDir()).Create("run1")
	if err != nil {
		t.Fatal(err)
	}

	st := run.Step("my-step")
	if err := st.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	want := filepath.Join(run.Dir(), "steps", "my-step")
	if st.Dir() != want {
		t.Errorf("Step dir: want %q, got %q", want, st.Dir())
	}
	if _, err := os.Stat(st.Dir()); err != nil {
		t.Errorf("step dir does not exist: %v", err)
	}
}

func TestPathAccessors(t *testing.T) {
	run := RunAt("/some/run/dir")
	st := run.Step("my-step")
	round := st.Review("r1")
	stepDir := filepath.Join("/some/run/dir", "steps", "my-step")
	roundDir := filepath.Join(stepDir, "review", "r1")
	cases := map[string][2]string{
		"Journal":              {run.Journal(), filepath.Join("/some/run/dir", "journal.jsonl")},
		"Findings":             {run.Findings(), filepath.Join("/some/run/dir", "findings.jsonl")},
		"SecurityMonitorState": {run.SecurityMonitorState(), filepath.Join("/some/run/dir", "security-monitor-state.json")},
		"WorkflowSnapshot":     {run.WorkflowSnapshot(), filepath.Join("/some/run/dir", "workflow.json")},
		"SchedulerLock":        {run.SchedulerLock(), filepath.Join("/some/run/dir", "scheduler.lock")},
		"Input":                {st.Input(), filepath.Join(stepDir, "input.md")},
		"Result":               {st.Result(), filepath.Join(stepDir, "result.json")},
		"Output":               {st.Output(), filepath.Join(stepDir, "output.md")},
		"OutputJSON":           {st.OutputJSON(), filepath.Join(stepDir, "output.json")},
		"Transcript":           {st.Transcript(), filepath.Join(stepDir, "transcript.jsonl")},
		"Security":             {st.Security(), filepath.Join(stepDir, "security.md")},
		"Session":              {st.Session(), filepath.Join(stepDir, "session.json")},
		"Review.Dir":           {round.Dir(), roundDir},
		"Review.Draft":         {round.Draft(), filepath.Join(roundDir, "draft.json")},
		"Review.Submission":    {round.Submission(), filepath.Join(roundDir, "submission.json")},
		"Review.Feedback":      {round.Feedback(), filepath.Join(roundDir, "feedback.md")},
		"Review.Documents":     {round.Documents(), filepath.Join(roundDir, "documents")},
	}
	for name, c := range cases {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", name, c[0], c[1])
		}
	}
}

func TestClearOutputsRemovesInputArtifact(t *testing.T) {
	run, err := Open(t.TempDir()).Create("run1")
	if err != nil {
		t.Fatal(err)
	}
	st := run.Step("my-step")
	if err := st.Ensure(); err != nil {
		t.Fatal(err)
	}
	path := st.Input()
	if err := os.WriteFile(path, []byte("assembled prompt"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := st.ClearOutputs(); err != nil {
		t.Fatalf("ClearOutputs: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("input.md still exists after reset: %v", err)
	}
}

// TestDisabledLocationsYieldEmptyPaths pins the persistence-off contract: no
// accessor may turn a disabled location into a path relative to the working
// directory.
func TestDisabledLocationsYieldEmptyPaths(t *testing.T) {
	run := Run{}
	st := run.Step("s")
	round := st.Review("1")
	for name, got := range map[string]string{
		"Run.Dir":                  run.Dir(),
		"Run.Artifacts":            run.Artifacts(),
		"Run.Journal":              run.Journal(),
		"Run.Findings":             run.Findings(),
		"Run.SecurityMonitorState": run.SecurityMonitorState(),
		"Run.WorkflowSnapshot":     run.WorkflowSnapshot(),
		"Run.SchedulerLock":        run.SchedulerLock(),
		"Step.Dir":                 st.Dir(),
		"Step.Input":               st.Input(),
		"Step.Result":              st.Result(),
		"Step.Output":              st.Output(),
		"Step.OutputJSON":          st.OutputJSON(),
		"Step.Transcript":          st.Transcript(),
		"Step.Security":            st.Security(),
		"Step.Session":             st.Session(),
		"Step.FanOutManifest":      st.FanOutManifest(0, 0),
		"Review.Dir":               round.Dir(),
		"Review.Draft":             round.Draft(),
		"Review.Submission":        round.Submission(),
		"Review.Feedback":          round.Feedback(),
		"Review.Documents":         round.Documents(),
	} {
		if got != "" {
			t.Errorf("%s on disabled location = %q, want \"\"", name, got)
		}
	}
	if err := st.Ensure(); err != nil {
		t.Errorf("disabled Ensure: %v", err)
	}
	if err := st.ClearOutputs(); err != nil {
		t.Errorf("disabled ClearOutputs: %v", err)
	}
}

// TestInvalidIDsDisablePathsAndFailMutations pins the id-safety contract: an
// invalid step or round id yields no path, and every mutation and read
// reports the validation error rather than silently doing nothing.
func TestInvalidIDsDisablePathsAndFailMutations(t *testing.T) {
	run := RunAt(t.TempDir())
	for _, bad := range invalidIDs("/abs") {
		st := run.Step(bad)
		if st.Enabled() || st.Dir() != "" || st.Result() != "" || st.Err() == nil {
			t.Errorf("Step(%q) = %+v, want disabled with error", bad, st)
		}
		if err := st.Ensure(); err == nil {
			t.Errorf("Step(%q).Ensure succeeded", bad)
		}
		if err := st.ClearOutputs(); err == nil {
			t.Errorf("Step(%q).ClearOutputs succeeded", bad)
		}
		if err := st.WriteSession(SessionInfo{SessionID: "x"}); err == nil {
			t.Errorf("Step(%q).WriteSession succeeded", bad)
		}
		if _, err := st.ReadSession(); err == nil {
			t.Errorf("Step(%q).ReadSession succeeded", bad)
		}
		if err := st.ClearSession(); err == nil {
			t.Errorf("Step(%q).ClearSession succeeded", bad)
		}
		if _, err := st.ReadFanOutManifest(0, 0); err == nil {
			t.Errorf("Step(%q).ReadFanOutManifest succeeded", bad)
		}

		round := run.Step("ok").Review(bad)
		if round.Dir() != "" || round.Draft() != "" || round.Err() == nil {
			t.Errorf("Review(%q) = %+v, want disabled with error", bad, round)
		}
	}
	// Persistence off never touches disk, so even a bad id is a quiet no-op.
	if st := (Run{}).Step(".."); st.Err() != nil || st.ClearSession() != nil {
		t.Errorf("Run{}.Step(\"..\") = %+v, want disabled without error", st)
	}
	// The round inherits the step's validation error.
	if run.Step("..").Review("r1").Err() == nil {
		t.Error("Review on an invalid step lost the step's error")
	}
}

// TestGeneratedFanOutIDsAreValid guards the segment rule against
// over-tightening: generated child ids contain dots.
func TestGeneratedFanOutIDsAreValid(t *testing.T) {
	id := "analyze.__fanout__.g000.r000.i0000"
	if st := RunAt("/r").Step(id); st.Err() != nil || st.Dir() == "" {
		t.Fatalf("Step(%q) = %+v", id, st)
	}
}

// invalidIDs lists identifiers that must never be joined as a path segment.
// abs is an absolute path that must also be rejected.
func invalidIDs(abs string) []string {
	return []string{"", ".", "..", "../escape", "a/b", `a\b`, "a\x00b", abs}
}

func TestStepIDs(t *testing.T) {
	run, err := Open(t.TempDir()).Create("r")
	if err != nil {
		t.Fatal(err)
	}
	if ids, err := run.StepIDs(); err != nil || ids != nil {
		t.Fatalf("empty run StepIDs = %v, %v; want nil, nil", ids, err)
	}
	for _, id := range []string{"b", "a"} {
		if err := run.Step(id).Ensure(); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(run.Dir(), "steps", "stray"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ids, err := run.StepIDs()
	if err != nil || !reflect.DeepEqual(ids, []string{"a", "b"}) {
		t.Fatalf("StepIDs = %v, %v; want [a b]", ids, err)
	}
	if ids, err := (Run{}).StepIDs(); err != nil || ids != nil {
		t.Fatalf("disabled StepIDs = %v, %v", ids, err)
	}
}
