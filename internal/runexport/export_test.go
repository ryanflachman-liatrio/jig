package runexport

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"jig/internal/engine"
)

func TestResolveRequestRefusesUnsafeTargetsWithoutWrites(t *testing.T) {
	root, runDir := makeRunStore(t)
	outside := t.TempDir()
	existing := filepath.Join(outside, "existing.zip")
	if err := os.WriteFile(existing, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(outside, "dangling.zip")
	if err := os.Symlink("missing.zip", dangling); err != nil {
		t.Fatal(err)
	}
	before := payloadHash(t, runDir)
	for _, tc := range []struct {
		name string
		opts Options
	}{
		{"empty run", Options{Root: root, Destination: filepath.Join(outside, "a.zip")}},
		{"path run", Options{Root: root, RunID: "../run-1", Destination: filepath.Join(outside, "a.zip")}},
		{"inside root", Options{Root: root, RunID: "run-1", Destination: filepath.Join(root, "bundle.zip")}},
		{"missing parent", Options{Root: root, RunID: "run-1", Destination: filepath.Join(outside, "missing", "a.zip")}},
		{"existing destination", Options{Root: root, RunID: fixtureRunID, Destination: existing}},
		{"dangling destination", Options{Root: root, RunID: fixtureRunID, Destination: dangling}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Export(context.Background(), tc.opts)
			if !errors.Is(err, ErrUsage) {
				t.Fatalf("Export error = %v, want usage refusal", err)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(runDir, "scheduler.lock")); !os.IsNotExist(err) {
		t.Fatalf("unsafe request changed run: %v", err)
	}
	if after := payloadHash(t, runDir); after != before {
		t.Fatalf("run payload hash changed: got %s want %s", after, before)
	}
}

func TestConfinedInventoryRejectsSelectedSymlink(t *testing.T) {
	runDir := t.TempDir()
	if err := os.Symlink(filepath.Join(t.TempDir(), "journal"), filepath.Join(runDir, "journal.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := collectInventory(runDir); !errors.Is(err, ErrOperational) {
		t.Fatalf("collectInventory error = %v, want operational refusal", err)
	}
}

func TestResolveRequestRejectsMissingFileAndSymlinkRuns(t *testing.T) {
	root := t.TempDir()
	runs := filepath.Join(root, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, "file-run"), []byte("not a run"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file-run", filepath.Join(runs, "link-run")); err != nil {
		t.Fatal(err)
	}
	for _, runID := range []string{"missing", "file-run", "link-run"} {
		t.Run(runID, func(t *testing.T) {
			_, err := Export(context.Background(), Options{Root: root, RunID: runID, Destination: filepath.Join(t.TempDir(), "bundle.zip")})
			if !errors.Is(err, ErrUsage) {
				t.Fatalf("Export error = %v, want run refusal", err)
			}
		})
	}
}

func TestExportRejectsLiveScheduler(t *testing.T) {
	root, runDir := makeRunStore(t)
	lease, err := engine.AcquireRunLease(runDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	destination := filepath.Join(t.TempDir(), "bundle.zip")
	_, err = Export(context.Background(), Options{Root: root, RunID: "run-1", Destination: destination})
	if !errors.Is(err, ErrOperational) {
		t.Fatalf("Export error = %v, want live scheduler refusal", err)
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("live export created destination: %v", err)
	}
}

func TestConfinedInventoryRecheckDetectsSelectedChange(t *testing.T) {
	_, runDir := makeRunStore(t)
	before, err := collectInventory(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "journal.jsonl"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := before.Recheck(runDir); !errors.Is(err, ErrOperational) {
		t.Fatalf("Recheck error = %v, want selected-change refusal", err)
	}
}

func TestExportInventoryFailureReleasesLease(t *testing.T) {
	root, runDir := makeRunStore(t)
	if err := os.Remove(filepath.Join(runDir, "workflow.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("outside", filepath.Join(runDir, "workflow.json")); err != nil {
		t.Fatal(err)
	}
	_, err := Export(context.Background(), Options{Root: root, RunID: fixtureRunID, Destination: filepath.Join(t.TempDir(), "bundle.zip")})
	if !errors.Is(err, ErrOperational) {
		t.Fatalf("Export error = %v, want inventory refusal", err)
	}
	lease, err := engine.AcquireRunLease(runDir)
	if err != nil {
		t.Fatalf("lease remained held after inventory failure: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExportRejectsSeparateProcessLease(t *testing.T) {
	if os.Getenv("JIG_EXPORT_LOCK_HELPER") == "1" {
		lease, err := engine.AcquireRunLease(os.Getenv("JIG_EXPORT_LOCK_DIR"))
		if err != nil {
			os.Exit(2)
		}
		defer lease.Close()
		_, _ = os.Stdout.WriteString("ready\n")
		var release [1]byte
		_, _ = os.Stdin.Read(release[:])
		return
	}
	root, runDir := makeRunStore(t)
	cmd := exec.Command(os.Args[0], "-test.run=TestExportRejectsSeparateProcessLease")
	cmd.Env = append(os.Environ(), "JIG_EXPORT_LOCK_HELPER=1", "JIG_EXPORT_LOCK_DIR="+runDir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if got, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || got != "ready\n" {
		t.Fatalf("lock helper readiness = %q, %v", got, err)
	}
	destination := filepath.Join(t.TempDir(), "bundle.zip")
	_, err = Export(context.Background(), Options{Root: root, RunID: fixtureRunID, Destination: destination})
	if !errors.Is(err, ErrOperational) {
		t.Fatalf("Export error = %v, want live scheduler refusal", err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	lease, err := engine.AcquireRunLease(runDir)
	if err != nil {
		t.Fatalf("lease did not release after helper exit: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}
