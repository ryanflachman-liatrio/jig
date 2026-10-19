package datastore

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// makeRun creates a run directory with a journal. If finishedAt is non-zero the
// journal ends with a run_finished envelope stamped at that time, marking the
// run terminal; otherwise the run has only a start line and is non-terminal.
func makeRun(t *testing.T, root, id string, finishedAt time.Time) string {
	t.Helper()
	dir, err := Open(root).Create(id)
	if err != nil {
		t.Fatalf("RunDir(%s): %v", id, err)
	}
	lines := `{"seq":1,"ts":"2026-07-01T00:00:00Z","kind":"run_started","data":{}}` + "\n"
	if !finishedAt.IsZero() {
		lines += fmt.Sprintf(`{"seq":2,"ts":%q,"kind":"run_finished","data":{}}`+"\n",
			finishedAt.UTC().Format(time.RFC3339))
	}
	if err := os.WriteFile(dir.Journal(), []byte(lines), 0o644); err != nil {
		t.Fatalf("write journal(%s): %v", id, err)
	}
	return dir.Dir()
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

	pruned, err := Open(root).Prune(RetentionPolicy{}, time.Now())
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

	pruned, err := Open(root).Prune(RetentionPolicy{MaxAge: 24 * time.Hour}, now)
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

	pruned, err := Open(root).Prune(RetentionPolicy{MaxAge: time.Nanosecond, KeepLast: 0}, now)
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
	pruned, err := Open(root).Prune(RetentionPolicy{KeepLast: 1}, now)
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

	pruned, err := Open(root).Prune(RetentionPolicy{MaxAge: 24 * time.Hour, KeepLast: 1}, now)
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
	if _, err := Open("").Prune(RetentionPolicy{KeepLast: 1}, time.Now()); err == nil {
		t.Error("expected error for empty root")
	}
}

func TestPrune_MissingRunsDirIsNoop(t *testing.T) {
	root := t.TempDir() // no runs/ subdirectory created
	pruned, err := Open(root).Prune(RetentionPolicy{KeepLast: 1}, time.Now())
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
	slices.Sort(ac)
	slices.Sort(bc)
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}

// TestRunFinishedAt_LongAndTornLines guards the journal scan: an envelope
// larger than bufio.Scanner's default 64 KiB token must not hide a later
// run_finished line, and a torn trailing write must not hide an earlier one.
func TestRunFinishedAt_LongAndTornLines(t *testing.T) {
	finished := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	big := strings.Repeat("x", 256<<10)
	for name, journal := range map[string]string{
		"long line before finish": `{"seq":1,"kind":"step_output","data":{"text":"` + big + `"}}` + "\n" +
			`{"seq":2,"ts":"2026-07-01T12:00:00Z","kind":"run_finished","data":{}}` + "\n",
		"torn line after finish": `{"seq":1,"ts":"2026-07-01T12:00:00Z","kind":"run_finished","data":{}}` + "\n" +
			`{"seq":2,"kind":"step_out`,
	} {
		t.Run(name, func(t *testing.T) {
			run, err := Open(t.TempDir()).Create("r")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(run.Journal(), []byte(journal), 0o644); err != nil {
				t.Fatal(err)
			}
			got, ok := runFinishedAt(run)
			if !ok || !got.Equal(finished) {
				t.Fatalf("runFinishedAt = %v, %v; want %v, true", got, ok, finished)
			}
		})
	}
}

func TestRetentionPolicyValidate(t *testing.T) {
	for _, p := range []RetentionPolicy{{}, {KeepLast: 3}, {MaxAge: time.Hour}, {KeepLast: 1, MaxAge: time.Hour}} {
		if err := p.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v, want nil", p, err)
		}
	}
	for _, p := range []RetentionPolicy{{KeepLast: -1}, {MaxAge: -time.Hour}, {KeepLast: -1, MaxAge: time.Hour}} {
		if err := p.Validate(); err == nil {
			t.Errorf("Validate(%+v) = nil, want error", p)
		}
	}
}

// TestPrune_NegativeKnobErrors guards against a typo like --keep-last -1
// silently disabling the count rule and letting MaxAge prune everything.
func TestPrune_NegativeKnobErrors(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	makeRun(t, root, "run-old", now.Add(-48*time.Hour))
	if _, err := Open(root).Prune(RetentionPolicy{KeepLast: -1, MaxAge: time.Hour}, now); err == nil {
		t.Fatal("expected error for negative KeepLast")
	}
	if !exists(t, filepath.Join(root, "runs", "run-old")) {
		t.Error("run-old removed despite an invalid policy")
	}
}
