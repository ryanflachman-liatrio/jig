package acp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResourceSnapshotJSONIncludesUnavailableValues(t *testing.T) {
	snapshot := ResourceSnapshot{RootPID: 42, CaptureError: "unavailable"}
	body, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"\"root_pid\":42", "\"process_group_process_count\":0", "\"rss_bytes\":0", "\"open_fd_count\":0", "\"capture_error\":\"unavailable\""} {
		if !strings.Contains(string(body), want) {
			t.Errorf("snapshot JSON missing %s: %s", want, body)
		}
	}
}

func TestSamplerCanBeInjected(t *testing.T) {
	log, err := newDiagnosticLog(t.TempDir(), func(pid int) ResourceSnapshot { return ResourceSnapshot{RootPID: pid, CaptureError: "fixed"} })
	if err != nil {
		t.Fatal(err)
	}
	log.setRootPID(99)
	log.Event("sampled", nil)
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
}
