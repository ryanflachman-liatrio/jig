package acp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
)

func TestCodexACPIntegration(t *testing.T) {
	if os.Getenv("JIG_CODEX_ACP_INTEGRATION") != "1" {
		t.Skip("set JIG_CODEX_ACP_INTEGRATION=1 to run against Codex ACP")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dir := t.TempDir()
	conn, err := ConnectCodex(ctx, nil, nil, nil)
	if err != nil {
		t.Fatalf("ConnectCodex: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	sessionID, err := conn.NewSession(ctx, dir)
	if err != nil {
		t.Fatalf("NewSession: %v (log in with codex login first)", err)
	}
	if !conn.SupportsLoadSession {
		t.Fatal("Codex ACP did not advertise session/load")
	}
	modelID, ok := conn.selectOptionIDByCategory(sessionID, acpsdk.SessionConfigOptionCategoryModel)
	if !ok {
		t.Fatal("Codex ACP did not advertise a model session option")
	}
	models, ok := conn.selectOptions(sessionID, modelID)
	if !ok || len(models) == 0 {
		t.Fatal("Codex ACP did not advertise a model session option")
	}
	if err := conn.SetSelectConfigByCategory(ctx, sessionID, acpsdk.SessionConfigOptionCategoryModel, models[0]); err != nil {
		t.Fatalf("SetSelectConfigByCategory(model): %v", err)
	}
	effortID, ok := conn.selectOptionIDByCategory(sessionID, acpsdk.SessionConfigOptionCategoryThoughtLevel)
	if !ok {
		t.Fatal("Codex ACP did not advertise a reasoning-effort session option")
	}
	efforts, ok := conn.selectOptions(sessionID, effortID)
	if !ok || len(efforts) == 0 {
		t.Fatal("Codex ACP did not advertise a reasoning-effort session option")
	}
	effort := efforts[0]
	if containsConfigValue(efforts, "high") {
		effort = "high"
	}
	t.Logf("selected model=%s effort=%s; advertised efforts=%v", models[0], effort, efforts)
	if err := conn.SetSelectConfigByCategory(ctx, sessionID, acpsdk.SessionConfigOptionCategoryThoughtLevel, effort); err != nil {
		t.Fatalf("SetSelectConfigByCategory(thought level): %v", err)
	}
	if _, err := conn.Prompt(ctx, sessionID, "Reply with exactly: ok"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if len(conn.client.Events()) == 0 {
		t.Fatal("Codex ACP emitted no transcript events")
	}
	const editPath = "telemetry.txt"
	if _, err := conn.Prompt(ctx, sessionID, "Create telemetry.txt containing exactly structured edit telemetry followed by a newline."); err != nil {
		t.Fatalf("Prompt edit: %v", err)
	}
	if content, err := os.ReadFile(dir + "/" + editPath); err != nil || string(content) != "structured edit telemetry\n" {
		t.Fatalf("adapter edit = %q, %v", content, err)
	}
	// ACP tool updates are deltas: edit details may arrive on the initial
	// call, with only a status change in the terminal update.
	editCalls := make(map[string]bool)
	detailedCalls := make(map[string]bool)
	completedCalls := make(map[string]bool)
	for _, event := range conn.client.Events() {
		if event.Kind != EventToolCall && event.Kind != EventToolCallUpdate {
			continue
		}
		if event.ToolKind == "edit" {
			editCalls[event.ToolID] = true
		}
		if len(event.Locations) > 0 {
			detailedCalls[event.ToolID] = true
		}
		for _, content := range event.Content {
			if content.Diff != nil {
				detailedCalls[event.ToolID] = true
			}
		}
		if event.Status == "completed" {
			completedCalls[event.ToolID] = true
		}
	}
	var structuredEdit bool
	for id := range editCalls {
		if detailedCalls[id] && completedCalls[id] {
			structuredEdit = true
		}
	}
	if !structuredEdit {
		for _, event := range conn.client.Events() {
			if event.Kind == EventToolCall || event.Kind == EventToolCallUpdate {
				t.Logf("tool event: type=%s id=%s status=%s kind=%s content=%d locations=%d", event.Kind, event.ToolID, event.Status, event.ToolKind, len(event.Content), len(event.Locations))
			}
		}
		t.Fatal("Codex ACP made the edit but did not emit standard structured edit telemetry for a completed call")
	}

	// Close deliberately kills the adapter process group and returns Wait's
	// exit status; a signal exit is expected here.
	if err := conn.Close(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != -1 {
			t.Fatalf("Close initial connection: %v", err)
		}
	}

	conn, err = ConnectCodex(ctx, nil, nil, nil)
	if err != nil {
		t.Fatalf("ReconnectCodex: %v", err)
	}
	defer conn.Close()
	if err := conn.LoadSession(ctx, dir, sessionID); err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if _, err := conn.Prompt(ctx, sessionID, "Reply with exactly: resumed"); err != nil {
		t.Fatalf("Prompt resumed session: %v", err)
	}
}
