package acp

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
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

func TestMissingEffortLogsAdvertisedConfiguration(t *testing.T) {
	dir := t.TempDir()
	log, err := newDiagnosticLog(dir, fixedSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	conn := newPipedConn(t, &configRecordingAgent{})
	conn.diagnostics = log
	if err := conn.SetSelectConfig(t.Context(), "session", "model", "haiku"); err != nil {
		t.Fatal(err)
	}
	err = conn.SetSelectConfigByCategory(t.Context(), "session", acpsdk.SessionConfigOptionCategoryThoughtLevel, "high")
	if err == nil {
		t.Fatal("missing effort must fail")
	}
	body, err := os.ReadFile(filepath.Join(dir, "acp-diagnostics.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var events []lifecycleEvent
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		var event lifecycleEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want request, response, rejection: %s", len(events), body)
	}
	if events[0].Event != "set_config_started" || events[0].Fields["config_id"] != "model" || events[0].Fields["value"] != "haiku" {
		t.Fatalf("request: %+v", events[0])
	}
	options, ok := events[0].Fields["advertised_config"].([]any)
	if !ok || len(options) != 1 {
		t.Fatalf("initial configuration: %+v", events[0])
	}
	model := options[0].(map[string]any)
	if model["id"] != "model" || model["category"] != "model" {
		t.Fatalf("model configuration: %+v", model)
	}
	values := model["values"].([]any)
	if len(values) != 2 || values[0] != "default" || values[1] != "haiku" {
		t.Fatalf("model choices: %+v", values)
	}
	if events[1].Event != "set_config_finished" || len(events[1].Fields["advertised_config"].([]any)) != 0 {
		t.Fatalf("response configuration: %+v", events[1])
	}
	rejected := events[2]
	if rejected.Event != "set_config_rejected" || rejected.Fields["category"] != "thought_level" || rejected.Fields["value"] != "high" || rejected.Fields["reason"] != "missing_category" {
		t.Fatalf("rejection: %+v", rejected)
	}
}
