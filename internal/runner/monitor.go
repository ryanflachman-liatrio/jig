package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/agentcfg"
	"github.com/ryanflachman-liatrio/jig/internal/harness"
	"github.com/ryanflachman-liatrio/jig/internal/sentinel"
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
// Every harness.Harness satisfies this directly.
type monitorHarness interface {
	Open(ctx context.Context, spec harness.SessionSpec) (harness.Session, error)
}

// MonitorAdapter dispatches Tier-2 classifier turns on one configured backend.
// Each attempt opens a fresh session on a fresh harness from newHarness.
type MonitorAdapter struct {
	backend    string
	newHarness func() (monitorHarness, error)
	timeout    time.Duration
}

// NewMonitorAdapter returns an adapter whose sessions run on backend, resolved
// through harness.For.
func NewMonitorAdapter(backend string) *MonitorAdapter {
	return newMonitorAdapter(backend, func() (monitorHarness, error) { return harness.For(backend) })
}

func newMonitorAdapter(backend string, factory func() (monitorHarness, error)) *MonitorAdapter {
	return &MonitorAdapter{backend: backend, newHarness: factory, timeout: 30 * time.Second}
}

// monitorSessionSpec builds the SessionSpec for one classification turn. ACP
// has no separate system-prompt channel (see buildAgentPrompt's convention),
// so the classifier policy and the untrusted transcript window are combined
// into a single prompt, clearly delimited; the deny-all Permission callback
// is the actual enforcement boundary on every backend (Tier-1 rules remain
// the fail-closed layer regardless of what a classifier's prompt claims).
func monitorSessionSpec(backend string, spec sentinel.MonitorSpec, windowText string) harness.SessionSpec {
	denyAll := func(harness.ToolCall) harness.Decision {
		return harness.Decision{Allow: false, Reason: "security classifiers cannot invoke tools"}
	}
	var prompt strings.Builder
	prompt.WriteString(spec.Prompt)
	prompt.WriteString("\n\n## Untrusted Transcript Window\n\n")
	prompt.WriteString(windowText)
	return harness.SessionSpec{
		Prompt:     prompt.String(),
		Model:      spec.Model,
		Agent:      monitorAgent(backend, spec.Model),
		Schema:     monitorJSONSchema,
		Permission: denyAll,
	}
}

// monitorAgent builds the backend's agent for a classifier turn. Claude also
// gets no built-in tools and a single turn; the other backends have no such
// settings, so the deny-all Permission callback alone keeps them tool-less.
func monitorAgent(backend, model string) agentcfg.Agent {
	common := agentcfg.Common{Model: model}
	switch backend {
	case agentcfg.BackendCodex:
		return agentcfg.CodexAgent{Common: common}
	case agentcfg.BackendCursor:
		return agentcfg.CursorAgent{Common: common}
	default:
		return agentcfg.ClaudeAgent{Common: common, Tools: []string{}, MaxTurns: 1}
	}
}

func (a *MonitorAdapter) Dispatch(ctx context.Context, spec sentinel.MonitorSpec, windowText string) (sentinel.MonitorResult, error) {
	// Model may be empty: codex and cursor then use the backend's own default.
	if spec.Prompt == "" {
		return sentinel.MonitorResult{}, fmt.Errorf("monitor definition is incomplete")
	}
	timeout := a.timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	dispatchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := time.Now()
	attempts := 1
	result, launched, retryable, err := a.attempt(dispatchCtx, spec, windowText)
	everLaunched := launched
	if err != nil && launched && retryable {
		// Single retry on a fresh session: a decode failure is treated as
		// classifier flakiness, not a connection/timeout problem, so it gets
		// exactly one more try inside the same dispatch timeout budget.
		attempts++
		result, launched, _, err = a.attempt(dispatchCtx, spec, windowText)
		everLaunched = everLaunched || launched
	}
	result.Launched = everLaunched
	if err != nil {
		info := monitorFailureInfo(err)
		info.Backend, info.Model = a.backend, spec.Model
		cause := err
		for errors.Unwrap(cause) != nil {
			cause = errors.Unwrap(cause)
		}
		info.ErrorType = fmt.Sprintf("%T", cause)
		if info.Model == "" {
			info.Model = "backend default"
		}
		info.Attempts, info.ElapsedMS, info.TimeoutMS = attempts, time.Since(started).Milliseconds(), timeout.Milliseconds()
		err = sentinel.NewMonitorFailure(info, err)
	}
	return result, err
}

