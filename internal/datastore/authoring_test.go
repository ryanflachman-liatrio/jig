package datastore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthoringPublicationFailures(t *testing.T) {
	data := []byte("ORIGINAL")
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	blobs := map[string][]byte{digest: data}
	for _, record := range []string{"authoring/blobs/" + digest, "authoring/manifest.json", "workflow.json"} {
		for _, stage := range []string{"write", "sync", "close", "rename", "directory_sync"} {
			t.Run(filepath.Base(record)+"_"+stage, func(t *testing.T) {
				dir := t.TempDir()
				reached := false
				err := publishAuthoring(dir, []byte(`{"version":1}`), blobs, []byte(`{"locked":true}`), func(at string) error {
					if at == record+":"+stage {
						reached = true
						return errors.New("injected failure")
					}
					return nil
				})
				if !reached || !errors.Is(err, ErrAuthoringStorage) {
					t.Fatalf("reached=%v err=%v", reached, err)
				}
				// A workflow rename followed by a directory-sync error can leave the file;
				// Start must still reject it and release ownership (engine test covers this).
				if record != "workflow.json" || stage != "directory_sync" {
					if _, err := os.Stat(filepath.Join(dir, "workflow.json")); !os.IsNotExist(err) {
						t.Fatal("snapshot published before required operation")
					}
				}
				if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if strings.HasPrefix(entry.Name(), ".pending-") {
						t.Error("temporary file leaked")
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestAuthoringMetadataBounds(t *testing.T) {
	for _, data := range []string{`{"value":1,"value":2}`, `{"unknown":1}`, strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65), `{"items":[` + strings.Repeat("0,", 2048) + `0]}`} {
		var dst struct {
			Value int   `json:"value"`
			Items []int `json:"items"`
		}
		if err := DecodeAuthoringRecord([]byte(data), 8<<20, &dst); err == nil {
			t.Fatal("unbounded/ambiguous metadata accepted")
		}
	}
}

func TestAuthoringStorageRejectsSymlinks(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "authoring")); err != nil {
		t.Fatal(err)
	}
	if err := PublishAuthoring(dir, []byte(`{}`), nil, []byte(`{}`)); err == nil {
		t.Fatal("publication followed escaping symlink")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("publication escaped root")
	}
	if _, err := ReadAuthoringFile(dir, "authoring/manifest.json", 8<<20); err == nil {
		t.Fatal("reader followed symlink")
	}
}
