//go:build darwin || linux

package datastore

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Invoked by the engine's process-crash test with synthetic prepared records.
func TestAuthoringPublicationCrashHelper(t *testing.T) {
	if os.Getenv("JIG_TEST_AUTHORING_CRASH_HELPER") != "1" {
		t.Skip("subprocess fixture")
	}
	dir := os.Getenv("JIG_TEST_AUTHORING_DIR")
	inputs := os.Getenv("JIG_TEST_AUTHORING_INPUTS")
	target := os.Getenv("JIG_TEST_AUTHORING_CHECKPOINT")
	lock, err := os.OpenFile(SchedulerLockPath(dir), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(inputs, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := os.ReadFile(filepath.Join(inputs, "workflow.json"))
	if err != nil {
		t.Fatal(err)
	}
	var blobs map[string][]byte
	raw, err := os.ReadFile(filepath.Join(inputs, "blobs.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &blobs); err != nil {
		t.Fatal(err)
	}
	park := func() { fmt.Println("CAPTURE_READY"); _, _ = io.Copy(io.Discard, os.Stdin) }
	check := func(stage string) error {
		if target == "blob_write" && strings.HasPrefix(stage, "authoring/blobs/") && strings.HasSuffix(stage, ":sync") || target == "manifest_write" && stage == "authoring/manifest.json:sync" || target == "before_commit" && stage == "workflow.json:rename" {
			park()
		}
		return nil
	}
	if err := publishAuthoring(dir, manifest, blobs, snapshot, check); err != nil {
		t.Fatal(err)
	}
	if target == "after_commit" {
		park()
	}
}
