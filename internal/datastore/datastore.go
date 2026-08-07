// Package datastore manages the on-disk layout for jig runs under .jig/.
//
// The run directory convention is:
//
//	.jig/runs/<run-id>/
//	  journal.jsonl          – one Envelope per line, written by manifest.Writer
//	  steps/
//	    <step-id>/
//	      result.json        – written on terminal step status events
//	  artifacts/             – agent output (Phase 4+)
//
// RunDir and StepDir create the directory tree on first call and return the
// absolute path.  All callers are engine-internal; the root is .jig/ by
// convention, passed in from Manager.
package datastore

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// RunDir creates (if necessary) the run directory for runID under root and
// returns its path.  The subdirectories steps/ and artifacts/ are created at
// the same time so executors never need to mkdir individually.
//
// root is the .jig/ directory (from Manager.root).  A non-empty root that
// cannot be created returns an error.  Callers that pass "" opt out of
// persistence (tests, Phase 1 compatibility).
func RunDir(root, runID string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("datastore: root is empty")
	}
	dir := filepath.Join(root, "runs", runID)
	for _, sub := range []string{dir, filepath.Join(dir, "steps"), filepath.Join(dir, "artifacts")} {
		if err := os.MkdirAll(sub, 0o755); err != nil {
			return "", fmt.Errorf("datastore: create run dir %q: %w", sub, err)
		}
	}
	return dir, nil
}

// ListRunIDs returns the IDs of the runs persisted under root's runs/ directory,
// sorted ascending. Because run IDs are timestamp-prefixed (YYYYMMDD-HHMMSS-…),
// ascending order is chronological — oldest first. Only subdirectories count as
// runs; stray files are ignored.
//
// A missing runs/ directory yields nil with no error (nothing has run yet), and
// an empty root (persistence off) yields nil as well — callers treat "no runs to
// list" and "persistence disabled" identically.
func ListRunIDs(root string) ([]string, error) {
	if root == "" {
		return nil, nil
	}
	runsDir := filepath.Join(root, "runs")
	ents, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("datastore: read runs dir %q: %w", runsDir, err)
	}
	var ids []string
	for _, ent := range ents {
		if ent.IsDir() {
			ids = append(ids, ent.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// StepDir creates (if necessary) the per-step directory steps/<stepID>/ inside
// runDir and returns its path.  runDir must already exist (RunDir creates it).
func StepDir(runDir, stepID string) (string, error) {
	dir := filepath.Join(runDir, "steps", stepID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("datastore: create step dir %q: %w", dir, err)
	}
	return dir, nil
}

// JournalPath returns the path to journal.jsonl inside runDir.
func JournalPath(runDir string) string { return filepath.Join(runDir, "journal.jsonl") }

// ResultPath returns the path to result.json for a step inside runDir.
func ResultPath(runDir, stepID string) string {
	return filepath.Join(runDir, "steps", stepID, "result.json")
}

// TranscriptPath returns the path to transcript.jsonl for a step inside runDir.
// The append-only transcript lives beside result.json and holds the step's full
// agent conversation (see internal/transcript).
func TranscriptPath(runDir, stepID string) string {
	return filepath.Join(runDir, "steps", stepID, "transcript.jsonl")
}

// OutputPath returns the canonical path to output.md for a step inside runDir.
// Content is the agent's raw_result base-schema field — the clean prose answer
// written by the agent as its primary deliverable.
func OutputPath(runDir, stepID string) string {
	return filepath.Join(runDir, "steps", stepID, "output.md")
}

// OutputJSONPath returns the canonical path to output.json for a step inside
// runDir. Content is the full structured output JSON (base fields + any
// declared [step.schema] fields).
func OutputJSONPath(runDir, stepID string) string {
	return filepath.Join(runDir, "steps", stepID, "output.json")
}
