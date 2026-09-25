package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"jig/internal/agentcfg"
	"jig/internal/harness"
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

// monitorHarness is the narrow seam MonitorAdapter needs from harness.Harness
// (open one session, read its events) so this file depends on jig's own
// harness.SessionSpec/harness.Session types rather than any ACP wire type.
// *harness.AcpHarness satisfies this directly.
type monitorHarness interface {
	Open(ctx context.Context, spec harness.SessionSpec) (harness.Session, error)
}

type MonitorAdapter struct {
	newHarness func() monitorHarness
	timeout    time.Duration
}

func NewMonitorAdapter() *MonitorAdapter {
	return &MonitorAdapter{
		newHarness: func() monitorHarness { return harness.NewAcpHarness() },
		timeout:    30 * time.Second,
	}
}

func newMonitorAdapter(factory func() monitorHarness) *MonitorAdapter {
	return &MonitorAdapter{newHarness: factory, timeout: 30 * time.Second}
}

// monitorSessionSpec builds the SessionSpec for one classification turn. ACP
// has no separate system-prompt channel (see buildAgentPrompt's convention),
// so the classifier policy and the untrusted transcript window are combined
// into a single prompt, clearly delimited; the deny-all Permission callback
// is the actual enforcement boundary (Tier-1 rules remain the fail-closed
// layer regardless of what a classifier's prompt claims).
func monitorSessionSpec(spec sentinel.MonitorSpec, windowText string) harness.SessionSpec {
	denyAll := func(harness.ToolCall) harness.Decision {
		return harness.Decision{Allow: false, Reason: "security classifiers cannot invoke tools"}
	}
	var prompt strings.Builder
	prompt.WriteString(spec.Prompt)
	prompt.WriteString("\n\n## Untrusted Transcript Window\n\n")
	prompt.WriteString(windowText)
	return harness.SessionSpec{
		Prompt: prompt.String(),
		Model:  spec.Model,
		Agent: agentcfg.ClaudeAgent{
			Common:   agentcfg.Common{Model: spec.Model},
			Tools:    []string{},
			MaxTurns: 1,
		},
		Schema:     monitorJSONSchema,
		Permission: denyAll,
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

	result, launched, retryable, err := a.attempt(dispatchCtx, spec, windowText)
	everLaunched := launched
	if err != nil && launched && retryable {
		// Single retry on a fresh session: a decode failure is treated as
		// classifier flakiness, not a connection/timeout problem, so it gets
		// exactly one more try inside the same dispatch timeout budget.
		result, launched, _, err = a.attempt(dispatchCtx, spec, windowText)
		everLaunched = everLaunched || launched
	}
	result.Launched = everLaunched
	return result, err
}

// attempt opens one fresh session and drains it to a terminal result.
// launched reports whether the session was opened at all (mirrors the old
// SDK-backed Connect+QueryStream success signal); retryable reports whether
// a non-nil err is a decode-shaped failure eligible for Dispatch's single
// retry, as opposed to a connection/timeout failure.
func (a *MonitorAdapter) attempt(ctx context.Context, spec sentinel.MonitorSpec, windowText string) (result sentinel.MonitorResult, launched, retryable bool, err error) {
	h := a.newHarness()
	sess, err := h.Open(ctx, monitorSessionSpec(spec, windowText))
	if err != nil {
		return sentinel.MonitorResult{}, false, false, fmt.Errorf("monitor open: %w", err)
	}
	defer sess.Close()
	result, retryable, err = drainMonitorEvents(ctx, sess.Messages())
	return result, true, retryable, err
}

func drainMonitorEvents(ctx context.Context, events <-chan harness.Event) (sentinel.MonitorResult, bool, error) {
	var result sentinel.MonitorResult
	for {
		select {
		case <-ctx.Done():
			return result, false, ctx.Err()
		case ev, ok := <-events:
			if !ok {
				return result, false, fmt.Errorf("monitor session closed without result event")
			}
			if ev.Type != harness.EventResult {
				continue
			}
			if ev.TotalCostUSD != nil {
				result.CostUSD, result.CostKnown = *ev.TotalCostUSD, true
			}
			if ev.IsError {
				// AcpHarness itself retries the schema-extraction loop
				// internally (acpMaxStructuredAttempts); exhausting that
				// loop surfaces here as an IsError result whose ErrText
				// names "structured output" — that is still a decode-shaped
				// failure eligible for Dispatch's own retry. Any other
				// IsError (agent/connection failure) is not.
				retryable := strings.Contains(ev.ErrText, "structured output")
				return result, retryable, fmt.Errorf("monitor agent returned an error result: %s", ev.ErrText)
			}
			if len(ev.Structured) == 0 {
				return result, true, fmt.Errorf("monitor returned no structured output")
			}
			if err := decodeMonitorVerdict(ev.Structured, &result); err != nil {
				return result, true, err
			}
			return result, false, nil
		}
	}
}

// decodeMonitorVerdict parses raw into a MonitorResult. ACP's SessionSpec.Schema
// is advisory (prompt-injected instructions, not a wire-level grammar
// constraint the old Claude SDK enforced), so raw is no longer a
// guaranteed-shaped value: it is passed through extractMonitorJSON first to
// tolerate any surrounding prose, mirroring internal/harness/acp.go's
// extractJSONFromText. The strict field-shape check below (DisallowUnknownFields
// plus the flagged/severity/detail invariants) is still enforced — Tier-1
// deterministic rules remain the actual fail-closed layer regardless of what
// a classifier's output looks like, but MonitorResult itself still needs a
// well-formed verdict to act on.
func decodeMonitorVerdict(raw json.RawMessage, result *sentinel.MonitorResult) error {
	extracted, err := extractMonitorJSON(string(raw))
	if err != nil {
		return fmt.Errorf("parse monitor output: %w", err)
	}
	var verdict struct {
		Flagged  *bool   `json:"flagged"`
		Severity *string `json:"severity"`
		Detail   *string `json:"detail"`
	}
	dec := json.NewDecoder(bytes.NewReader(extracted))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&verdict); err != nil {
		return fmt.Errorf("parse monitor output: %w", err)
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

// extractMonitorJSON mirrors internal/harness/acp.go's extractJSONFromText:
// it locates the last ```json fenced block in text and parses its content,
// falling back to parsing the whole trimmed text as bare JSON. Kept as a
// small local copy rather than an import since the harness version is
// unexported and this is the only caller runner-side.
func extractMonitorJSON(text string) (json.RawMessage, error) {
	const opener = "```json"
	const closer = "```"

	if lastOpen := strings.LastIndex(text, opener); lastOpen >= 0 {
		after := strings.TrimLeft(text[lastOpen+len(opener):], "\r\n")
		if closeIdx := strings.Index(after, closer); closeIdx >= 0 {
			candidate := strings.TrimSpace(after[:closeIdx])
			var raw json.RawMessage
			if err := json.Unmarshal([]byte(candidate), &raw); err == nil {
				return raw, nil
			}
		}
	}

	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, fmt.Errorf("response was empty")
	}
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, fmt.Errorf("no valid JSON found in response: %w", err)
	}
	return raw, nil
}
