// Package datastore manages the on-disk layout for jig runs under .jig/.
//
// The run directory convention is:
//
//	.jig/runs/<run-id>/
//	  journal.jsonl                  – one Envelope per line, written by manifest.Writer
//	  workflow.json                  – immutable workflow snapshot taken at run start
//	  scheduler.lock                 – advisory lock held by the live scheduler
//	  findings.jsonl                 – append-only security findings (internal/sentinel)
//	  security-monitor-state.json    – Tier-2 monitor cost accounting
//	  steps/
//	    <step-id>/
//	      input.md                   – assembled agent prompt for the latest dispatch
//	      output.md, output.json     – the step's prose and structured output
//	      result.json                – written on terminal step status events
//	      session.json               – mid-flight agent SessionID (crash reopen)
//	      security.md                – rendered findings scoped to this step
//	      transcript.jsonl           – append-only agent/command conversation
//	      fanout/                    – per-generation foreach expansion manifests
//	      review/<round-id>/         – human review drafts, submissions, feedback
//	  artifacts/
//
// Locations are addressed through four value types layered like the
// directories: Store (the .jig/ root), Run, Step, and ReviewRound. The zero
// value of each means persistence is off, and every type follows one rule:
//
//   - path accessors return "" when disabled, so an empty root can never
//     become a path relative to the working directory;
//   - optional mutations and reads are no-ops (nil error, zero value) when
//     disabled;
//   - in an enabled location, an identifier that is not a single safe path
//     segment disables the child location's paths and makes every mutation
//     and read return the validation error;
//   - required operations (Store.Create, Store.Resolve, Store.Prune) fail
//     explicitly when the store is disabled.
package datastore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// errDisabled reports a required operation attempted with persistence off.
var errDisabled = errors.New("datastore: persistence root is empty")

// validateSegment reports whether id can be joined as exactly one path
// element. Identifiers reach the filesystem from CLI input and generated
// fan-out ids (which contain dots), so the rule is per segment rather than an
// alphabet: non-empty, not "." or "..", and free of separators and NUL.
func validateSegment(kind, id string) error {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\\x00") ||
		filepath.IsAbs(id) || filepath.Base(id) != id {
		return fmt.Errorf("datastore: invalid %s id %q", kind, id)
	}
	return nil
}

// Store is the .jig/ persistence root. The zero Store means persistence is
// off: it lists nothing, deletes nothing, and refuses to create or resolve
// runs.
type Store struct{ root string }

// Open returns the Store rooted at root. An empty root yields the disabled
// zero Store.
func Open(root string) Store { return Store{root: root} }

// Enabled reports whether the store persists to disk.
func (s Store) Enabled() bool { return s.root != "" }

// Root returns the .jig/ directory, or "" when disabled.
func (s Store) Root() string { return s.root }

func (s Store) runsDir() string {
	if s.root == "" {
		return ""
	}
	return filepath.Join(s.root, "runs")
}

// Create creates (if necessary) the run directory for runID with its steps/
// and artifacts/ subdirectories, so executors never need to mkdir
// individually. Creating a run is required, so a disabled store is an error.
func (s Store) Create(runID string) (Run, error) {
	if !s.Enabled() {
		return Run{}, errDisabled
	}
	if err := validateSegment("run", runID); err != nil {
		return Run{}, err
	}
	dir := filepath.Join(s.runsDir(), runID)
	for _, sub := range []string{dir, filepath.Join(dir, "steps"), filepath.Join(dir, "artifacts")} {
		if err := os.MkdirAll(sub, 0o755); err != nil {
			return Run{}, fmt.Errorf("datastore: create run dir %q: %w", sub, err)
		}
	}
	return Run{dir: dir}, nil
}

// Resolve validates runID and resolves an existing persisted run without
// creating anything. Neither runs/ nor the run may be a symlink, and the
// resolved run must stay inside runs/, because these ids arrive from CLI
// input.
func (s Store) Resolve(runID string) (Run, error) {
	if !s.Enabled() {
		return Run{}, errDisabled
	}
	if err := validateSegment("run", runID); err != nil {
		return Run{}, err
	}

	runsDir, err := filepath.Abs(s.runsDir())
	if err != nil {
		return Run{}, fmt.Errorf("datastore: resolve runs directory: %w", err)
	}
	info, err := os.Lstat(runsDir)
	if err != nil {
		return Run{}, fmt.Errorf("datastore: access runs directory %q: %w", runsDir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Run{}, fmt.Errorf("datastore: runs path %q must not be a symlink", runsDir)
	}
	if !info.IsDir() {
		return Run{}, fmt.Errorf("datastore: runs path %q is not a directory", runsDir)
	}

	dir := filepath.Join(runsDir, runID)
	entry, err := os.Lstat(dir)
	if err != nil {
		return Run{}, fmt.Errorf("datastore: access run %q: %w", runID, err)
	}
	if entry.Mode()&os.ModeSymlink != 0 {
		return Run{}, fmt.Errorf("datastore: run %q must not be a symlink", runID)
	}
	if !entry.IsDir() {
		return Run{}, fmt.Errorf("datastore: run %q is not a directory", runID)
	}

	resolvedRuns, err := filepath.EvalSymlinks(runsDir)
	if err != nil {
		return Run{}, fmt.Errorf("datastore: resolve runs directory %q: %w", runsDir, err)
	}
	resolvedRun, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return Run{}, fmt.Errorf("datastore: resolve run %q: %w", runID, err)
	}
	rel, err := filepath.Rel(resolvedRuns, resolvedRun)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Run{}, fmt.Errorf("datastore: run %q escapes the persistence root", runID)
	}
	return Run{dir: dir}, nil
}

