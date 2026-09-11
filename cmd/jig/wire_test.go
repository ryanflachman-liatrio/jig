package main

import (
	"context"
	"testing"

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
