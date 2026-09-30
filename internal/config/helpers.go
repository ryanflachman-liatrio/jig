package config

import (
	"fmt"
	"strings"

	"github.com/ryanflachman-liatrio/jig/internal/agentcfg"
)

// HelpChatConfig selects the backend and model for the in-monitor help chat.
// Both fields are optional: an empty Backend means claude, and an empty Model
// lets the help chat apply its per-backend default.
type HelpChatConfig struct {
	Backend string `toml:"backend"`
	Model   string `toml:"model"`
}

// BackendOrDefault resolves Backend, falling back to claude when unset.
func (c HelpChatConfig) BackendOrDefault() string {
	return backendOrDefault(c.Backend)
}

// SecurityConfig selects the backend and model for the Tier-2 security
// monitors. Both fields are optional: an empty MonitorBackend means claude,
// and an empty MonitorModel lets the monitors apply their per-backend
// default.
type SecurityConfig struct {
	MonitorBackend string `toml:"monitor_backend"`
	MonitorModel   string `toml:"monitor_model"`
}

// MonitorBackendOrDefault resolves MonitorBackend, falling back to claude
// when unset.
func (c SecurityConfig) MonitorBackendOrDefault() string {
	return backendOrDefault(c.MonitorBackend)
}

func backendOrDefault(backend string) string {
	if backend == "" {
		return agentcfg.BackendClaude
	}
	return backend
}

// validateBackend rejects an unknown backend for key. The message names the
// key and the accepted values but never the configured value, keeping file
// content out of errors (see load.go's sanitization rule).
func validateBackend(key, backend string) error {
	if backend == "" || agentcfg.ValidBackend(backend) {
		return nil
	}
	return backendError{key: key}
}

// backendError reports an unknown helper backend without the offending value.
type backendError struct{ key string }

func (e backendError) Error() string {
	return fmt.Sprintf("%s must be one of %s", e.key, strings.Join(agentcfg.Backends, ", "))
}

func mergeHelpChat(base, overlay HelpChatConfig) HelpChatConfig {
	out := base
	if overlay.Backend != "" {
		out.Backend = overlay.Backend
	}
	if overlay.Model != "" {
		out.Model = overlay.Model
	}
	return out
}

func mergeSecurity(base, overlay SecurityConfig) SecurityConfig {
	out := base
	if overlay.MonitorBackend != "" {
		out.MonitorBackend = overlay.MonitorBackend
	}
	if overlay.MonitorModel != "" {
		out.MonitorModel = overlay.MonitorModel
	}
	return out
}