// Locate addresses the run for runID without creating it or checking that
// it exists — for readers that tolerate a missing run. A disabled store
// yields the disabled zero Run; an invalid runID is an error.
func (s Store) Locate(runID string) (Run, error) {
	if err := validateSegment("run", runID); err != nil {
		return Run{}, err
	}
	if !s.Enabled() {
		return Run{}, nil
	}
	return Run{dir: filepath.Join(s.runsDir(), runID)}, nil
}

// List returns the IDs of the persisted runs, sorted ascending. Because run
// IDs are timestamp-prefixed (YYYYMMDD-HHMMSS-…), ascending order is
// chronological — oldest first. Only subdirectories count as runs; stray
// files are ignored. A disabled store or a missing runs/ directory yields nil
// with no error: "no runs" and "persistence off" are treated identically.
func (s Store) List() ([]string, error) {
	if !s.Enabled() {
		return nil, nil
	}
	return listDirs(s.runsDir())
}

// listDirs returns the names of dir's subdirectories, sorted. Stray files are
// ignored and a missing dir yields nil with no error.
func listDirs(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("datastore: read dir %q: %w", dir, err)
	}
	var names []string
	for _, ent := range ents {
		if ent.IsDir() {
			names = append(names, ent.Name())
		}
	}
	slices.Sort(names)
	return names, nil
}

// Delete removes the entire run directory for runID. It is a no-op when the
// store is disabled or the run does not exist, and never creates directories
// as a side effect. An invalid runID is an error, so no id can widen the
// removal beyond runs/.
func (s Store) Delete(runID string) error {
	if !s.Enabled() {
		return nil
	}
	if err := validateSegment("run", runID); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(s.runsDir(), runID)); err != nil {
		return fmt.Errorf("datastore: delete run %q: %w", runID, err)
	}
	return nil
}

// Run addresses one run directory. The zero Run is disabled. Runs come from
// Store.Create, Store.Resolve, or RunAt.
type Run struct{ dir string }

// RunAt wraps an existing run directory path — for code that receives a run
// directory rather than a store and id, such as replay of an exported run.
// An empty dir yields the disabled zero Run.
func RunAt(dir string) Run { return Run{dir: dir} }

// Enabled reports whether the run persists to disk.
func (r Run) Enabled() bool { return r.dir != "" }

// Dir returns the run directory, or "" when disabled.
func (r Run) Dir() string { return r.dir }

func (r Run) path(name string) string {
	if r.dir == "" {
		return ""
	}
	return filepath.Join(r.dir, name)
}

// Journal returns journal.jsonl.
func (r Run) Journal() string { return r.path("journal.jsonl") }

// Artifacts returns the artifacts/ directory Store.Create pre-creates.
func (r Run) Artifacts() string { return r.path("artifacts") }

// Findings returns findings.jsonl, the append-only file holding every
// security finding emitted by both Tier-1 (guard) and Tier-2 (monitor fleet)
// for this run (see internal/sentinel).
func (r Run) Findings() string { return r.path("findings.jsonl") }

// SecurityMonitorState returns the run-owned Tier-2 cost-accounting state.
func (r Run) SecurityMonitorState() string { return r.path("security-monitor-state.json") }

// WorkflowSnapshot returns the immutable workflow definition captured when a
// run starts. Historical resume uses it instead of whichever version happens
// to be present in the working tree later.
func (r Run) WorkflowSnapshot() string { return r.path("workflow.json") }

// SchedulerLock names the advisory lock held for the lifetime of a live
// scheduler, preventing two jig processes from resuming the same run.
func (r Run) SchedulerLock() string { return r.path("scheduler.lock") }

// StepIDs lists the step directories present under steps/, sorted. A
// disabled run or a missing steps/ directory yields nil with no error.
func (r Run) StepIDs() ([]string, error) {
	if r.dir == "" {
		return nil, nil
	}
	return listDirs(filepath.Join(r.dir, "steps"))
}

