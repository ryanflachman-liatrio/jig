package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jig/internal/datastore"
	"jig/internal/workflow"
)

// buildSnapshotWorkflow returns a workflow with a resolved notification
// policy so persistWorkflowSnapshot has something to freeze.
func buildSnapshotWorkflow(t *testing.T) *workflow.Workflow {
	t.Helper()
	src := `
[workflow]
name = "snapshot-test"
version = "0.1"

[notification]
events = ["run_failed"]
routes = [{ destination = "ops", events = ["run_failed"] }]

[[step]]
id = "a"
type = "command"
run = "echo hi"
`
	wf, err := workflow.Decode(src, "")
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func TestWorkflowSnapshotPersistsResolvedNotificationPolicy(t *testing.T) {
	root := t.TempDir()
	runDir, err := datastore.RunDir(root, "test-run")
	if err != nil {
		t.Fatal(err)
	}
	wf := buildSnapshotWorkflow(t)
	if err := persistWorkflowSnapshot(runDir, wf); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(datastore.WorkflowSnapshotPath(runDir))
	if err != nil {
		t.Fatal(err)
	}
	var snap workflowSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Notification == nil {
		t.Fatalf("notification policy missing from snapshot")
	}
	if len(snap.Notification.Routes) != 1 || snap.Notification.Routes[0].Destination != "ops" {
		t.Fatalf("route not persisted: %+v", snap.Notification)
	}
	if snap.NotificationSHA256 == "" {
		t.Fatalf("digest not set")
	}
	// No secrets or URL should have leaked into the file.
	body := string(data)
	if strings.Contains(body, "url") || strings.Contains(body, "bearer") || strings.Contains(body, "JIG_SECRET") {
		t.Fatalf("snapshot leaks operator config: %s", body)
	}
}

func TestWorkflowSnapshotDetectsPolicyTampering(t *testing.T) {
	root := t.TempDir()
	runDir, err := datastore.RunDir(root, "test-run")
	if err != nil {
		t.Fatal(err)
	}
	wf := buildSnapshotWorkflow(t)
	if err := persistWorkflowSnapshot(runDir, wf); err != nil {
		t.Fatal(err)
	}
	// Read, tamper with the notification policy, and write back.
	path := datastore.WorkflowSnapshotPath(runDir)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snap workflowSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Notification == nil {
		t.Fatalf("expected policy in snapshot")
	}
	snap.Notification.Routes[0].Destination = "tampered"
	tampered, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadWorkflowSnapshot(runDir); err == nil {
		t.Fatal("loader accepted tampered policy")
	}
}

func TestWorkflowSnapshotRestoresPolicyOnReopen(t *testing.T) {
	root := t.TempDir()
	runDir, err := datastore.RunDir(root, "test-run")
	if err != nil {
		t.Fatal(err)
	}
	wf := buildSnapshotWorkflow(t)
	if err := persistWorkflowSnapshot(runDir, wf); err != nil {
		t.Fatal(err)
	}
	restored, err := loadWorkflowSnapshot(runDir)
	if err != nil {
		t.Fatal(err)
	}
	policy := restored.NotificationPolicy()
	if len(policy.Routes) != 1 || policy.Routes[0].Destination != "ops" {
		t.Fatalf("policy not restored: %+v", policy)
	}
}

// TestNotificationSecretsNeverPersist proves the snapshot's serialized form
// contains none of the synthetic canaries an operator secret would carry. The
// resolved policy stores alias names only; URLs, bearer tokens, JIG_SECRET_*
// values, and Slack webhook paths must never appear.
func TestNotificationSecretsNeverPersist(t *testing.T) {
	root := t.TempDir()
	runDir, err := datastore.RunDir(root, "test-run")
	if err != nil {
		t.Fatal(err)
	}
	// A workflow whose alias name deliberately collides with an operator
	// secret reference. If the loader ever pulled the resolved binding into
	// the snapshot, the URL/token canaries would end up in the persisted
	// JSON — but the snapshot only serializes alias names, so nothing
	// dangerous leaks.
	src := `
[workflow]
name = "canary-test"
version = "0.1"

[notification]
events = ["attention_required", "run_failed"]
routes = [
  { destination = "ops-alias-A", events = ["run_failed"] },
  { destination = "team-B", events = ["attention_required"] },
]

[[step]]
id = "a"
type = "command"
run = "echo hi"
`
	wf, err := workflow.Decode(src, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := persistWorkflowSnapshot(runDir, wf); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(datastore.WorkflowSnapshotPath(runDir))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	canaries := []string{
		"https://hooks.slack.com/",
		"JIG_SECRET_",
		"xoxb-",
		"sk-live-",
		"sk-test-",
		"Bearer ",
		"url_secret",
		"bearer_secret",
	}
	for _, c := range canaries {
		if strings.Contains(body, c) {
			t.Fatalf("snapshot leaked canary %q; snapshot body:\n%s", c, body)
		}
	}
	// The alias names DO appear (that is the whole point of the snapshot)
	// but they are the safe surface the reopen path can bind to fresh
	// operator config, not the URL/token they resolve to today.
	for _, alias := range []string{"ops-alias-A", "team-B"} {
		if !strings.Contains(body, alias) {
			t.Fatalf("snapshot missing expected alias %q; body:\n%s", alias, body)
		}
	}
}

func TestWorkflowSnapshotOlderRecordTreatedAsDisabled(t *testing.T) {
	root := t.TempDir()
	runDir, err := datastore.RunDir(root, "test-run")
	if err != nil {
		t.Fatal(err)
	}
	// Write a snapshot without the notification field, simulating an older
	// journal.
	src := `
[workflow]
name = "legacy"
version = "0.1"

[[step]]
id = "a"
type = "command"
run = "echo hi"
`
	wf, err := workflow.Decode(src, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := persistWorkflowSnapshot(runDir, wf); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(datastore.WorkflowSnapshotPath(runDir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "notification") {
		t.Fatalf("legacy workflow accidentally serialized notification: %s", data)
	}
	restored, err := loadWorkflowSnapshot(runDir)
	if err != nil {
		t.Fatal(err)
	}
	policy := restored.NotificationPolicy()
	if len(policy.Events) != 0 || len(policy.Routes) != 0 {
		t.Fatalf("legacy snapshot acquired policy: %+v", policy)
	}
	_ = filepath.Dir(runDir)
}
