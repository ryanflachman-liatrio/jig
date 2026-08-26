package acp

import (
	"context"
	"os"
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
	conn, err := ConnectCodex(ctx, nil, nil)
	if err != nil {
		t.Fatalf("ConnectCodex: %v", err)
	}
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
	if err := conn.SetSelectConfigByCategory(ctx, sessionID, acpsdk.SessionConfigOptionCategoryThoughtLevel, efforts[0]); err != nil {
		t.Fatalf("SetSelectConfigByCategory(thought level): %v", err)
	}
	if _, err := conn.Prompt(ctx, sessionID, "Reply with exactly: ok"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if len(conn.client.Events()) == 0 {
		t.Fatal("Codex ACP emitted no transcript events")
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close initial connection: %v", err)
	}

	conn, err = ConnectCodex(ctx, nil, nil)
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
