package datastore

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
)

var ErrAuthoringStorage = errors.New("authoring capture: storage")

// PublishAuthoring commits immutable blobs and manifest before workflow.json.
// The caller owns an exclusive run lease and does not register a scheduler until
// this returns successfully. Partial records cannot authorize a run.
func PublishAuthoring(dir string, manifest []byte, blobs map[string][]byte, snapshot []byte) error {
	return publishAuthoring(dir, manifest, blobs, snapshot, nil)
}
func publishAuthoring(dir string, manifest []byte, blobs map[string][]byte, snapshot []byte, check func(string) error) (resultErr error) {
	if dir == "" || len(manifest) > 8<<20 || len(snapshot) > 32<<20 || len(blobs) > 1024 {
		return ErrAuthoringStorage
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return ErrAuthoringStorage
	}
	defer func() {
		if err := root.Close(); err != nil && resultErr == nil {
			resultErr = ErrAuthoringStorage
		}
	}()
	if err := root.MkdirAll("authoring/blobs", 0700); err != nil {
		return ErrAuthoringStorage
	}
	for _, name := range []string{"authoring", "authoring/blobs"} {
		info, err := root.Lstat(name)
		if err != nil || !info.IsDir() {
			return ErrAuthoringStorage
		}
	}
	keys := make([]string, 0, len(blobs))
	total := 0
	for key, data := range blobs {
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != key || len(data) > 1<<20 {
			return ErrAuthoringStorage
		}
		keys = append(keys, key)
		total += len(data)
	}
	if total > 16<<20 {
		return ErrAuthoringStorage
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := publishAuthoringFile(root, "authoring/blobs/"+key, blobs[key], check); err != nil {
			return err
		}
	}
	if err := publishAuthoringFile(root, "authoring/manifest.json", manifest, check); err != nil {
		return err
	}
	for _, name := range []string{"authoring", "."} {
		if err := syncAuthoringRoot(root, name); err != nil {
			return err
		}
	}
	if err := syncAuthoringDir(filepath.Dir(dir)); err != nil {
		return err
	}
	return publishAuthoringFile(root, "workflow.json", snapshot, check)
}
func publishAuthoringFile(root *os.Root, name string, data []byte, check func(string) error) error {
	parent := filepath.Dir(name)
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return ErrAuthoringStorage
	}
	temporary := filepath.Join(parent, ".pending-"+hex.EncodeToString(random[:]))
	f, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return ErrAuthoringStorage
	}
	defer root.Remove(temporary)
	defer f.Close()
	for _, stage := range []string{"write", "sync", "close", "rename", "directory_sync"} {
		if check != nil {
			if err := check(name + ":" + stage); err != nil {
				return ErrAuthoringStorage
			}
		}
		switch stage {
		case "write":
			var n int
			n, err = f.Write(data)
			if err == nil && n != len(data) {
				err = io.ErrShortWrite
			}
		case "sync":
			err = f.Sync()
		case "close":
			err = f.Close()
		case "rename":
			err = root.Rename(temporary, name)
		case "directory_sync":
			err = syncAuthoringRoot(root, parent)
		}
		if err != nil {
			return ErrAuthoringStorage
		}
	}
	return nil
}
func syncAuthoringDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return ErrAuthoringStorage
	}
	err = d.Sync()
	ce := d.Close()
	if err != nil || ce != nil {
		return ErrAuthoringStorage
	}
	return nil
}

func syncAuthoringRoot(root *os.Root, name string) error {
	d, err := root.Open(name)
	if err != nil {
		return ErrAuthoringStorage
	}
	err = d.Sync()
	ce := d.Close()
	if err != nil || ce != nil {
		return ErrAuthoringStorage
	}
	return nil
}
