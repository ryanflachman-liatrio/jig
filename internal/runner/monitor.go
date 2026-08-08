package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	claudecode "github.com/severity1/claude-agent-sdk-go"

	"jig/internal/sentinel"
	"jig/internal/workflow"
)

// monitorJSONSchema is the structured-output contract for all Tier-2 monitor
// agents. Monitors must emit exactly these three fields; no base schema is
// applied (unlike regular agent steps, which always carry summary/status).
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

// MonitorAdapter implements sentinel.MonitorDispatcher by calling the Claude
// Agent SDK directly. Each Dispatch is a single-turn, tools-off, persistence-off
// invocation; it does not go through AgentExecutor because monitors require a
// custom JSON schema that differs from the base schema all agent steps carry.
type MonitorAdapter struct{}

// NewMonitorAdapter returns a MonitorAdapter. It holds no state and is safe for
// concurrent Dispatch calls from the supervisor.
func NewMonitorAdapter() *MonitorAdapter { return &MonitorAdapter{} }

// Dispatch runs the monitor agent file against the transcript window text and
// returns whether the monitor flagged a finding.
func (a *MonitorAdapter) Dispatch(ctx context.Context, monitorFile, windowText string) (sentinel.MonitorResult, error) {
	data, err := os.ReadFile(monitorFile)
	if err != nil {
		return sentinel.MonitorResult{}, fmt.Errorf("read monitor %q: %w", monitorFile, err)
	}
	model, prompt, err := workflow.ParseAgentFileContent(data)
	if err != nil {
		return sentinel.MonitorResult{}, fmt.Errorf("parse monitor %q: %w", monitorFile, err)
	}
	// Short model aliases used in monitor frontmatter (e.g. "haiku") map to the
	// current Haiku model ID. Non-empty full IDs pass through unchanged.
	if model == "" || model == "haiku" {
		model = "claude-haiku-4-5-20251001"
	}

	client := claudecode.NewClient(
		claudecode.WithModel(model),
		claudecode.WithIncludePartialMessages(true),
		claudecode.WithJSONSchema(monitorJSONSchema),
		claudecode.WithPermissionMode(claudecode.PermissionModeDefault),
		claudecode.WithMaxTurns(1), // classifiers are single-turn
	)
	if err := client.Connect(ctx); err != nil {
		return sentinel.MonitorResult{}, fmt.Errorf("monitor connect: %w", err)
	}
	defer func() { _ = client.Disconnect() }()

	// The monitor file body is the system-context section of the query; the
	// transcript window is the data to analyze. Separating them with a delimiter
	// makes the boundary explicit so the monitor can treat window content as data.
	query := prompt
	if query != "" {
		query += "\n\n---\n\n"
	}
	query += windowText

	msgChan := client.ReceiveMessages(ctx)
	// Monitors are single-turn; close the send channel immediately so the SDK
	// does not wait for injected tool results.
	sendCh := make(chan claudecode.StreamMessage, 1)
	if err := client.QueryStream(ctx, sendCh); err != nil {
		close(sendCh)
		return sentinel.MonitorResult{}, fmt.Errorf("monitor query stream: %w", err)
	}
	close(sendCh)

	if err := client.Query(ctx, query); err != nil {
		return sentinel.MonitorResult{}, fmt.Errorf("monitor query: %w", err)
	}

	return drainMonitorChannel(msgChan)
}

// drainMonitorChannel reads the SDK message stream and extracts the monitor's
// structured verdict from the ResultMessage.
func drainMonitorChannel(msgChan <-chan claudecode.Message) (sentinel.MonitorResult, error) {
	var result sentinel.MonitorResult
	start := time.Now()
	_ = start

	for msg := range msgChan {
		rm, ok := msg.(*claudecode.ResultMessage)
		if !ok {
			continue
		}
		if rm.TotalCostUSD != nil {
			result.CostUSD = *rm.TotalCostUSD
		}
		if rm.IsError {
			return result, fmt.Errorf("monitor agent error: %s", rm.Subtype)
		}
		if rm.StructuredOutput == nil {
			return result, fmt.Errorf("monitor returned no structured output")
		}
		raw, err := json.Marshal(rm.StructuredOutput)
		if err != nil {
			return result, fmt.Errorf("marshal monitor output: %w", err)
		}
		var out struct {
			Flagged  bool   `json:"flagged"`
			Severity string `json:"severity"`
			Detail   string `json:"detail"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return result, fmt.Errorf("parse monitor output: %w", err)
		}
		result.Flagged = out.Flagged
		result.Severity = out.Severity
		result.Detail = out.Detail
		return result, nil
	}
	return result, fmt.Errorf("monitor channel closed without ResultMessage")
}
