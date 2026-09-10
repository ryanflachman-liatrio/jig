package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	claudecode "github.com/severity1/claude-agent-sdk-go"

	"jig/internal/sentinel"
)

var monitorJSONSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"flagged":  map[string]any{"type": "boolean"},
		"severity": map[string]any{"type": "string", "enum": []any{"low", "medium", "high", "critical"}},
		"detail":   map[string]any{"type": "string"},
	},
	"required":             []any{"flagged", "severity", "detail"},
	"additionalProperties": false,
}

type monitorClient interface {
	Connect(context.Context, ...claudecode.StreamMessage) error
	Disconnect() error
	QueryStream(context.Context, <-chan claudecode.StreamMessage) error
	ReceiveMessages(context.Context) <-chan claudecode.Message
}

type monitorClientFactory func(...claudecode.Option) monitorClient

type MonitorAdapter struct {
	newClient monitorClientFactory
	timeout   time.Duration
}

func NewMonitorAdapter() *MonitorAdapter {
	return &MonitorAdapter{newClient: func(opts ...claudecode.Option) monitorClient {
		return claudecode.NewClient(opts...)
	}, timeout: 30 * time.Second}
}

func newMonitorAdapter(factory monitorClientFactory) *MonitorAdapter {
	return &MonitorAdapter{newClient: factory, timeout: 30 * time.Second}
}

func monitorOptions(spec sentinel.MonitorSpec) []claudecode.Option {
	emptySurface := func(o *claudecode.Options) {
		o.Tools = []string{}
		o.AllowedTools = []string{}
		o.DisallowedTools = []string{}
		o.SettingSources = []claudecode.SettingSource{}
	}
	denyTools := claudecode.WithCanUseTool(func(context.Context, string, map[string]any, claudecode.ToolPermissionContext) (claudecode.PermissionResult, error) {
		return claudecode.NewPermissionResultDeny("security classifiers cannot invoke tools"), nil
	})
	return []claudecode.Option{
		emptySurface,
		claudecode.WithSkillsDisabled(),
		claudecode.WithModel(spec.Model),
		claudecode.WithSystemPrompt(spec.Prompt),
		claudecode.WithJSONSchema(monitorJSONSchema),
		claudecode.WithPermissionMode(claudecode.PermissionModeDefault),
		claudecode.WithMaxTurns(1),
		denyTools,
	}
}

func (a *MonitorAdapter) Dispatch(ctx context.Context, spec sentinel.MonitorSpec, windowText string) (sentinel.MonitorResult, error) {
	if spec.Model == "" || spec.Prompt == "" {
		return sentinel.MonitorResult{}, fmt.Errorf("monitor definition is incomplete")
	}
	timeout := a.timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	dispatchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := a.newClient(monitorOptions(spec)...)
	if err := client.Connect(dispatchCtx); err != nil {
		return sentinel.MonitorResult{}, fmt.Errorf("monitor connect: %w", err)
	}
	defer client.Disconnect()

	messages := client.ReceiveMessages(dispatchCtx)
	sendCh := make(chan claudecode.StreamMessage, 1)
	var closeOnce sync.Once
	closeSend := func() { closeOnce.Do(func() { close(sendCh) }) }
	defer closeSend()
	if err := client.QueryStream(dispatchCtx, sendCh); err != nil {
		return sentinel.MonitorResult{}, fmt.Errorf("monitor query stream: %w", err)
	}
	sendCh <- claudecode.StreamMessage{
		Type:    "user",
		Message: map[string]any{"role": "user", "content": windowText},
	}
	result, err := drainMonitorChannel(dispatchCtx, messages)
	result.Launched = true
	return result, err
}

func drainMonitorChannel(ctx context.Context, messages <-chan claudecode.Message) (sentinel.MonitorResult, error) {
	var result sentinel.MonitorResult
	for {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case msg, ok := <-messages:
			if !ok {
				return result, fmt.Errorf("monitor channel closed without ResultMessage")
			}
			rm, ok := msg.(*claudecode.ResultMessage)
			if !ok {
				continue
			}
			if rm.TotalCostUSD != nil {
				result.CostUSD, result.CostKnown = *rm.TotalCostUSD, true
			}
			if rm.IsError {
				return result, fmt.Errorf("monitor agent returned an error result")
			}
			if rm.StructuredOutput == nil {
				return result, fmt.Errorf("monitor returned no structured output")
			}
			if err := decodeMonitorVerdict(rm.StructuredOutput, &result); err != nil {
				return result, err
			}
			return result, nil
		}
	}
}

func decodeMonitorVerdict(value any, result *sentinel.MonitorResult) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal monitor output: %w", err)
	}
	var verdict struct {
		Flagged  *bool   `json:"flagged"`
		Severity *string `json:"severity"`
		Detail   *string `json:"detail"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&verdict); err != nil {
		return fmt.Errorf("parse monitor output: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("parse monitor output: trailing data")
	}
	if verdict.Flagged == nil || verdict.Severity == nil || verdict.Detail == nil {
		return fmt.Errorf("monitor output is missing required fields")
	}
	switch *verdict.Severity {
	case "low", "medium", "high", "critical":
	default:
		return fmt.Errorf("monitor output has unknown severity")
	}
	if !*verdict.Flagged && (*verdict.Severity != "low" || *verdict.Detail != "") {
		return fmt.Errorf("unflagged monitor output must use low severity and empty detail")
	}
	result.Flagged, result.Severity, result.Detail = *verdict.Flagged, *verdict.Severity, sentinel.RedactText(*verdict.Detail)
	return nil
}
