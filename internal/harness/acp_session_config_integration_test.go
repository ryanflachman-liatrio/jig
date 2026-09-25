package harness_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"jig/internal/agentcfg"
	"jig/internal/engine"
	"jig/internal/harness"
	"jig/internal/runner"
	"jig/internal/step"
	"jig/internal/workflow"
)

// TestACPHarnessesApplyStepModelAndEffort drives each backend's real Open path
// against the fixture adapter and asserts a step's model/effort reach the
// adapter as session config — or fail closed when the adapter cannot honor
// them — rather than being silently dropped.
func TestACPHarnessesApplyStepModelAndEffort(t *testing.T) {
	rpcLog := setupConfigFixture(t)

	for _, tc := range []struct {
		name, backend, model string
		effort               string
		resume               bool
		wantSets             []string
		wantErr              string
	}{
		{
			// Claude's adapter resolves full model IDs onto its alias options,
			// so the ID is forwarded verbatim rather than rejected client-side.
			name: "claude full model id and effort", backend: "claude",
			model: "claude-haiku-4-5-20251001", effort: "high",
			wantSets: []string{"set-config:model=claude-haiku-4-5-20251001", "set-config:effort=high", "set-config:mode=default"},
		},
		{
			name: "claude resume reapplies model", backend: "claude",
			model: "haiku", resume: true,
			wantSets: []string{"set-config:model=haiku", "set-config:mode=default"},
		},
		{
			// The permission mode is always applied, so an unset one still
			// reaches the adapter as an explicit default.
			name: "claude without model sets only the default mode", backend: "claude",
			wantSets: []string{"set-config:mode=default"},
		},
		{
			name: "cursor exact model", backend: "cursor",
			model:    "fixture-model",
			wantSets: []string{"set-config:model=fixture-model"},
		},
		{
			name: "cursor rejects unadvertised model", backend: "cursor",
			model:   "claude-haiku-4-5-20251001",
			wantErr: "is unavailable; adapter advertises",
		},
		{
			name: "codex model and effort", backend: "codex",
			model: "fixture-model", effort: "low",
			wantSets: []string{"set-config:model=fixture-model", "set-config:effort=low"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, logData := runFixtureStep(t, rpcLog, fixtureStep{
				backend: tc.backend, agent: resolvedFixtureAgent(tc.backend, tc.model, tc.effort), resume: tc.resume,
			})
			sets := fixtureLines(logData, "set-config:")

			if tc.wantErr != "" {
				if result.Status != step.StatusFailed || !strings.Contains(result.Err, tc.wantErr) {
					t.Fatalf("result = %s %q, want failure containing %q", result.Status, result.Err, tc.wantErr)
				}
				if len(fixtureLines(logData, "prompt")) != 0 {
					t.Fatalf("step prompted despite unappliable config; rpc log:\n%s", logData)
				}
				return
			}
			if result.Status != step.StatusSucceeded {
				t.Fatalf("result = %s %q; rpc log:\n%s", result.Status, result.Err, logData)
			}
			if strings.Join(sets, "\n") != strings.Join(tc.wantSets, "\n") {
				t.Fatalf("config sets = %q, want %q", sets, tc.wantSets)
			}
		})
	}
}

// resolvedFixtureAgent builds the resolved agent a loaded step would carry.
func resolvedFixtureAgent(backend, model, effort string) agentcfg.Agent {
	common := agentcfg.Common{Model: model}
	switch backend {
	case agentcfg.BackendCodex:
		return agentcfg.CodexAgent{Common: common, Effort: effort}
	case agentcfg.BackendCursor:
		return agentcfg.CursorAgent{Common: common}
	}
	return agentcfg.ClaudeAgent{Common: common, Effort: effort}
}

// setupConfigFixture points npx and cursor-agent at the in-test ACP fixture
// and returns the fixture's RPC log path.
func setupConfigFixture(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	rpcLog := filepath.Join(binDir, "rpc.log")
	writeACPWrapper(t, filepath.Join(binDir, "npx"))
	writeACPWrapper(t, filepath.Join(binDir, "cursor-agent"))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("JIG_ACP_FIXTURE", "1")
	t.Setenv("JIG_ACP_FIXTURE_BIN", os.Args[0])
	t.Setenv("JIG_ACP_ARGS_LOG", filepath.Join(binDir, "args.log"))
	t.Setenv("JIG_ACP_RPC_LOG", rpcLog)
	t.Setenv("JIG_ACP_FIXTURE_DELAY_MS", "10")
	return rpcLog
}

