package runner

import (
	_ "embed"
	"fmt"
	"strings"

	"jig/internal/sentinel"
	"jig/internal/workflow"
)

const monitorModel = "claude-haiku-4-5-20251001"

//go:embed monitors/prompt-injection.md
var promptInjectionMonitor []byte

//go:embed monitors/stuck-loop.md
var stuckLoopMonitor []byte

//go:embed monitors/exfil-pattern.md
var exfilPatternMonitor []byte

func BuiltinMonitors() ([]sentinel.MonitorDef, error) {
	adapter := NewMonitorAdapter()
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
		def, err := parseBuiltinMonitor(asset.id, asset.data, adapter)
		if err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	return defs, nil
}

func parseBuiltinMonitor(id string, data []byte, dispatcher sentinel.MonitorDispatcher) (sentinel.MonitorDef, error) {
	model, prompt, err := workflow.ParseAgentFileContent(data)
	if err != nil {
		return sentinel.MonitorDef{}, fmt.Errorf("parse built-in monitor %q: %w", id, err)
	}
	if model == "haiku" {
		model = monitorModel
	}
	if model != monitorModel {
		return sentinel.MonitorDef{}, fmt.Errorf("built-in monitor %q must use %s", id, monitorModel)
	}
	if strings.TrimSpace(prompt) == "" {
		return sentinel.MonitorDef{}, fmt.Errorf("built-in monitor %q has an empty prompt", id)
	}
	return sentinel.MonitorDef{Monitor: id, Spec: sentinel.MonitorSpec{Model: model, Prompt: prompt}, Dispatcher: dispatcher, Circuit: &sentinel.MonitorCircuit{}}, nil
}
