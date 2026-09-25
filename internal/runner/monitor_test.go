package runner

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"jig/internal/agentcfg"
	"jig/internal/harness"
	"jig/internal/sentinel"
)

type fakeMonitorSession struct {
	events chan harness.Event
	closed bool
}

func (f *fakeMonitorSession) Messages() <-chan harness.Event { return f.events }
func (f *fakeMonitorSession) Send(context.Context, harness.ToolResult) error {
	return errors.New("not supported")
}
func (f *fakeMonitorSession) Close() error { f.closed = true; return nil }

// fakeMonitorHarness hands out sessions in order across successive Dispatch
// attempts (the retry path opens a second, independent session) and records
// the SessionSpec passed to each Open call for assertion.
type fakeMonitorHarness struct {
	openErr  error
	sessions []*fakeMonitorSession
	opened   int
	specs    []harness.SessionSpec
}

func (f *fakeMonitorHarness) Open(_ context.Context, spec harness.SessionSpec) (harness.Session, error) {
	f.specs = append(f.specs, spec)
	if f.openErr != nil {
		return nil, f.openErr
	}
	if f.opened >= len(f.sessions) {
		return nil, errors.New("fakeMonitorHarness: no more scripted sessions")
	}
	sess := f.sessions[f.opened]
	f.opened++
	return sess, nil
}

func resultEvent(structured any, cost *float64) harness.Event {
	var raw json.RawMessage
	if structured != nil {
		b, err := json.Marshal(structured)
		if err != nil {
			panic(err)
		}
		raw = b
	}
	return harness.Event{Type: harness.EventResult, Structured: raw, TotalCostUSD: cost}
}

func scriptedSession(events ...harness.Event) *fakeMonitorSession {
	ch := make(chan harness.Event, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)
	return &fakeMonitorSession{events: ch}
}

func adapterWith(h *fakeMonitorHarness) *MonitorAdapter {
	return newMonitorAdapter(func() monitorHarness { return h })
}

func TestMonitorAdapterIsolationAndLifecycle(t *testing.T) {
	h := &fakeMonitorHarness{sessions: []*fakeMonitorSession{
		scriptedSession(resultEvent(map[string]any{"flagged": true, "severity": "high", "detail": "entry 2 block 1"}, nil)),
	}}
	adapter := adapterWith(h)
	spec := sentinel.MonitorSpec{Model: monitorModel, Prompt: "system classifier policy"}
	got, err := adapter.Dispatch(context.Background(), spec, "untrusted transcript")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Flagged || got.Severity != "high" || !got.Launched {
		t.Fatalf("result = %+v", got)
	}
	// ACP never reports cost/usage (spec 12's existing convention for the
	// ACP-backed path); no new cost tracking was added.
	if got.CostKnown || got.CostUSD != 0 {
		t.Fatalf("expected no cost tracking, got %+v", got)
	}
	if len(h.specs) != 1 {
		t.Fatalf("expected exactly one Open call, got %d", len(h.specs))
	}
	if !h.sessions[0].closed {
		t.Fatal("session was not closed")
	}
	sentSpec := h.specs[0]
	if sentSpec.Model != monitorModel {
		t.Fatalf("model = %q", sentSpec.Model)
	}
	claude, ok := sentSpec.Agent.(agentcfg.ClaudeAgent)
	if !ok {
		t.Fatalf("agent = %#v, want a Claude agent", sentSpec.Agent)
	}
	if claude.MaxTurns != 1 || claude.Model != monitorModel {
		t.Fatalf("agent = %#v, want max_turns 1 and the monitor model", claude)
	}
	if claude.Tools == nil || len(claude.Tools) != 0 {
		t.Fatalf("tools = %#v, want explicit empty (no built-in tools)", claude.Tools)
	}
	if sentSpec.Schema == nil {
		t.Fatal("expected schema to be set so AcpHarness injects it into the prompt")
	}
	if sentSpec.Permission == nil {
		t.Fatal("expected a deny-all permission callback")
	}
	decision := sentSpec.Permission("Read", map[string]any{"file_path": "secret"})
	if decision.Allow {
		t.Fatal("permission callback must deny every tool call")
	}
	if !strings.Contains(sentSpec.Prompt, spec.Prompt) || !strings.Contains(sentSpec.Prompt, "untrusted transcript") {
		t.Fatalf("prompt = %q, want it to contain both the policy and the window text", sentSpec.Prompt)
	}
}

func TestMonitorAdapterStrictVerdictsRetryThenFail(t *testing.T) {
	tests := []struct {
		name   string
		output any
	}{
		{"missing", map[string]any{"flagged": false, "severity": "low"}},
		{"malformed", "not a verdict object"},
		{"extra", map[string]any{"flagged": false, "severity": "low", "detail": "", "extra": true}},
		{"wrong type", map[string]any{"flagged": "no", "severity": "low", "detail": ""}},
		{"unknown severity", map[string]any{"flagged": true, "severity": "urgent", "detail": "x"}},
		{"invalid unflagged", map[string]any{"flagged": false, "severity": "high", "detail": "x"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeMonitorHarness{sessions: []*fakeMonitorSession{
				scriptedSession(resultEvent(tc.output, nil)),
				scriptedSession(resultEvent(tc.output, nil)),
			}}
			adapter := adapterWith(h)
			got, err := adapter.Dispatch(context.Background(), sentinel.MonitorSpec{Model: monitorModel, Prompt: "p"}, "w")
			if err == nil {
				t.Fatal("expected invalid verdict error")
			}
			if !got.Launched {
				t.Fatalf("launched = %+v", got)
			}
			if len(h.specs) != 2 {
				t.Fatalf("expected exactly one retry (2 Open calls), got %d", len(h.specs))
			}
		})
	}
}

