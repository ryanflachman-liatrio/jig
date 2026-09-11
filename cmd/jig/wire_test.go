package main

import (
	"bytes"
	"context"
	"testing"

	"jig/internal/notification"
	"jig/internal/workflow"
)

func TestNewManagerUsesPortableBuiltinRoster(t *testing.T) {
	t.Chdir(t.TempDir())
	mgr, err := newManager("")
	if err != nil {
		t.Fatal(err)
	}
	if mgr == nil {
		t.Fatal("newManager returned nil")
	}
}

func TestNewRuntimeSharesOneDispatcher(t *testing.T) {
	t.Chdir(t.TempDir())
	rt, err := NewRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	if rt.Manager == nil || rt.Dispatcher == nil || rt.Diagnostics == nil || rt.Lifecycle == nil {
		t.Fatalf("runtime missing components: %+v", rt)
	}
	// Close is idempotent.
	rt.Close(context.Background())
	rt.Close(context.Background())
}

func TestRuntimePrepareRunRecordsPolicy(t *testing.T) {
	t.Chdir(t.TempDir())
	rt, err := NewRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(context.Background())
	policy := workflow.NotificationPolicy{
		Events: []workflow.NotificationEvent{workflow.RunFailed},
		Routes: []workflow.NotificationRoute{{Destination: "ops", Events: []workflow.NotificationEvent{workflow.RunFailed}}},
	}
	rt.PrepareRun("run-1", policy)
	got := rt.ResolvedPolicyForRun("run-1")
	if len(got.Events) != 1 || got.Events[0] != workflow.RunFailed {
		t.Fatalf("policy not retained: %+v", got)
	}
	rt.ReleaseRun("run-1")
	got = rt.ResolvedPolicyForRun("run-1")
	if len(got.Events) != 0 {
		t.Fatalf("policy not released: %+v", got)
	}
}

// TestDrainDiagnosticsToIsSilentWhenEmpty proves FR-17's "byte-for-byte
// compatible" invariant: a run with no notification diagnostics must not
// write anything to headless stderr. Regression test for a prior bug where
// Render's fixed "no notification diagnostics" fallback string was
// misinterpreted as non-empty, printing noise on every ordinary run.
func TestDrainDiagnosticsToIsSilentWhenEmpty(t *testing.T) {
	t.Chdir(t.TempDir())
	rt, err := NewRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(context.Background())

	var buf bytes.Buffer
	rt.DrainDiagnosticsTo(&buf)
	if buf.Len() != 0 {
		t.Fatalf("expected silent stderr for an empty diagnostic store, got %q", buf.String())
	}
}

// TestDrainDiagnosticsToRendersWhenPresent proves the complementary case: a
// recorded diagnostic is actually drained to the writer.
func TestDrainDiagnosticsToRendersWhenPresent(t *testing.T) {
	t.Chdir(t.TempDir())
	rt, err := NewRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(context.Background())

	rt.Diagnostics.Record(notification.Diagnostic{
		Alias:   "ops",
		RunID:   "run-1",
		Outcome: "failed",
		Reason:  "timeout",
	})

	var buf bytes.Buffer
	rt.DrainDiagnosticsTo(&buf)
	if buf.Len() == 0 {
		t.Fatal("expected diagnostic output, got none")
	}
	if got := buf.String(); !bytes.Contains([]byte(got), []byte("ops")) {
		t.Fatalf("drained output missing alias: %q", got)
	}
}
