package datastore

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// RetentionPolicy configures pruning of old run directories under .jig/runs/.
// A zero-value policy prunes nothing — retention is opt-in and conservative by
// design (Phase 7). The two knobs compose:
//
//   - KeepLast protects the N most-recently-finished runs from deletion.
//   - MaxAge deletes finished runs older than the cutoff, but never one that
//     KeepLast protects.
//
// With only KeepLast set, everything past the newest N is removed. With only
// MaxAge set, every finished run older than the cutoff is removed. With both
// zero, Prune is a no-op.
type RetentionPolicy struct {
	// MaxAge removes a finished run whose finish time is older than this. Zero
	// disables the age rule.
	MaxAge time.Duration

	// KeepLast protects the N most-recently-finished runs. Zero disables the
	// count rule (protects nothing by count).
	KeepLast int
}

// runInfo is a terminal run eligible for retention decisions.
type runInfo struct {
	id         string
	dir        string
	finishedAt time.Time
}

// Prune removes finished run directories under root according to policy and
// returns the IDs of the runs it deleted, in the order removed. It never
// touches a run that has not reached a terminal RunFinished event ("file is
// truth"): an in-progress or crashed run has no run_finished line in its
// journal and is always preserved. now is injected so callers (and tests) can
// pin the reference time.
//
// A missing runs/ directory is not an error (nothing to prune). root must be
// non-empty — an empty root means persistence is off and there is nothing on
// disk to prune.
func Prune(root string, policy RetentionPolicy, now time.Time) ([]string, error) {
	victims, err := selectPrunable(root, policy, now)
	if err != nil {
		return nil, err
	}
	var pruned []string
	for _, r := range victims {
		if err := os.RemoveAll(r.dir); err != nil {
			return pruned, fmt.Errorf("datastore: remove run %q: %w", r.id, err)
		}
		pruned = append(pruned, r.id)
	}
	return pruned, nil
}

// Prunable reports the run IDs that Prune would remove for the given policy,
// without deleting anything — the backing query for a `--dry-run`. Same rules
// as Prune: only terminal runs are candidates.
func Prunable(root string, policy RetentionPolicy, now time.Time) ([]string, error) {
	victims, err := selectPrunable(root, policy, now)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(victims))
	for i, r := range victims {
		ids[i] = r.id
	}
	return ids, nil
}

// selectPrunable computes the terminal runs a policy would remove, newest-last
// in removal order. It performs no deletion, so Prune and Prunable share one
// selection rule.
func selectPrunable(root string, policy RetentionPolicy, now time.Time) ([]runInfo, error) {
	if root == "" {
		return nil, fmt.Errorf("datastore: prune with empty root")
	}
	if policy.MaxAge <= 0 && policy.KeepLast <= 0 {
		return nil, nil // conservative default: nothing to do
	}

	runsDir := filepath.Join(root, "runs")
	ents, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("datastore: read runs dir %q: %w", runsDir, err)
	}

	// Collect only terminal runs; non-terminal runs are never candidates.
	var finished []runInfo
	for _, ent := range ents {
		if !ent.IsDir() {
			continue
		}
		dir := filepath.Join(runsDir, ent.Name())
		ts, ok := runFinishedAt(dir)
		if !ok {
			continue // not terminal — leave it alone
		}
		finished = append(finished, runInfo{id: ent.Name(), dir: dir, finishedAt: ts})
	}

	// Newest first so KeepLast protects the head of the slice.
	sort.Slice(finished, func(i, j int) bool {
		return finished[i].finishedAt.After(finished[j].finishedAt)
	})

	var victims []runInfo
	for i, r := range finished {
		if policy.KeepLast > 0 && i < policy.KeepLast {
			continue // protected: among the newest KeepLast
		}
		// Past the keep window (or KeepLast disabled). If MaxAge is set, the run
		// must also be older than the cutoff to be removed; if MaxAge is off,
		// KeepLast alone authorizes removal.
		if policy.MaxAge > 0 && now.Sub(r.finishedAt) <= policy.MaxAge {
			continue
		}
		victims = append(victims, r)
	}
	return victims, nil
}

// runFinishedAt reports whether the run at dir is terminal and, if so, when it
// finished. A run is terminal iff its journal.jsonl contains a run_finished
// envelope; the returned time is that envelope's timestamp (falling back to the
// journal's modification time if the timestamp is absent). The journal is
// decoded into a minimal local shape so datastore stays free of an engine
// import (engine already depends on datastore).
func runFinishedAt(dir string) (time.Time, bool) {
	path := JournalPath(dir)
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()

	// Only the kind and timestamp are needed; ignore the event payload.
	type envelope struct {
		Ts   time.Time `json:"ts"`
		Kind string    `json:"kind"`
	}

	var finishedTs time.Time
	found := false
	br := bufio.NewReader(f)
	for {
		line, readErr := br.ReadString('\n')
		if len(line) > 0 {
			var env envelope
			if json.Unmarshal([]byte(line), &env) == nil && env.Kind == "run_finished" {
				found = true
				finishedTs = env.Ts
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			break
		}
	}
	if !found {
		return time.Time{}, false
	}
	if finishedTs.IsZero() {
		if fi, err := os.Stat(path); err == nil {
			finishedTs = fi.ModTime()
		}
	}
	return finishedTs, true
}