type fixtureStep struct {
	backend string
	agent   agentcfg.Agent
	askUser bool
	resume  bool
}

// runFixtureStep executes one agent step through the real runner and harness
// against the fixture adapter, returning the result and the RPC log.
func runFixtureStep(t *testing.T, rpcLog string, fs fixtureStep) (*step.Result, string) {
	t.Helper()
	if err := os.WriteFile(rpcLog, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	stepDir := t.TempDir()
	askUser := fs.askUser
	req := engine.StepRequest{
		Step: &workflow.Step{
			ID: "worker", Type: workflow.StepAgent, Backend: fs.backend,
			Isolation: workflow.IsolationNone, Model: fs.agent.Base().Model, AskUser: &askUser,
			SnapshotAgent: &workflow.AgentSnapshot{Agent: fs.agent},
		},
		TranscriptPath: filepath.Join(stepDir, "transcript.jsonl"),
		ExecutionDir:   stepDir,
	}
	if fs.resume {
		req.ResumeSessionID = "fixture-session"
		req.Message = "continue"
	}
	result, err := runner.NewAgentExecutor(harness.For).Execute(context.Background(), req, noopReporter{})
	if err != nil {
		t.Fatal(err)
	}
	logData, err := os.ReadFile(rpcLog)
	if err != nil {
		t.Fatal(err)
	}
	return result, string(logData)
}

func fixtureLines(log, prefix string) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		if strings.HasPrefix(line, prefix) {
			lines = append(lines, line)
		}
	}
	return lines
}

