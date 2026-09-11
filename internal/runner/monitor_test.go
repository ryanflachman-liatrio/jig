package runner

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	claudecode "github.com/severity1/claude-agent-sdk-go"
	"jig/internal/sentinel"
)

type fakeMonitorClient struct {
	connectErr   error
	queryErr     error
	messages     chan claudecode.Message
	send         <-chan claudecode.StreamMessage
	disconnected bool
}

func (f *fakeMonitorClient) Connect(context.Context, ...claudecode.StreamMessage) error {
	return f.connectErr
}
func (f *fakeMonitorClient) Disconnect() error { f.disconnected = true; return nil }
func (f *fakeMonitorClient) QueryStream(_ context.Context, ch <-chan claudecode.StreamMessage) error {
	f.send = ch
	return f.queryErr
}
func (f *fakeMonitorClient) ReceiveMessages(context.Context) <-chan claudecode.Message {
	return f.messages
}

func resultMessage(output any, cost *float64) *claudecode.ResultMessage {
	return &claudecode.ResultMessage{StructuredOutput: output, TotalCostUSD: cost}
}

func TestMonitorAdapterIsolationAndLifecycle(t *testing.T) {
	cost := 0.004
	client := &fakeMonitorClient{messages: make(chan claudecode.Message, 1)}
	client.messages <- resultMessage(map[string]any{"flagged": true, "severity": "high", "detail": "entry 2 block 1"}, &cost)
	close(client.messages)
	var options claudecode.Options
	adapter := newMonitorAdapter(func(opts ...claudecode.Option) monitorClient {
		for _, option := range opts {
			option(&options)
		}
		return client
	})
	spec := sentinel.MonitorSpec{Model: monitorModel, Prompt: "system classifier policy"}
	got, err := adapter.Dispatch(context.Background(), spec, "untrusted transcript")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Flagged || !got.CostKnown || !got.Launched || got.CostUSD != cost {
		t.Fatalf("result = %+v", got)
	}
	if !client.disconnected {
		t.Fatal("client was not disconnected")
	}
	if options.SystemPrompt == nil || *options.SystemPrompt != spec.Prompt {
		t.Fatalf("system prompt = %v", options.SystemPrompt)
	}
	if options.MaxTurns != 1 || options.PermissionMode == nil || *options.PermissionMode != claudecode.PermissionModeDefault {
		t.Fatalf("unsafe options: %+v", options)
	}
	if tools, ok := options.Tools.([]string); !ok || tools == nil || len(tools) != 0 {
		t.Fatalf("tools = %#v, want explicit empty slice", options.Tools)
	}
	if options.AllowedTools == nil || len(options.AllowedTools) != 0 {
		t.Fatalf("allowed tools = %#v", options.AllowedTools)
	}
	if options.DisallowedTools == nil || len(options.DisallowedTools) != 0 {
		t.Fatalf("disallowed tools = %#v", options.DisallowedTools)
	}
	if options.SettingSources == nil || len(options.SettingSources) != 0 {
		t.Fatalf("setting sources = %#v", options.SettingSources)
	}
	if skills, ok := options.Skills.([]string); !ok || skills == nil || len(skills) != 0 {
		t.Fatalf("skills = %#v", options.Skills)
	}
	if options.CanUseTool == nil {
		t.Fatal("deny callback was not installed")
	}
	permission, err := options.CanUseTool(context.Background(), "Read", map[string]any{"file_path": "secret"}, claudecode.ToolPermissionContext{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := permission.(claudecode.PermissionResultDeny); !ok {
		t.Fatalf("permission result = %#v, want deny", permission)
	}
	msg, ok := <-client.send
	if !ok {
		t.Fatal("query channel closed before message")
	}
	if !reflect.DeepEqual(msg.Message, map[string]any{"role": "user", "content": "untrusted transcript"}) {
		t.Fatalf("query = %#v", msg.Message)
	}
	if _, open := <-client.send; open {
		t.Fatal("query channel remained open after dispatch")
	}
}

func TestMonitorAdapterStrictVerdictsPreserveCost(t *testing.T) {
	cost := 0.02
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
			messages := make(chan claudecode.Message, 1)
			messages <- resultMessage(tc.output, &cost)
			close(messages)
			client := &fakeMonitorClient{messages: messages}
			adapter := newMonitorAdapter(func(...claudecode.Option) monitorClient { return client })
			got, err := adapter.Dispatch(context.Background(), sentinel.MonitorSpec{Model: monitorModel, Prompt: "p"}, "w")
			if err == nil {
				t.Fatal("expected invalid verdict error")
			}
			if !got.CostKnown || got.CostUSD != cost || !got.Launched {
				t.Fatalf("cost/lifecycle lost: %+v", got)
			}
		})
	}
}

