package helpchat

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ryanflachman-liatrio/jig/internal/agentcfg"
	"github.com/ryanflachman-liatrio/jig/internal/config"
	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/harness"
)

var allHelpCaps = harness.NewCapabilitySet(harness.CapPartialStreaming, harness.CapPermissionCallback, harness.CapSessionResume)

// runTwoTurns drives two user turns through m against h and returns the
// SessionSpec each turn opened.
func runTwoTurns(t *testing.T, m Model, h *fakeHelpchatHarness) []harness.SessionSpec {
	t.Helper()
	m, _ = m.Update(m.Init()().(ServerReadyMsg))
	defer m.toolSrv.Close()
	for _, text := range []string{"what failed?", "and why did that happen?"} {
		m.ta.SetValue(text)
		var cmd tea.Cmd
		m, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd == nil {
			t.Fatalf("turn %q fired no command", text)
		}
		connected, ok := cmd().(ConnectedMsg)
		if !ok {
			t.Fatalf("turn %q did not connect", text)
		}
		m, _ = m.Update(connected)
		for {
			next := waitForMessageCmd(m.msgChan)()
			m, _ = m.Update(next)
			if _, done := next.(TurnCompleteMsg); done {
				break
			}
		}
	}
	return h.specs
}

func TestHelpChatBackendSelection(t *testing.T) {
	tests := []struct {
		name    string
		backend string
		model   string
		want    agentcfg.Agent
	}{
		{"claude default", "claude", helpModelID, agentcfg.ClaudeAgent{Common: agentcfg.Common{Model: helpModelID}, MaxTurns: 200}},
		{"codex model", "codex", "gpt-5-codex", agentcfg.CodexAgent{Common: agentcfg.Common{Model: "gpt-5-codex"}}},
		{"codex default", "codex", "", agentcfg.CodexAgent{}},
		{"cursor", "cursor", "sonnet-4", agentcfg.CursorAgent{Common: agentcfg.Common{Model: "sonnet-4"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeHelpchatHarness{events: []harness.Event{
				{Type: harness.EventTextDelta, Text: "ok"},
				{Type: harness.EventResult, SessionID: "sess-1"},
			}}
			probe := &harness.FakeHarness{NameVal: tc.backend, Caps: allHelpCaps}
			m := newModel(fakeRun("run-help"), t.TempDir(), engine.RunSnapshot{ID: "run-help"}, tc.backend, tc.model, probe,
				func() (helpchatHarness, error) { return h, nil })
			specs := runTwoTurns(t, m, h)
			if len(specs) != 2 {
				t.Fatalf("opened %d sessions, want 2", len(specs))
			}
			for i, spec := range specs {
				if !reflect.DeepEqual(spec.Agent, tc.want) {
					t.Errorf("turn %d agent = %#v, want %#v", i+1, spec.Agent, tc.want)
				}
				if spec.Model != tc.model || !spec.Partial || spec.Permission == nil {
					t.Errorf("turn %d spec = model %q partial %v permission %v", i+1, spec.Model, spec.Partial, spec.Permission != nil)
				}
				if len(spec.MCPServers) != 1 || spec.MCPServers[0].Name != "jig-help" {
					t.Errorf("turn %d MCP servers = %+v, want jig-help", i+1, spec.MCPServers)
				}
			}
			if specs[0].Resume != "" || specs[1].Resume != "sess-1" {
				t.Fatalf("resume = %q then %q, want \"\" then sess-1", specs[0].Resume, specs[1].Resume)
			}
		})
	}
}

func TestNewResolvesConfiguredBackend(t *testing.T) {
	tests := []struct {
		cfg                config.HelpChatConfig
		backend, wantModel string
	}{
		{config.HelpChatConfig{}, "claude", helpModelID},
		{config.HelpChatConfig{Model: "claude-sonnet-5-5"}, "claude", "claude-sonnet-5-5"},
		{config.HelpChatConfig{Backend: "codex"}, "codex", ""},
		{config.HelpChatConfig{Backend: "cursor", Model: "sonnet-4"}, "cursor", "sonnet-4"},
	}
	for _, tc := range tests {
		m := New(nil, "", engine.RunSnapshot{}, tc.cfg)
		if m.backend != tc.backend || m.model != tc.wantModel || m.newHarness == nil {
			t.Errorf("New(%+v) = backend %q model %q, want %q %q", tc.cfg, m.backend, m.model, tc.backend, tc.wantModel)
		}
	}
}

func TestHelpChatUnavailableWithoutRequiredCapability(t *testing.T) {
	tests := []struct {
		name    string
		caps    harness.CapabilitySet
		missing string
	}{
		{"no resume", harness.NewCapabilitySet(harness.CapPartialStreaming, harness.CapPermissionCallback), "CapSessionResume"},
		{"no streaming", harness.NewCapabilitySet(harness.CapSessionResume, harness.CapPermissionCallback), "CapPartialStreaming"},
		{"no permission", harness.NewCapabilitySet(harness.CapSessionResume, harness.CapPartialStreaming), "CapPermissionCallback"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			probe := &harness.FakeHarness{NameVal: "codex", Caps: tc.caps}
			m := newModel(fakeRun("run-help"), t.TempDir(), engine.RunSnapshot{}, "codex", "", probe,
				func() (helpchatHarness, error) { t.Fatal("unavailable model opened a harness"); return nil, nil })
			if m.Init() != nil {
				t.Fatal("unavailable model started the tool server")
			}
			if len(m.turns) != 1 {
				t.Fatalf("turns = %+v, want one unavailable turn", m.turns)
			}
			text := m.turns[0].assistant
			for _, want := range []string{"unavailable", `"codex"`, tc.missing} {
				if !strings.Contains(text, want) {
					t.Errorf("unavailable text %q does not contain %q", text, want)
				}
			}
		})
	}
}