// fixtureClaudeOptions decodes the _meta.claudeCode.options the fixture
// recorded for a session/new ("new") or session/load ("load") request.
func fixtureClaudeOptions(t *testing.T, log, kind string) map[string]any {
	t.Helper()
	lines := fixtureLines(log, "meta:"+kind+":")
	if len(lines) != 1 {
		t.Fatalf("recorded %d %s _meta lines, want 1; rpc log:\n%s", len(lines), kind, log)
	}
	var meta struct {
		ClaudeCode struct {
			Options map[string]any `json:"options"`
		} `json:"claudeCode"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[0], "meta:"+kind+":")), &meta); err != nil {
		t.Fatal(err)
	}
	return meta.ClaudeCode.Options
}

// TestClaudeAgentSessionMeta proves every Claude tool-list setting reaches the
// adapter under its SDK name in _meta.claudeCode.options, on session/new and
// on session/load.
func TestClaudeAgentSessionMeta(t *testing.T) {
	rpcLog := setupConfigFixture(t)
	for _, tc := range []struct {
		name    string
		agent   agentcfg.ClaudeAgent
		askUser bool
		want    map[string]any
	}{
		{
			name:  "tools restrict the built-in set and ask_user off disallows AskUserQuestion",
			agent: agentcfg.ClaudeAgent{Tools: []string{"Read", "Grep"}, DisallowedTools: []string{"Bash"}},
			want: map[string]any{
				"tools":           []any{"Read", "Grep"},
				"disallowedTools": []any{"Bash", "AskUserQuestion"},
				"settingSources":  []any{"project"},
			},
		},
		{
			name:    "ask_user on adds AskUserQuestion to a present tools list",
			agent:   agentcfg.ClaudeAgent{Tools: []string{"Read"}},
			askUser: true,
			want: map[string]any{
				"tools":          []any{"Read", "AskUserQuestion"},
				"settingSources": []any{"project"},
			},
		},
		{
			name:  "explicit empty tools is sent as an empty list",
			agent: agentcfg.ClaudeAgent{Tools: []string{}},
			want: map[string]any{
				"tools":           []any{},
				"disallowedTools": []any{"AskUserQuestion"},
				"settingSources":  []any{"project"},
			},
		},
		{
			name:    "omitted tools keeps the full built-in set",
			agent:   agentcfg.ClaudeAgent{},
			askUser: true,
			want:    map[string]any{"settingSources": []any{"project"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, resume := range []bool{false, true} {
				result, log := runFixtureStep(t, rpcLog, fixtureStep{backend: "claude", agent: tc.agent, askUser: tc.askUser, resume: resume})
				if result.Status != step.StatusSucceeded {
					t.Fatalf("resume=%v result = %s %q; rpc log:\n%s", resume, result.Status, result.Err, log)
				}
				kind := "new"
				if resume {
					kind = "load"
				}
				got := fixtureClaudeOptions(t, log, kind)
				if _, ok := got["allowedTools"]; ok {
					t.Fatalf("%s _meta sent allowedTools: %#v", kind, got)
				}
				if _, ok := got["permissionMode"]; ok {
					t.Fatalf("%s _meta sent permissionMode, which the adapter overwrites: %#v", kind, got)
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("%s _meta.claudeCode.options = %#v, want %#v", kind, got, tc.want)
				}
			}
		})
	}
}

// TestClaudeAgentMode proves the permission mode is applied through the mode
// config option after the model, with an explicit default, and that an
// unadvertised mode fails before the prompt.
func TestClaudeAgentMode(t *testing.T) {
	rpcLog := setupConfigFixture(t)
	for _, tc := range []struct {
		name     string
		agent    agentcfg.ClaudeAgent
		resume   bool
		omit     string
		wantSets []string
		wantErr  string
	}{
		{
			name:     "explicit mode",
			agent:    agentcfg.ClaudeAgent{PermissionMode: "plan"},
			wantSets: []string{"set-config:mode=plan"},
		},
		{
			name:     "explicit mode on a resumed session",
			agent:    agentcfg.ClaudeAgent{PermissionMode: "plan"},
			resume:   true,
			wantSets: []string{"set-config:mode=plan"},
		},
		{
			name:     "unset mode applies default",
			wantSets: []string{"set-config:mode=default"},
		},
		{
			name:     "unset mode applies default on a resumed session",
			resume:   true,
			wantSets: []string{"set-config:mode=default"},
		},
		{
			name:     "model is applied before mode",
			agent:    agentcfg.ClaudeAgent{Common: agentcfg.Common{Model: "haiku"}, PermissionMode: "acceptEdits"},
			wantSets: []string{"set-config:model=haiku", "set-config:mode=acceptEdits"},
		},
		{
			name:    "unadvertised mode fails before the prompt",
			agent:   agentcfg.ClaudeAgent{PermissionMode: "bypassPermissions"},
			omit:    "bypassPermissions",
			wantErr: `mode "bypassPermissions" is unavailable`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JIG_ACP_FIXTURE_OMIT_MODE", tc.omit)
			result, log := runFixtureStep(t, rpcLog, fixtureStep{backend: "claude", agent: tc.agent, resume: tc.resume})
			if tc.wantErr != "" {
				if result.Status != step.StatusFailed || !strings.Contains(result.Err, tc.wantErr) {
					t.Fatalf("result = %s %q, want failure containing %q", result.Status, result.Err, tc.wantErr)
				}
				if len(fixtureLines(log, "prompt")) != 0 {
					t.Fatalf("step prompted despite an unadvertised mode; rpc log:\n%s", log)
				}
				return
			}
			if result.Status != step.StatusSucceeded {
				t.Fatalf("result = %s %q; rpc log:\n%s", result.Status, result.Err, log)
			}
			if got := fixtureLines(log, "set-config:"); !reflect.DeepEqual(got, tc.wantSets) {
				t.Fatalf("config sets = %q, want %q", got, tc.wantSets)
			}
		})
	}
}

// TestClaudePromptLimitOnWire proves the adapter's limit rejection of
// session/prompt reaches the step result with its message intact.
func TestClaudePromptLimitOnWire(t *testing.T) {
	rpcLog := setupConfigFixture(t)
	t.Setenv("JIG_ACP_FIXTURE_PROMPT_ERROR", "Reached maximum number of turns (3)")
	result, log := runFixtureStep(t, rpcLog, fixtureStep{backend: "claude", agent: agentcfg.ClaudeAgent{}})
	if result.Status != step.StatusFailed || !strings.Contains(result.Err, "Reached maximum number of turns (3)") {
		t.Fatalf("result = %s %q, want a failure carrying the adapter's limit message; rpc log:\n%s", result.Status, result.Err, log)
	}
}
