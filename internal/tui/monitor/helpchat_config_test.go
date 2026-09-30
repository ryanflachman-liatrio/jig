package monitor

import (
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/config"
	"github.com/ryanflachman-liatrio/jig/internal/engine"
)

func TestMonitorHandsHelpChatConfigToHelpModel(t *testing.T) {
	tests := []struct {
		cfg  config.HelpChatConfig
		want string
	}{
		{config.HelpChatConfig{}, "claude"},
		{config.HelpChatConfig{Backend: "codex"}, "codex"},
	}
	for _, tc := range tests {
		m := New("run-help").WithHelpChatConfig(tc.cfg)
		if got := m.newHelpModel(engine.RunSnapshot{}).Backend(); got != tc.want {
			t.Errorf("help backend with %+v = %q, want %q", tc.cfg, got, tc.want)
		}
	}
}