// TestDecodeMonitorVerdictTolerantOfSurroundingProse demonstrates that
// decodeMonitorVerdict still extracts a valid verdict when its input carries
// prose around the JSON block, since ACP's SessionSpec.Schema is a
// prompt-injected convention, not a wire-level guarantee.
func TestDecodeMonitorVerdictTolerantOfSurroundingProse(t *testing.T) {
	raw := json.RawMessage("Here is my analysis.\n\n```json\n{\"flagged\": true, \"severity\": \"critical\", \"detail\": \"prompt injection attempt\"}\n```\n")
	var result sentinel.MonitorResult
	if err := decodeMonitorVerdict(raw, &result); err != nil {
		t.Fatalf("decodeMonitorVerdict returned error: %v", err)
	}
	if !result.Flagged || result.Severity != "critical" || result.Detail != "prompt injection attempt" {
		t.Fatalf("result = %+v", result)
	}
}

func TestMonitorAdapterDecodeRetryThenFailOpen(t *testing.T) {
	bad := map[string]any{"flagged": false, "severity": "low", "detail": "", "extra": true}
	h := &fakeMonitorHarness{sessions: []*fakeMonitorSession{
		scriptedSession(resultEvent(bad, nil)),
		scriptedSession(resultEvent(bad, nil)),
	}}
	adapter := adapterWith(h)
	got, err := adapter.Dispatch(context.Background(), sentinel.MonitorSpec{Model: monitorModel, Prompt: "p"}, "w")
	if err == nil {
		t.Fatal("expected a normal Dispatch error, not a panic or a new fail-closed branch")
	}
	if !got.Launched {
		t.Fatalf("expected Launched=true so the sentinel fleet's fail-open handling applies: %+v", got)
	}
	if len(h.specs) != 2 {
		t.Fatalf("expected exactly one retry, got %d Open calls", len(h.specs))
	}
}

func TestMonitorAdapterTimeoutAndConnectFailure(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		h := &fakeMonitorHarness{sessions: []*fakeMonitorSession{scriptedSession()}}
		h.sessions[0].events = make(chan harness.Event) // never delivers
		adapter := adapterWith(h)
		adapter.timeout = 10 * time.Millisecond
		got, err := adapter.Dispatch(context.Background(), sentinel.MonitorSpec{Model: monitorModel, Prompt: "p"}, "w")
		if !errors.Is(err, context.DeadlineExceeded) || !got.Launched {
			t.Fatalf("result=%+v err=%v", got, err)
		}
		if len(h.specs) != 1 {
			t.Fatalf("timeout must not retry, got %d Open calls", len(h.specs))
		}
	})
	t.Run("open failure", func(t *testing.T) {
		h := &fakeMonitorHarness{openErr: errors.New("adapter offline")}
		adapter := adapterWith(h)
		got, err := adapter.Dispatch(context.Background(), sentinel.MonitorSpec{Model: monitorModel, Prompt: "p"}, "w")
		if err == nil || got.Launched {
			t.Fatalf("result=%+v err=%v", got, err)
		}
		if len(h.specs) != 1 {
			t.Fatalf("open failure must not retry, got %d Open calls", len(h.specs))
		}
	})
	t.Run("closed without result", func(t *testing.T) {
		h := &fakeMonitorHarness{sessions: []*fakeMonitorSession{scriptedSession()}}
		adapter := adapterWith(h)
		got, err := adapter.Dispatch(context.Background(), sentinel.MonitorSpec{Model: monitorModel, Prompt: "p"}, "w")
		if err == nil || !got.Launched {
			t.Fatalf("result=%+v err=%v", got, err)
		}
		if len(h.specs) != 1 {
			t.Fatalf("a dropped connection is not a decode failure and must not retry, got %d Open calls", len(h.specs))
		}
	})
	t.Run("agent error result", func(t *testing.T) {
		h := &fakeMonitorHarness{sessions: []*fakeMonitorSession{
			scriptedSession(harness.Event{Type: harness.EventResult, IsError: true, ErrText: "agent crashed"}),
		}}
		adapter := adapterWith(h)
		got, err := adapter.Dispatch(context.Background(), sentinel.MonitorSpec{Model: monitorModel, Prompt: "p"}, "w")
		if err == nil || !got.Launched {
			t.Fatalf("result=%+v err=%v", got, err)
		}
		if len(h.specs) != 1 {
			t.Fatalf("a non-decode agent error must not retry, got %d Open calls", len(h.specs))
		}
	})
	t.Run("structured output exhausted retries with acp harness itself", func(t *testing.T) {
		h := &fakeMonitorHarness{sessions: []*fakeMonitorSession{
			scriptedSession(harness.Event{Type: harness.EventResult, IsError: true, ErrText: "acp: structured output: no valid JSON after 3 attempts: no valid JSON found in response"}),
			scriptedSession(harness.Event{Type: harness.EventResult, IsError: true, ErrText: "acp: structured output: no valid JSON after 3 attempts: no valid JSON found in response"}),
		}}
		adapter := adapterWith(h)
		got, err := adapter.Dispatch(context.Background(), sentinel.MonitorSpec{Model: monitorModel, Prompt: "p"}, "w")
		if err == nil || !got.Launched {
			t.Fatalf("result=%+v err=%v", got, err)
		}
		if len(h.specs) != 2 {
			t.Fatalf("AcpHarness's own exhausted structured-output retries are still a decode-shaped failure eligible for Dispatch's single retry, got %d Open calls", len(h.specs))
		}
	})
}
