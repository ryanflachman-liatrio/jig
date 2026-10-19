package engine

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
)

func TestRunLeasePreservesExistingLockBytesAndIdentity(t *testing.T) {
	runDir := datastore.RunAt(t.TempDir())
	path := runDir.SchedulerLock()
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
	runDir := datastore.RunAt(t.TempDir())
	lease, err := AcquireRunLease(runDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	info, err := os.Stat(runDir.SchedulerLock())
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

func TestRunLeaseHeldReportsErrRunLocked(t *testing.T) {
	runDir := datastore.RunAt(t.TempDir())
	lease, err := AcquireRunLease(runDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if _, err := AcquireRunLease(runDir); !errors.Is(err, ErrRunLocked) {
		t.Fatalf("second acquire error = %v, want ErrRunLocked", err)
	}
}

func TestRunLeaseOpenFailureIsNotReportedAsLocked(t *testing.T) {
	runDir := datastore.RunAt(filepath.Join(t.TempDir(), "missing"))
	_, err := AcquireRunLease(runDir)
	if err == nil || errors.Is(err, ErrRunLocked) {
		t.Fatalf("acquire in a missing dir = %v, want a non-lock error", err)
	}
}

func TestRunLeaseDisabledIsNilAndCloseSafe(t *testing.T) {
	lease, err := AcquireRunLease(datastore.Run{})
	if lease != nil || err != nil {
		t.Fatalf("disabled lease = (%v, %v), want (nil, nil)", lease, err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("Close on nil lease: %v", err)
	}
}
