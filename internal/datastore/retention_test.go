package datastore

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// makeRun creates a run directory with a journal. If finishedAt is non-zero the
// journal ends with a run_finished envelope stamped at that time, marking the
// run terminal; otherwise the run has only a start line and is non-terminal.
func makeRun(t *testing.T, root, id string, finishedAt time.Time) string {
	t.Helper()
	dir, err := RunDir(root, id)
	if err != nil {
		t.Fatalf("RunDir(%s): %v", id, err)
	}
	lines := `{"seq":1,"ts":"2026-07-01T00:00:00Z","kind":"run_started","data":{}}` + "\n"
	if !finishedAt.IsZero() {
		lines += fmt.Sprintf(`{"seq":2,"ts":%q,"kind":"run_finished","data":{}}`+"\n",
			finishedAt.UTC().Format(time.RFC3339))
	}
	if err := os.WriteFile(JournalPath(dir), []byte(lines), 0o644); err != nil {
		t.Fatalf("write journal(%s): %v", id, err)
	}
	return dir
}

func exists(t *testing.T, dir string) bool {
	t.Helper()
	_, err := os.Stat(dir)
	return err == nil
}

func TestPrune_ZeroPolicyIsNoop(t *testing.T) {
	root := t.TempDir()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	makeRun(t, root, "run-old", old)

	pruned, err := Prune(root, RetentionPolicy{}, time.Now())
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(pruned) != 0 {
		t.Errorf("zero policy pruned %v, want none", pruned)
	}
	if !exists(t, filepath.Join(root, "runs", "run-old")) {
		t.Error("run-old removed by a zero policy")
	}
}

func TestPrune_MaxAgeRemovesOldTerminalRuns(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	makeRun(t, root, "run-old", now.Add(-48*time.Hour))
	makeRun(t, root, "run-recent", now.Add(-1*time.Hour))

	pruned, err := Prune(root, RetentionPolicy{MaxAge: 24 * time.Hour}, now)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if want := []string{"run-old"}; !equalSet(pruned, want) {
		t.Errorf("pruned = %v, want %v", pruned, want)
	}
	if exists(t, filepath.Join(root, "runs", "run-old")) {
		t.Error("run-old should have been pruned")
	}
	if !exists(t, filepath.Join(root, "runs", "run-recent")) {
		t.Error("run-recent should have been kept")
	}
}

func TestPrune_NeverRemovesNonTerminalRuns(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	// A very old, still-running (non-terminal) run must survive any policy.
	makeRun(t, root, "run-live", time.Time{})

	pruned, err := Prune(root, RetentionPolicy{MaxAge: time.Nanosecond, KeepLast: 0}, now)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(pruned) != 0 {
		t.Errorf("pruned %v, want none (non-terminal run must be preserved)", pruned)
	}
	if !exists(t, filepath.Join(root, "runs", "run-live")) {
		t.Error("non-terminal run-live was removed")
	}
}

func TestPrune_KeepLastProtectsNewest(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	makeRun(t, root, "run-1", now.Add(-3*time.Hour))
	makeRun(t, root, "run-2", now.Add(-2*time.Hour))
	makeRun(t, root, "run-3", now.Add(-1*time.Hour))

	// KeepLast=1 with no age rule: keep only the newest, prune the rest.
	pruned, err := Prune(root, RetentionPolicy{KeepLast: 1}, now)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if want := []string{"run-1", "run-2"}; !equalSet(pruned, want) {
		t.Errorf("pruned = %v, want %v", pruned, want)
	}
	if !exists(t, filepath.Join(root, "runs", "run-3")) {
		t.Error("newest run-3 should have been kept")
	}
}

func TestPrune_KeepLastProtectsAgainstMaxAge(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	// Both runs are older than MaxAge, but KeepLast=1 protects the newest.
	makeRun(t, root, "run-older", now.Add(-72*time.Hour))
	makeRun(t, root, "run-old", now.Add(-48*time.Hour))

	pruned, err := Prune(root, RetentionPolicy{MaxAge: 24 * time.Hour, KeepLast: 1}, now)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if want := []string{"run-older"}; !equalSet(pruned, want) {
		t.Errorf("pruned = %v, want %v", pruned, want)
	}
	if !exists(t, filepath.Join(root, "runs", "run-old")) {
		t.Error("run-old should be protected by KeepLast even though it is old")
	}
}

func TestPrune_EmptyRootErrors(t *testing.T) {
	if _, err := Prune("", RetentionPolicy{KeepLast: 1}, time.Now()); err == nil {
		t.Error("expected error for empty root")
	}
}

func TestPrune_MissingRunsDirIsNoop(t *testing.T) {
	root := t.TempDir() // no runs/ subdirectory created
	pruned, err := Prune(root, RetentionPolicy{KeepLast: 1}, time.Now())
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(pruned) != 0 {
		t.Errorf("pruned %v, want none", pruned)
	}
}

// equalSet compares two string slices ignoring order.
func equalSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]string(nil), a...)
	bc := append([]string(nil), b...)
	sort.Strings(ac)
	sort.Strings(bc)
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}