// attempt opens one fresh session and drains it to a terminal result.
// launched reports whether the session was opened at all (mirrors the old
// SDK-backed Connect+QueryStream success signal); retryable reports whether
// a non-nil err is a decode-shaped failure eligible for Dispatch's single
// retry, as opposed to a connection/timeout failure.
func (a *MonitorAdapter) attempt(ctx context.Context, spec sentinel.MonitorSpec, windowText string) (result sentinel.MonitorResult, launched, retryable bool, err error) {
	h, err := a.newHarness()
	if err != nil {
		return sentinel.MonitorResult{}, false, false, &monitorStageError{stage: "adapter_start", err: err}
	}
	sess, err := h.Open(ctx, monitorSessionSpec(a.backend, spec, windowText))
	if err != nil {
		return sentinel.MonitorResult{}, false, false, &monitorStageError{stage: "session_open", err: err}
	}
	defer sess.Close()
	result, retryable, err = drainMonitorEvents(ctx, sess.Messages())
	if err != nil {
		stage := "response"
		if retryable {
			stage = "verdict"
		}
		err = &monitorStageError{stage: stage, err: err}
	}
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

// Provider messages can include arbitrary transcript content. Classify them
// into fixed explanations rather than copying their text into findings.
type monitorStageError struct {
	stage string
	err   error
}

func (e *monitorStageError) Error() string { return e.err.Error() }
func (e *monitorStageError) Unwrap() error { return e.err }

func monitorFailureInfo(err error) sentinel.MonitorFailure {
	info := sentinel.MonitorFailure{Stage: "dispatch", Reason: "backend_error", Summary: "The security classifier backend failed", NextStep: "Check the configured security.monitor_backend and security.monitor_model, and verify that backend works outside jig. Restart jig after fixing it."}
	var stage *monitorStageError
	if errors.As(err, &stage) {
		info.Stage = stage.stage
	}
	text := strings.ToLower(err.Error())
	var executable *exec.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		info.Reason, info.Summary = "timeout", "The security classifier exceeded its time limit"
		info.NextStep = "Check backend availability and response latency. Restart jig to re-enable this check."
	case errors.Is(err, context.Canceled):
		info.Reason, info.Summary = "canceled", "The security classifier was canceled"
	case errors.As(err, &executable):
		info.Reason, info.Summary = "adapter_unavailable", "The classifier executable could not be started"
		info.NextStep = "Install the configured backend and ensure its executable and npx are available on PATH. Restart jig afterward."
	case strings.Contains(text, "authentication") || strings.Contains(text, "not authenticated") || strings.Contains(text, "unauthorized") || strings.Contains(text, "not logged in"):
		info.Reason, info.Summary = "authentication", "The classifier backend requires authentication"
		info.NextStep = "Sign in to the configured monitor backend, then restart jig. The monitor backend can differ from the workflow agent backend."
	case strings.Contains(text, "rate limit") || strings.Contains(text, "rate_limit") || strings.Contains(text, "429"):
		info.Reason, info.Summary = "rate_limited", "The classifier backend rate limit was reached"
		info.NextStep = "Wait for the provider limit to reset or select another monitor backend/model, then restart jig."
	case strings.Contains(text, "model") && (strings.Contains(text, "unavailable") || strings.Contains(text, "not found") || strings.Contains(text, "not supported")):
		info.Reason, info.Summary = "model_unavailable", "The configured classifier model is unavailable"
		info.NextStep = "Choose a model available to your monitor backend in security.monitor_model, then restart jig."
	case strings.Contains(text, "cwd") || strings.Contains(text, "working directory"):
		info.Reason, info.Summary = "working_directory", "The adapter rejected the classifier working directory"
		info.NextStep = "Check the classifier session working-directory configuration and directory access, then restart jig."
	case strings.Contains(text, "did not advertise") || strings.Contains(text, "set model"):
		info.Reason, info.Summary = "session_configuration", "The adapter could not apply the classifier session settings"
		info.NextStep = "Check that the monitor model and backend are compatible with the installed adapter, then restart jig."
	case info.Stage == "verdict":
		info.Reason, info.Summary = "invalid_verdict", "The classifier did not return a valid security verdict"
		if strings.Contains(text, "missing required fields") {
			info.Summary = "The classifier verdict is missing required fields (flagged, severity, detail)"
		} else if strings.Contains(text, "unknown severity") {
			info.Summary = "The classifier returned an unsupported severity; expected low, medium, high, or critical"
		} else if strings.Contains(text, "no structured output") {
			info.Summary = "The classifier returned no structured security verdict"
		} else if strings.Contains(text, "unknown field") {
			info.Summary = "The classifier returned unexpected JSON fields; only flagged, severity, and detail are accepted"
		}
		info.NextStep = "Use a monitor model that follows the required JSON schema. One fresh-session retry was exhausted; restart jig after changing the monitor configuration."
	case strings.Contains(text, "closed without result"):
		info.Reason, info.Summary = "session_closed", "The classifier session ended without a result"
	}
	return info
}
