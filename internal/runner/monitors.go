package runner

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/ryanflachman-liatrio/jig/internal/agentcfg"
	"github.com/ryanflachman-liatrio/jig/internal/config"
	"github.com/ryanflachman-liatrio/jig/internal/harness"
	"github.com/ryanflachman-liatrio/jig/internal/sentinel"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// monitorModel is the Claude default for Tier-2 monitors. Codex and Cursor
// default to an empty model, which lets the backend pick its own.
const monitorModel = "claude-haiku-4-5-20251001"

//go:embed monitors/prompt-injection.md
var promptInjectionMonitor []byte

//go:embed monitors/stuck-loop.md
var stuckLoopMonitor []byte

//go:embed monitors/exfil-pattern.md
var exfilPatternMonitor []byte

// BuiltinMonitors builds the embedded Tier-2 monitors on the backend and model
// cfg selects. It fails closed, before any run starts, when that backend
// cannot enforce the monitor schema or the deny-all permission callback.
func BuiltinMonitors(cfg config.SecurityConfig) ([]sentinel.MonitorDef, error) {
	backend := cfg.MonitorBackendOrDefault()
	h, err := harness.For(backend)
	if err != nil {
		return nil, fmt.Errorf("security.monitor_backend: %w", err)
	}
	return builtinMonitors(backend, resolveMonitorModel(backend, cfg.MonitorModel), h, NewMonitorAdapter(backend))
}

// resolveMonitorModel applies the per-backend default when model is unset.
func resolveMonitorModel(backend, model string) string {
	if model == "" && backend == agentcfg.BackendClaude {
		return monitorModel
	}
	return model
}

func builtinMonitors(backend, model string, h harness.Harness, adapter sentinel.MonitorDispatcher) ([]sentinel.MonitorDef, error) {
	if err := harness.Require(h, harness.CapStructuredOutput, harness.CapPermissionCallback); err != nil {
		return nil, fmt.Errorf("security monitor backend %q: %w", backend, err)
	}
	assets := []struct {
		id   string
		data []byte
	}{
		{"prompt-injection", promptInjectionMonitor},
		{"stuck-loop", stuckLoopMonitor},
		{"exfil-pattern", exfilPatternMonitor},
	}
	defs := make([]sentinel.MonitorDef, 0, len(assets))
	seen := make(map[string]bool, len(assets))
	for _, asset := range assets {
		if seen[asset.id] {
			return nil, fmt.Errorf("duplicate built-in monitor %q", asset.id)
		}
		seen[asset.id] = true
		def, err := parseBuiltinMonitor(asset.id, asset.data, model, adapter)
		if err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	return defs, nil
}

// parseBuiltinMonitor parses one embedded monitor file. Its `haiku` alias
// stands for the resolved monitor model; any other model is a packaging error.
func parseBuiltinMonitor(id string, data []byte, resolvedModel string, dispatcher sentinel.MonitorDispatcher) (sentinel.MonitorDef, error) {
	model, prompt, err := workflow.ParseAgentFileContent(data)
	if err != nil {
		return sentinel.MonitorDef{}, fmt.Errorf("parse built-in monitor %q: %w", id, err)
	}
	if model == "haiku" {
		model = resolvedModel
	}
	if model != resolvedModel {
		return sentinel.MonitorDef{}, fmt.Errorf("built-in monitor %q must use the configured monitor model", id)
	}
	if strings.TrimSpace(prompt) == "" {
		return sentinel.MonitorDef{}, fmt.Errorf("built-in monitor %q has an empty prompt", id)
	}
	return sentinel.MonitorDef{Monitor: id, Spec: sentinel.MonitorSpec{Model: model, Prompt: prompt}, Dispatcher: dispatcher, Circuit: &sentinel.MonitorCircuit{}}, nil
}
