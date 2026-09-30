package config

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelperBackendsDefaultToClaude(t *testing.T) {
	var cfg Config
	if got := cfg.HelpChat.BackendOrDefault(); got != "claude" {
		t.Fatalf("HelpChat.BackendOrDefault() = %q, want claude", got)
	}
	if got := cfg.Security.MonitorBackendOrDefault(); got != "claude" {
		t.Fatalf("Security.MonitorBackendOrDefault() = %q, want claude", got)
	}
	if cfg.HelpChat.Model != "" || cfg.Security.MonitorModel != "" {
		t.Fatal("unset helper models should stay empty so consumers apply their own defaults")
	}
}

func TestHelperBackendsExplicitValues(t *testing.T) {
	dir := t.TempDir()
	userPath := writeConfigFile(t, dir, "user.toml", `
[helpchat]
backend = "codex"
model = "gpt-5-mini"

[security]
monitor_backend = "cursor"
`)
	cfg, err := Load(userPath, "")
	if err != nil {
		t.Fatalf("Load: unexpected error %v", err)
	}
	if cfg.HelpChat.BackendOrDefault() != "codex" || cfg.HelpChat.Model != "gpt-5-mini" {
		t.Fatalf("HelpChat = %+v, want codex/gpt-5-mini", cfg.HelpChat)
	}
	if cfg.Security.MonitorBackendOrDefault() != "cursor" || cfg.Security.MonitorModel != "" {
		t.Fatalf("Security = %+v, want cursor with empty model", cfg.Security)
	}
}

func TestHelperBackendsProjectOverridesUser(t *testing.T) {
	dir := t.TempDir()
	userPath := writeConfigFile(t, dir, "user.toml", "[helpchat]\nbackend = \"codex\"\nmodel = \"a\"\n[security]\nmonitor_backend = \"codex\"\n")
	projectRoot := filepath.Join(dir, "project", ".jig")
	writeConfigFile(t, dir, filepath.Join("project", ".jig", "config.toml"), "[helpchat]\nbackend = \"cursor\"\n[security]\nmonitor_model = \"m\"\n")

	cfg, err := Load(userPath, projectRoot)
	if err != nil {
		t.Fatalf("Load: unexpected error %v", err)
	}
	if cfg.HelpChat.Backend != "cursor" || cfg.HelpChat.Model != "a" {
		t.Fatalf("HelpChat = %+v, want project backend cursor with user model a", cfg.HelpChat)
	}
	if cfg.Security.MonitorBackend != "codex" || cfg.Security.MonitorModel != "m" {
		t.Fatalf("Security = %+v, want user backend codex with project model m", cfg.Security)
	}
}

func TestHelperBackendsRejectUnknown(t *testing.T) {
	cases := []struct {
		name, body, key string
	}{
		{"helpchat", "[helpchat]\nbackend = \"bogus\"\n", "helpchat.backend"},
		{"security", "[security]\nmonitor_backend = \"bogus\"\n", "security.monitor_backend"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfigFile(t, t.TempDir(), "user.toml", tc.body)
			_, err := Load(path, "")
			if !errors.Is(err, ErrConfigInvalid) {
				t.Fatalf("err = %v, want ErrConfigInvalid", err)
			}
			msg := err.Error()
			for _, want := range []string{tc.key, "claude", "codex", "cursor"} {
				if !strings.Contains(msg, want) {
					t.Fatalf("error %q does not mention %q", msg, want)
				}
			}
			if strings.Contains(msg, "bogus") {
				t.Fatalf("error %q echoes the configured value; keep file content out of errors", msg)
			}
		})
	}
}
