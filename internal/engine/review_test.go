package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jig/internal/review"
	"jig/internal/step"
	"jig/internal/workflow"
)

func TestPrepareReviewSnapshotsFileTarget(t *testing.T) {
	runWorktree := t.TempDir()
	path := filepath.Join(runWorktree, "docs", "spec.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const content = "# Specification\n\nExact run snapshot.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	s := reviewScheduler(runWorktree, map[string]any{"spec_path": "docs/spec.md"})
	gate := &workflow.Step{ID: "gate", Review: []workflow.ReviewTarget{{
		File: "@write.spec_path", Label: "Specification",
	}}}
	sess, err := s.prepareReview(gate)
	if err != nil {
		t.Fatalf("prepareReview: %v", err)
	}
	if len(sess.Documents) != 1 {
		t.Fatalf("documents = %#v", sess.Documents)
	}
	doc := sess.Documents[0]
	if doc.Content != content || doc.Format != "markdown" {
		t.Fatalf("document = %#v, want markdown content", doc)
	}
	if got, want := doc.Source, "@write.spec_path (docs/spec.md)"; got != want {
		t.Fatalf("document source = %q, want %q", got, want)
	}
	if err := os.WriteFile(path, []byte("mutated"), 0o644); err != nil {
		t.Fatal(err)
	}
	persisted, err := os.ReadFile(doc.SnapshotPath)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if string(persisted) != content || review.Digest(string(persisted)) != doc.SHA256 {
		t.Fatalf("snapshot = %q, digest = %q; want immutable original", persisted, review.Digest(string(persisted)))
	}
	restored, err := restoreReviewSession(s.runDir, ReviewRequest{
		StepID: "gate", RoundID: sess.RoundID, Documents: sess.Documents,
	})
	if err != nil || len(restored.Documents) != 1 || restored.Documents[0].Content != content {
		t.Fatalf("restored review = %#v, %v; want immutable snapshot", restored, err)
	}
}

func TestPrepareReviewRejectsUnsafeFileTargets(t *testing.T) {
	runWorktree := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(runWorktree, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(runWorktree, "directory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runWorktree, "binary.md"), []byte{'a', 0, 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runWorktree, "large.md"), []byte(strings.Repeat("x", maxReviewDocument+1)), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		path string
		want string
	}{
		{name: "parent traversal", path: "../secret.md", want: "must not traverse parent"},
		{name: "absolute path", path: filepath.Join(outside, "secret.md"), want: "must be relative"},
		{name: "escaping symlink", path: "escape/secret.md", want: "escapes execution directory"},
		{name: "directory", path: "directory", want: "not a regular file"},
		{name: "binary", path: "binary.md", want: "binary or contains NUL"},
		{name: "oversize", path: "large.md", want: "exceeds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := reviewScheduler(runWorktree, map[string]any{"spec_path": tc.path})
			_, err := s.prepareReview(&workflow.Step{ID: "gate", Review: []workflow.ReviewTarget{{
				File: "@write.spec_path", Label: "Specification",
			}}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("prepareReview error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPrepareReviewPreservesTargetOrder(t *testing.T) {
	runWorktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(runWorktree, "proof.txt"), []byte("proof bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := reviewScheduler(runWorktree, map[string]any{
		"overview":   "short summary",
		"proof_path": "proof.txt",
	})
	s.diffs["write"] = "diff --git a/a b/a\n"
	wf, err := workflow.Decode(`
[workflow]
name = "review-order"
version = "1"
[[step]]
id = "write"
type = "agent"
skill = "write"
  [step.schema]
  overview = "text"
  proof_path = "text"
[[step]]
id = "gate"
type = "review"
depends_on = ["write"]
output_type = { enum = ["approve"] }
  [[step.review]]
  source = "@write.overview"
  label = "Summary"
  [[step.review]]
  file = "@write.proof_path"
  label = "Proof"
  [[step.review]]
  source = "diff"
  label = "Diff"
`, "")
	if err != nil {
		t.Fatal(err)
	}
	s.wf = wf
	gate := &wf.Steps[1]
	sess, err := s.prepareReview(gate)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(sess.Documents))
	for i, doc := range sess.Documents {
		got[i] = doc.Label + ":" + doc.Content
	}
	want := []string{"Summary:short summary", "Proof:proof bytes", "Diff:diff --git a/a b/a\n"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("document order = %v, want %v", got, want)
	}
}

func reviewScheduler(runWorktree string, fields map[string]any) *scheduler {
	return &scheduler{
		runWorktree: runWorktree,
		runDir:      tdir(runWorktree),
		states: map[string]*step.State{
			"write": {ID: "write", Result: &step.Result{Status: step.StatusSucceeded}},
			"gate":  {ID: "gate"},
		},
		structured: map[string]map[string]any{"write": fields},
		diffs:      make(map[string]string),
	}
}

func tdir(runWorktree string) string {
	return filepath.Join(runWorktree, ".jig-run")
}
