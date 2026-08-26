package acp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixedSnapshot(pid int) ResourceSnapshot {
	return ResourceSnapshot{Timestamp: time.Unix(1, 0).UTC(), RootPID: pid, ProcessGroupID: pid, ProcessGroupCount: 2, RSSBytes: 4096, OpenFDCount: 7}
}

func TestDiagnosticLogSeparatesJSONLifecycleFromStderr(t *testing.T) {
	dir := t.TempDir()
	log, err := newDiagnosticLog(dir, fixedSnapshot)
	if err != nil {
		t.Fatalf("newDiagnosticLog: %v", err)
	}
	log.setRootPID(42)
	log.Event("prompt_started", map[string]any{"prompt_bytes": 42})
	if _, err := log.StderrWriter().Write([]byte("adapter warning\n")); err != nil {
		t.Fatalf("write stderr: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("close log: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, "acp-diagnostics.jsonl"))
	if err != nil {
		t.Fatalf("read diagnostics: %v", err)
	}
	var event lifecycleEvent
	if err := json.Unmarshal(body, &event); err != nil {
		t.Fatalf("decode lifecycle event: %v\n%s", err, body)
	}
	if event.Event != "prompt_started" || event.Fields["prompt_bytes"] != float64(42) || event.Resource.RootPID != 42 {
		t.Fatalf("event = %#v", event)
	}
	if strings.Contains(string(body), "adapter warning") {
		t.Fatalf("lifecycle artifact contains stderr: %s", body)
	}
	stderr, err := os.ReadFile(filepath.Join(dir, "acp-adapter.stderr.log"))
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if string(stderr) != "adapter warning\n" {
		t.Errorf("stderr = %q", stderr)
	}
	for _, name := range []string{"acp-diagnostics.jsonl", "acp-adapter.stderr.log"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s permissions = %o, want 600", name, info.Mode().Perm())
		}
	}
}

func TestDiagnosticLogNoopsWithoutPersistence(t *testing.T) {
	log, err := newDiagnosticLog("", fixedSnapshot)
	if err != nil {
		t.Fatalf("newDiagnosticLog: %v", err)
	}
	log.Event("prompt_started", map[string]any{"prompt_bytes": 1})
	if _, err := log.StderrWriter().Write([]byte("adapter warning")); err != nil {
		t.Fatalf("write stderr: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestDiagnosticLogRetainsBoundedStderrTail(t *testing.T) {
	log, err := newDiagnosticLog("", fixedSnapshot)
	if err != nil {
		t.Fatalf("newDiagnosticLog: %v", err)
	}
	input := strings.Repeat("x", diagnosticTailBytes+10) + "tail"
	if _, err := log.StderrWriter().Write([]byte(input)); err != nil {
		t.Fatalf("write stderr: %v", err)
	}
	if got := log.StderrTail(); !strings.HasSuffix(got, "tail") || len(got) != diagnosticTailBytes {
		t.Errorf("stderr tail = %d bytes ending %q, want %d bytes ending tail", len(got), got[max(0, len(got)-4):], diagnosticTailBytes)
	}
}

func TestDiagnosticLogSerializesConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	log, err := newDiagnosticLog(dir, fixedSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	const writers = 16
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Event("concurrent", nil)
			_, _ = log.StderrWriter().Write([]byte("stderr\n"))
		}()
	}
	wg.Wait()
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "acp-diagnostics.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != writers {
		t.Fatalf("events = %d, want %d", len(lines), writers)
	}
	for _, line := range lines {
		var event lifecycleEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid JSON line %q: %v", line, err)
		}
	}
}
