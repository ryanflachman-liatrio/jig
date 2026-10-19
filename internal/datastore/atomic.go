package datastore

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomic replaces path with data so that a reader, or a crash at any
// point, observes either the old contents or the new contents in full — never
// a partial or empty file. It is the single write path for durable,
// whole-file run state (session.json, fan-out manifests, monitor state).
//
// The data is written to a uniquely named temp file in the same directory (so
// concurrent writers never share a temp file and the rename stays on one
// filesystem), fsynced, chmodded to perm, and renamed over path. The parent
// directory is then fsynced so the rename itself survives power loss.
// Append-only files (journal, transcript, findings) must not use this.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("datastore: create temp for %q: %w", path, err)
	}
	tmpPath := tmp.Name()
	// After a successful rename tmpPath no longer exists and this is a no-op.
	defer os.Remove(tmpPath)

	if err := writeAndSync(tmp, data, perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("datastore: write %q: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("datastore: close temp for %q: %w", path, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("datastore: replace %q: %w", path, err)
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("datastore: sync directory of %q: %w", path, err)
	}
	return nil
}

func writeAndSync(f *os.File, data []byte, perm os.FileMode) error {
	if err := f.Chmod(perm); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

// syncDir flushes a directory entry change (a create or rename) to disk.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
