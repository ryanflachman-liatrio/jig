package runexport

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"jig/internal/datastore"
)

type request struct {
	runDir string
}

// sourceRecord retains only filesystem consistency facts. It never retains
// evidence contents, which keeps acquisition safe to use before projection.
type sourceRecord struct {
	name     string
	present  bool
	mode     os.FileMode
	size     int64
	modified time.Time
	dev      uint64
	ino      uint64
}

type inventory struct {
	runRoot sourceRecord
	files   []sourceRecord
	steps   []string
}

func resolveRequest(options Options) (request, error) {
	if options.Root == "" || options.RunID == "" || options.Destination == "" {
		return request{}, fmt.Errorf("%w: root, run id, and destination are required", ErrUsage)
	}
	runDir, err := datastore.ResolveRunDir(options.Root, options.RunID)
	if err != nil {
		return request{}, fmt.Errorf("%w: run cannot be resolved", ErrUsage)
	}
	destination, err := filepath.Abs(options.Destination)
	if err != nil {
		return request{}, fmt.Errorf("%w: destination cannot be resolved", ErrUsage)
	}
	parent := filepath.Dir(destination)
	parentInfo, err := os.Stat(parent)
	if err != nil || !parentInfo.IsDir() {
		return request{}, fmt.Errorf("%w: destination parent is unavailable", ErrUsage)
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return request{}, fmt.Errorf("%w: persistence root cannot be resolved", ErrUsage)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return request{}, fmt.Errorf("%w: persistence root is unavailable", ErrUsage)
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return request{}, fmt.Errorf("%w: destination parent is unavailable", ErrUsage)
	}
	if isWithin(resolvedRoot, filepath.Join(resolvedParent, filepath.Base(destination))) {
		return request{}, fmt.Errorf("%w: destination is inside persistence root", ErrUsage)
	}
	if _, err := os.Lstat(destination); err == nil || !os.IsNotExist(err) {
		return request{}, fmt.Errorf("%w: destination already exists", ErrUsage)
	}
	return request{runDir: runDir}, nil
}

func isWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func collectInventory(runDir string) (inventory, error) {
	root, err := os.OpenRoot(runDir)
	if err != nil {
		return inventory{}, fmt.Errorf("%w: run evidence is unavailable", ErrOperational)
	}
	defer root.Close()

	var out inventory
	rootInfo, err := root.Lstat(".")
	if err != nil || !rootInfo.IsDir() {
		return inventory{}, fmt.Errorf("%w: run root is unavailable", ErrOperational)
	}
	record, err := sourceInfo(".", rootInfo)
	if err != nil {
		return inventory{}, err
	}
	out.runRoot = record
	for _, name := range []string{"journal.jsonl", "workflow.json", "scheduler.lock"} {
		if err := appendRegular(root, name, &out); err != nil {
			if os.IsNotExist(err) {
				out.files = append(out.files, sourceRecord{name: name})
				continue
			}
			return inventory{}, err
		}
	}
	stepsInfo, err := root.Lstat("steps")
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil || stepsInfo.Mode()&os.ModeSymlink != 0 || !stepsInfo.IsDir() {
		return inventory{}, fmt.Errorf("%w: invalid selected steps directory", ErrOperational)
	}
	stepsDir, err := root.Open("steps")
	if err != nil {
		return inventory{}, fmt.Errorf("%w: open selected steps directory", ErrOperational)
	}
	entries, err := stepsDir.ReadDir(-1)
	closeErr := stepsDir.Close()
	if err != nil || closeErr != nil {
		return inventory{}, fmt.Errorf("%w: read selected steps directory", ErrOperational)
	}
	if len(entries) > maxStepInventory {
		return inventory{}, fmt.Errorf("%w: step inventory exceeds the size limit", ErrOperational)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "." || name == ".." || strings.ContainsAny(name, `/\\`) {
			return inventory{}, fmt.Errorf("%w: invalid step entry", ErrOperational)
		}
		info, err := root.Lstat(filepath.Join("steps", name))
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return inventory{}, fmt.Errorf("%w: invalid selected step", ErrOperational)
		}
		out.steps = append(out.steps, name)
		transcriptName := filepath.Join("steps", name, "transcript.jsonl")
		if err := appendRegular(root, transcriptName, &out); err != nil {
			if os.IsNotExist(err) {
				out.files = append(out.files, sourceRecord{name: transcriptName})
			} else {
				return inventory{}, err
			}
		}
	}
	sort.Strings(out.steps)
	return out, nil
}

// Recheck reports only that selected evidence changed, never the source name
// or value. The lease prevents cooperative scheduler writes; this catches
// detectable external changes before later publication.
func (before inventory) Recheck(runDir string) error {
	after, err := collectInventory(runDir)
	if err != nil {
		return fmt.Errorf("%w: selected evidence changed", ErrOperational)
	}
	if !sameRecord(before.runRoot, after.runRoot) || !sameStrings(before.steps, after.steps) || len(before.files) != len(after.files) {
		return fmt.Errorf("%w: selected evidence changed", ErrOperational)
	}
	for i := range before.files {
		if !sameRecord(before.files[i], after.files[i]) {
			return fmt.Errorf("%w: selected evidence changed", ErrOperational)
		}
	}
	return nil
}

func appendRegular(root *os.Root, name string, out *inventory) error {
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: invalid selected evidence", ErrOperational)
	}
	record, err := sourceInfo(name, info)
	if err != nil {
		return err
	}
	out.files = append(out.files, record)
	return nil
}

func sourceInfo(name string, info os.FileInfo) (sourceRecord, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return sourceRecord{}, fmt.Errorf("%w: unsupported source identity", ErrOperational)
	}
	return sourceRecord{name: name, present: true, mode: info.Mode(), size: info.Size(), modified: info.ModTime(), dev: uint64(stat.Dev), ino: stat.Ino}, nil
}

func sameRecord(a, b sourceRecord) bool {
	return a.name == b.name && a.present == b.present && a.mode == b.mode && a.size == b.size && a.modified.Equal(b.modified) && a.dev == b.dev && a.ino == b.ino
}

// fileRecord looks up one selected source's consistency record by its
// relative name (e.g. "journal.jsonl" or "steps/<id>/transcript.jsonl").
func (inv inventory) fileRecord(name string) (sourceRecord, bool) {
	for _, f := range inv.files {
		if f.name == name {
			return f, true
		}
	}
	return sourceRecord{}, false
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
