package notification

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"jig/internal/workflow"
)

func sampleAttention(count int) []AttentionDescriptor {
	out := make([]AttentionDescriptor, count)
	kinds := []AttentionKind{AttentionReview, AttentionInput, AttentionPrompt, AttentionQuestion, AttentionRecovery, AttentionIntegrationConflict, AttentionFinalMerge}
	for i := range out {
		out[i] = AttentionDescriptor{StepID: attentionStepID(i), Kind: kinds[i%len(kinds)]}
	}
	return out
}

func attentionStepID(i int) string {
	const s = "step-"
	return s + string(rune('a'+i%26))
}

func TestBuildOutboundPayloadJSONShape(t *testing.T) {
	n := Notification{
		ID:        "notif-01",
		Event:     workflow.AttentionRequired,
		Timestamp: time.Date(2026, 9, 11, 15, 4, 0, 0, time.UTC),
		Workflow:  "review-change",
		RunID:     "example-run",
		Attention: []AttentionDescriptor{{StepID: "review", Kind: AttentionReview}},
	}
	p := BuildOutboundPayload(n)
	var decoded map[string]any
	if err := json.Unmarshal(p.JSON, &decoded); err != nil {
		t.Fatalf("payload not valid json: %v", err)
	}
	if decoded["schema_version"].(float64) != float64(SchemaVersion) {
		t.Fatalf("schema_version=%v", decoded["schema_version"])
	}
	if decoded["event"] != string(workflow.AttentionRequired) {
		t.Fatalf("event=%v", decoded["event"])
	}
	if decoded["run_id"] != "example-run" {
		t.Fatalf("run_id=%v", decoded["run_id"])
	}
	items := decoded["attention"].([]any)
	if len(items) != 1 {
		t.Fatalf("attention items=%d", len(items))
	}
	first := items[0].(map[string]any)
	if first["step_id"] != "review" || first["kind"] != "review" {
		t.Fatalf("first=%v", first)
	}
	if first["action"] != AttentionReview.Action() {
		t.Fatalf("action=%v", first["action"])
	}
}

func TestBuildOutboundPayloadCoalescingAndBounds(t *testing.T) {
	n := Notification{
		ID:        "n1",
		Event:     workflow.AttentionRequired,
		Timestamp: time.Now().UTC(),
		Workflow:  "wf",
		RunID:     "r",
		Attention: sampleAttention(25),
	}
	p := BuildOutboundPayload(n)
	if len(p.JSON) > MaxRequestBytes {
		t.Fatalf("json exceeds cap: %d", len(p.JSON))
	}
	var decoded jsonPayload
	if err := json.Unmarshal(p.JSON, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.AttentionCount != MaxDisplayedAttention {
		t.Fatalf("displayed=%d", decoded.AttentionCount)
	}
	if decoded.OmittedCount != 25-MaxDisplayedAttention {
		t.Fatalf("omitted=%d", decoded.OmittedCount)
	}
	if strings.Count(p.SlackBody, "•") != MaxDisplayedAttention {
		t.Fatalf("slack items=%d", strings.Count(p.SlackBody, "•"))
	}
}

func TestPayloadNeverIncludesSecrets(t *testing.T) {
	n := Notification{
		ID:        "n1",
		Event:     workflow.AttentionRequired,
		Timestamp: time.Now().UTC(),
		Workflow:  "wf-name",
		RunID:     "run-01",
		Attention: []AttentionDescriptor{{StepID: "step-<mention>", Kind: AttentionReview}},
	}
	p := BuildOutboundPayload(n)
	if strings.Contains(p.SlackBody, "<mention>") {
		t.Fatalf("slack body did not escape: %q", p.SlackBody)
	}
	if !strings.Contains(p.SlackBody, "&lt;mention&gt;") {
		t.Fatalf("slack body lost escaping: %q", p.SlackBody)
	}
	if strings.Contains(string(p.JSON), "\x1b") {
		t.Fatalf("control leaked: %q", p.JSON)
	}
}

func TestPayloadStripsControlChars(t *testing.T) {
	got := safeString("hello\x1b[31mworld")
	if got != "hello[31mworld" {
		t.Fatalf("safeString=%q", got)
	}
	if truncateRunes("héllo", 3) != "hé…" {
		t.Fatalf("truncateRunes = %q", truncateRunes("héllo", 3))
	}
	if truncateRunes("héllo", 10) != "héllo" {
		t.Fatalf("truncate over max mutated")
	}
}

func TestPayloadTerminalOmitsAttention(t *testing.T) {
	n := Notification{
		ID:        "n1",
		Event:     workflow.RunFailed,
		Timestamp: time.Now().UTC(),
		Workflow:  "wf",
		RunID:     "r",
		Attention: sampleAttention(4),
	}
	p := BuildOutboundPayload(n)
	var decoded jsonPayload
	if err := json.Unmarshal(p.JSON, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Attention) != 0 || decoded.AttentionCount != 0 {
		t.Fatalf("terminal payload leaked attention: %+v", decoded)
	}
}

func TestActionTextIsFixed(t *testing.T) {
	for _, k := range []AttentionKind{AttentionReview, AttentionInput, AttentionPrompt, AttentionQuestion, AttentionRecovery, AttentionIntegrationConflict, AttentionFinalMerge} {
		if k.Action() == "" || k.Action() == "Attention required" {
			t.Fatalf("kind %s has default text", k)
		}
	}
}