// Step addresses steps/<id>/ inside the run. A Step from a disabled run is
// disabled and every operation on it is a no-op: ids only need validating
// before they touch the filesystem. A Step with an invalid id in an enabled
// run is disabled and reports the validation error from every mutation and
// read.
func (r Run) Step(id string) Step {
	if r.dir == "" {
		return Step{}
	}
	if err := validateSegment("step", id); err != nil {
		return Step{err: err}
	}
	return Step{dir: filepath.Join(r.dir, "steps", id)}
}

// Step addresses one step directory. The zero Step is disabled.
type Step struct {
	dir string
	err error
}

// Enabled reports whether the step persists to disk.
func (s Step) Enabled() bool { return s.dir != "" }

// Dir returns the step directory, or "" when disabled.
func (s Step) Dir() string { return s.dir }

// Err returns the id validation error, if the step was addressed with an
// invalid id.
func (s Step) Err() error { return s.err }

func (s Step) path(elems ...string) string {
	if s.dir == "" {
		return ""
	}
	return filepath.Join(append([]string{s.dir}, elems...)...)
}

// Ensure creates the step directory if necessary. A disabled step is a no-op.
func (s Step) Ensure() error {
	if s.err != nil {
		return s.err
	}
	if s.dir == "" {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("datastore: create step dir %q: %w", s.dir, err)
	}
	return nil
}

// Input returns input.md: the fully assembled prompt agent steps write
// before opening the harness.
func (s Step) Input() string { return s.path("input.md") }

// Result returns result.json, written on terminal step status events.
func (s Step) Result() string { return s.path("result.json") }

// Output returns output.md: the agent's final prose and rendered declared
// fields, its full human-readable deliverable. Base status fields live in
// metadata.md.
func (s Step) Output() string { return s.path("output.md") }

// Metadata returns metadata.md: the base status fields rendered as Markdown.
func (s Step) Metadata() string { return s.path("metadata.md") }

// OutputJSON returns output.json: the full structured output (base fields
// plus any declared [step.schema] fields).
func (s Step) OutputJSON() string { return s.path("output.json") }

// Transcript returns transcript.jsonl, the append-only record of the step's
// full agent conversation (see internal/transcript).
func (s Step) Transcript() string { return s.path("transcript.jsonl") }

// Security returns security.md: a human-readable rendering of every
// findings.jsonl entry scoped to this step (see sentinel.RenderMarkdown),
// refreshed each time the run records a new finding for the step.
func (s Step) Security() string { return s.path("security.md") }

// ClearOutputs removes the derived per-step inputs and outputs — input.md,
// result.json, output.md, output.json, metadata.md, and session.json — so a reset step
// starts fresh. transcript.jsonl is intentionally left intact: it is
// append-only and a re-run appends a new generation rather than overwriting
// history. A disabled step and missing files are no-ops.
func (s Step) ClearOutputs() error {
	if s.err != nil {
		return s.err
	}
	if s.dir == "" {
		return nil
	}
	for _, path := range []string{s.Input(), s.Result(), s.Output(), s.OutputJSON(), s.Metadata(), s.Session()} {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("datastore: clear step artifact %q: %w", path, err)
		}
	}
	return nil
}

// Review addresses review/<roundID>/ inside the step. It inherits the step's
// disabled state and validation error.
func (s Step) Review(roundID string) ReviewRound {
	if s.err != nil {
		return ReviewRound{err: s.err}
	}
	if s.dir == "" {
		return ReviewRound{}
	}
	if err := validateSegment("review round", roundID); err != nil {
		return ReviewRound{err: err}
	}
	return ReviewRound{dir: filepath.Join(s.dir, "review", roundID)}
}

// ReviewRound addresses one human review round. The zero ReviewRound is
// disabled.
type ReviewRound struct {
	dir string
	err error
}

// Enabled reports whether the round persists to disk.
func (r ReviewRound) Enabled() bool { return r.dir != "" }

// Dir returns the round directory, or "" when disabled.
func (r ReviewRound) Dir() string { return r.dir }

// Err returns the id validation error, if the round or its step was
// addressed with an invalid id.
func (r ReviewRound) Err() error { return r.err }

func (r ReviewRound) path(name string) string {
	if r.dir == "" {
		return ""
	}
	return filepath.Join(r.dir, name)
}

// Draft returns the operator's in-progress review draft.
func (r ReviewRound) Draft() string { return r.path("draft.json") }

// Submission returns the operator's submitted review decision.
func (r ReviewRound) Submission() string { return r.path("submission.json") }

// Feedback returns the rendered feedback handed back to the agent.
func (r ReviewRound) Feedback() string { return r.path("feedback.md") }

// Documents returns the directory of documents under review.
func (r ReviewRound) Documents() string { return r.path("documents") }
