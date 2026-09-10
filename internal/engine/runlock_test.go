package engine

import (
	"os"
	"testing"

	"jig/internal/datastore"
)

func TestRunLeasePreservesExistingLockBytesAndIdentity(t *testing.T) {
	runDir := t.TempDir()
	path := datastore.SchedulerLockPath(runDir)
	if err := os.WriteFile(path, []byte("coordination only"), 0o600); err != nil {
		t.Fatal(err)
	}
	lease, err := AcquireRunLease(runDir)
	if err != nil {
		t.Fatal(err)
	}
	dev, ino, ok := lease.Identity()
	if !ok || dev == 0 || ino == 0 {
		t.Fatalf("Identity = (%d, %d, %t), want filesystem identity", dev, ino, ok)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "coordination only" {
		t.Fatalf("lock bytes = %q, want preserved bytes", got)
	}
	reacquired, err := AcquireRunLease(runDir)
	if err != nil {
		t.Fatalf("lease was not released: %v", err)
	}
	if err := reacquired.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRunLeaseCreatesMissingCoordinationFile(t *testing.T) {
	runDir := t.TempDir()
	lease, err := AcquireRunLease(runDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	info, err := os.Stat(datastore.SchedulerLockPath(runDir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("new coordination file size = %d, want 0", info.Size())
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("new coordination file mode = %o, want 644", info.Mode().Perm())
	}
}
