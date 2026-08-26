package acp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestCodexACPParallelism(t *testing.T) {
	if os.Getenv("JIG_CODEX_ACP_INTEGRATION") != "1" {
		t.Skip("set JIG_CODEX_ACP_INTEGRATION=1 to run authenticated Codex ACP probe")
	}
	parallelism := 1
	if raw := os.Getenv("JIG_CODEX_ACP_PARALLELISM"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			t.Fatal("JIG_CODEX_ACP_PARALLELISM must be a positive integer")
		}
		parallelism = parsed
	}
	dir, err := os.MkdirTemp("", "jig-codex-acp-probe-")
	if err != nil {
		t.Fatal("create diagnostics directory")
	}
	succeeded := false
	defer func() {
		if succeeded {
			_ = os.RemoveAll(dir)
		} else {
			t.Logf("Codex ACP diagnostics retained at %s", dir)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	prompt := "Return exactly this JSON object and nothing else: {\"status\":\"complete\"}."
	var wg sync.WaitGroup
	failed := make(chan struct{}, parallelism)
	for i := 0; i < parallelism; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn, err := ConnectCodexWithDiagnostics(ctx, nil, nil, filepath.Join(dir, fmt.Sprintf("research-%d", i)))
			if err != nil {
				failed <- struct{}{}
				return
			}
			defer conn.Close()
			sessionID, err := conn.NewSession(ctx, "")
			if err == nil {
				_, err = conn.Prompt(ctx, sessionID, prompt)
			}
			if err != nil {
				failed <- struct{}{}
			}
		}(i)
	}
	wg.Wait()
	close(failed)
	for range failed {
		t.Fail()
	}
	if t.Failed() {
		return
	}

	conn, err := ConnectCodexWithDiagnostics(ctx, nil, nil, filepath.Join(dir, "synthesis"))
	if err != nil {
		t.FailNow()
	}
	defer conn.Close()
	sessionID, err := conn.NewSession(ctx, "")
	if err != nil {
		t.FailNow()
	}
	if _, err := conn.Prompt(ctx, sessionID, prompt); err != nil {
		t.FailNow()
	}
	succeeded = true
}
