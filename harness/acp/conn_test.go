package acp

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

func TestSelectOptions(t *testing.T) {
	options := acpsdk.SessionConfigSelectOptionsUngrouped{
		{Value: "low"},
		{Value: "high"},
	}
	conn := &Conn{}
	conn.setSessionConfig("session", []acpsdk.SessionConfigOption{{
		Select: &acpsdk.SessionConfigOptionSelect{
			Id:       "adapter-effort",
			Category: categoryPtr(acpsdk.SessionConfigOptionCategoryThoughtLevel),
			Options:  acpsdk.SessionConfigSelectOptions{Ungrouped: &options},
		},
	}})

	got, ok := conn.selectOptions("session", "adapter-effort")
	if !ok || !containsConfigValue(got, "high") {
		t.Fatalf("selectOptions() = %v, %t; want advertised high", got, ok)
	}
	if got, ok := conn.selectOptionIDByCategory("session", acpsdk.SessionConfigOptionCategoryThoughtLevel); !ok || got != "adapter-effort" {
		t.Fatalf("selectOptionIDByCategory() = %q, %t; want adapter-effort", got, ok)
	}
	if _, ok := conn.selectOptions("session", "model"); ok {
		t.Fatal("selectOptions(model) reported an option that was not advertised")
	}
}

func TestSetSelectConfigRejectsUnavailableValue(t *testing.T) {
	options := acpsdk.SessionConfigSelectOptionsUngrouped{{Value: "medium"}}
	conn := &Conn{}
	conn.setSessionConfig("session", []acpsdk.SessionConfigOption{{
		Select: &acpsdk.SessionConfigOptionSelect{
			Id:       "adapter-effort",
			Category: categoryPtr(acpsdk.SessionConfigOptionCategoryThoughtLevel),
			Options:  acpsdk.SessionConfigSelectOptions{Ungrouped: &options},
		},
	}})

	err := conn.SetSelectConfig(t.Context(), "session", "adapter-effort", "high")
	if err == nil || !strings.Contains(err.Error(), "adapter advertises medium") {
		t.Fatalf("SetEffort() error = %v, want advertised values", err)
	}
}

func categoryPtr(category acpsdk.SessionConfigOptionCategory) *acpsdk.SessionConfigOptionCategory {
	return &category
}

// configRecordingAgent is an in-process ACP agent that records every
// session/set_config_option request it receives.
type configRecordingAgent struct {
	acpsdk.Agent
	mu   sync.Mutex
	sets []acpsdk.SetSessionConfigOptionValueId
}

func (a *configRecordingAgent) SetSessionConfigOption(_ context.Context, req acpsdk.SetSessionConfigOptionRequest) (acpsdk.SetSessionConfigOptionResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if req.ValueId != nil {
		a.sets = append(a.sets, *req.ValueId)
	}
	return acpsdk.SetSessionConfigOptionResponse{}, nil
}

func (a *configRecordingAgent) recorded() []acpsdk.SetSessionConfigOptionValueId {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]acpsdk.SetSessionConfigOptionValueId(nil), a.sets...)
}

// newPipedConn wires a Conn to agent over in-memory pipes and advertises a
// model selector whose options are aliases, mirroring Claude's adapter.
func newPipedConn(t *testing.T, agent *configRecordingAgent) *Conn {
	t.Helper()
	clientToAgentR, clientToAgentW := io.Pipe()
	agentToClientR, agentToClientW := io.Pipe()
	t.Cleanup(func() {
		_ = clientToAgentW.Close()
		_ = agentToClientW.Close()
	})
	acpsdk.NewAgentSideConnection(agent, agentToClientW, clientToAgentR)
	client := &Client{}
	conn := &Conn{rpc: acpsdk.NewClientSideConnection(client, clientToAgentW, agentToClientR), client: client}
	options := acpsdk.SessionConfigSelectOptionsUngrouped{{Value: "default"}, {Value: "haiku"}}
	conn.setSessionConfig("session", []acpsdk.SessionConfigOption{{
		Select: &acpsdk.SessionConfigOptionSelect{
			Id:       "model",
			Category: categoryPtr(acpsdk.SessionConfigOptionCategoryModel),
			Options:  acpsdk.SessionConfigSelectOptions{Ungrouped: &options},
		},
	}})
	return conn
}