func TestMonitorAdapterTimeoutAndConnectFailure(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		client := &fakeMonitorClient{messages: make(chan claudecode.Message)}
		adapter := newMonitorAdapter(func(...claudecode.Option) monitorClient { return client })
		adapter.timeout = 10 * time.Millisecond
		got, err := adapter.Dispatch(context.Background(), sentinel.MonitorSpec{Model: monitorModel, Prompt: "p"}, "w")
		if !errors.Is(err, context.DeadlineExceeded) || !got.Launched || !client.disconnected {
			t.Fatalf("result=%+v err=%v disconnected=%v", got, err, client.disconnected)
		}
	})
	t.Run("connect", func(t *testing.T) {
		client := &fakeMonitorClient{connectErr: errors.New("offline")}
		adapter := newMonitorAdapter(func(...claudecode.Option) monitorClient { return client })
		got, err := adapter.Dispatch(context.Background(), sentinel.MonitorSpec{Model: monitorModel, Prompt: "p"}, "w")
		if err == nil || got.Launched || client.disconnected {
			t.Fatalf("result=%+v err=%v disconnected=%v", got, err, client.disconnected)
		}
	})
	t.Run("query stream", func(t *testing.T) {
		client := &fakeMonitorClient{queryErr: errors.New("query failed"), messages: make(chan claudecode.Message)}
		adapter := newMonitorAdapter(func(...claudecode.Option) monitorClient { return client })
		got, err := adapter.Dispatch(context.Background(), sentinel.MonitorSpec{Model: monitorModel, Prompt: "p"}, "w")
		if err == nil || got.Launched || !client.disconnected {
			t.Fatalf("result=%+v err=%v disconnected=%v", got, err, client.disconnected)
		}
	})
	t.Run("closed without result", func(t *testing.T) {
		messages := make(chan claudecode.Message)
		close(messages)
		client := &fakeMonitorClient{messages: messages}
		adapter := newMonitorAdapter(func(...claudecode.Option) monitorClient { return client })
		got, err := adapter.Dispatch(context.Background(), sentinel.MonitorSpec{Model: monitorModel, Prompt: "p"}, "w")
		if err == nil || !got.Launched || !client.disconnected {
			t.Fatalf("result=%+v err=%v disconnected=%v", got, err, client.disconnected)
		}
	})
	t.Run("error result keeps cost", func(t *testing.T) {
		cost := 0.03
		messages := make(chan claudecode.Message, 1)
		result := resultMessage(nil, &cost)
		result.IsError = true
		messages <- result
		close(messages)
		client := &fakeMonitorClient{messages: messages}
		adapter := newMonitorAdapter(func(...claudecode.Option) monitorClient { return client })
		got, err := adapter.Dispatch(context.Background(), sentinel.MonitorSpec{Model: monitorModel, Prompt: "p"}, "w")
		if err == nil || !got.Launched || !got.CostKnown || got.CostUSD != cost || !client.disconnected {
			t.Fatalf("result=%+v err=%v disconnected=%v", got, err, client.disconnected)
		}
	})
}