func TestSetSelectConfigByCategoryAdapterValidatedDefersValueToAdapter(t *testing.T) {
	agent := &configRecordingAgent{}
	conn := newPipedConn(t, agent)
	const fullID = "claude-haiku-4-5-20251001"

	if err := conn.SetSelectConfigByCategory(t.Context(), "session", acpsdk.SessionConfigOptionCategoryModel, fullID); err == nil {
		t.Fatal("strict setter accepted a value the adapter did not advertise verbatim")
	}
	if got := agent.recorded(); len(got) != 0 {
		t.Fatalf("strict setter reached the adapter: %+v", got)
	}

	if err := conn.SetSelectConfigByCategoryAdapterValidated(t.Context(), "session", acpsdk.SessionConfigOptionCategoryModel, fullID); err != nil {
		t.Fatalf("SetSelectConfigByCategoryAdapterValidated: %v", err)
	}
	got := agent.recorded()
	if len(got) != 1 || got[0].ConfigId != "model" || got[0].Value != fullID {
		t.Fatalf("adapter received %+v, want model=%s", got, fullID)
	}
}

func TestSetSelectConfigByCategoryAdapterValidatedRequiresAdvertisedCategory(t *testing.T) {
	agent := &configRecordingAgent{}
	conn := newPipedConn(t, agent)

	err := conn.SetSelectConfigByCategoryAdapterValidated(t.Context(), "session", acpsdk.SessionConfigOptionCategoryThoughtLevel, "high")
	if err == nil || !strings.Contains(err.Error(), "thought_level") {
		t.Fatalf("error = %v, want missing thought_level selector", err)
	}
	if err := conn.SetSelectConfigByCategoryAdapterValidated(t.Context(), "session", acpsdk.SessionConfigOptionCategoryThoughtLevel, ""); err != nil {
		t.Fatalf("empty value must be a no-op, got %v", err)
	}
	if got := agent.recorded(); len(got) != 0 {
		t.Fatalf("adapter received %+v, want no requests", got)
	}
}

// sessionRecordingAgent is an in-process ACP agent that records the _meta and
// MCP server list of every session/new and session/load request.
type sessionRecordingAgent struct {
	acpsdk.Agent
	mu      sync.Mutex
	newReqs []acpsdk.NewSessionRequest
	loads   []acpsdk.LoadSessionRequest
}

func (a *sessionRecordingAgent) NewSession(_ context.Context, req acpsdk.NewSessionRequest) (acpsdk.NewSessionResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.newReqs = append(a.newReqs, req)
	return acpsdk.NewSessionResponse{SessionId: "session"}, nil
}

func (a *sessionRecordingAgent) LoadSession(_ context.Context, req acpsdk.LoadSessionRequest) (acpsdk.LoadSessionResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.loads = append(a.loads, req)
	return acpsdk.LoadSessionResponse{}, nil
}

func TestSessionRequestsCarryMeta(t *testing.T) {
	clientToAgentR, clientToAgentW := io.Pipe()
	agentToClientR, agentToClientW := io.Pipe()
	t.Cleanup(func() {
		_ = clientToAgentW.Close()
		_ = agentToClientW.Close()
	})
	agent := &sessionRecordingAgent{}
	acpsdk.NewAgentSideConnection(agent, agentToClientW, clientToAgentR)
	client := &Client{}
	conn := &Conn{rpc: acpsdk.NewClientSideConnection(client, clientToAgentW, agentToClientR), client: client, SupportsLoadSession: true}

	meta := map[string]any{"claudeCode": map[string]any{"options": map[string]any{"tools": []any{}}}}
	server := acpsdk.McpServer{Stdio: &acpsdk.McpServerStdio{Name: "fixture", Command: "fixture-mcp", Args: []string{}, Env: []acpsdk.EnvVariable{}}}

	if _, err := conn.NewSession(t.Context(), "/tmp/fixture", WithMeta(meta), WithMcpServers(server)); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := conn.LoadSession(t.Context(), "/tmp/fixture", "session", WithMeta(meta)); err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if _, err := conn.NewSession(t.Context(), "/tmp/fixture"); err != nil {
		t.Fatalf("NewSession without options: %v", err)
	}

	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.newReqs) != 2 || len(agent.loads) != 1 {
		t.Fatalf("recorded %d new and %d load requests, want 2 and 1", len(agent.newReqs), len(agent.loads))
	}
	for name, got := range map[string]map[string]any{"new": agent.newReqs[0].Meta, "load": agent.loads[0].Meta} {
		options, _ := got["claudeCode"].(map[string]any)["options"].(map[string]any)
		if tools, ok := options["tools"].([]any); !ok || len(tools) != 0 {
			t.Fatalf("%s _meta = %#v, want claudeCode.options.tools = []", name, got)
		}
	}
	if len(agent.newReqs[0].McpServers) != 1 || agent.newReqs[0].McpServers[0].Stdio.Name != "fixture" {
		t.Fatalf("new session MCP servers = %+v, want the fixture server", agent.newReqs[0].McpServers)
	}
	if agent.newReqs[1].Meta != nil || agent.newReqs[1].McpServers == nil {
		t.Fatalf("option-free request = meta %#v servers %#v, want no _meta and an empty server list", agent.newReqs[1].Meta, agent.newReqs[1].McpServers)
	}
}
