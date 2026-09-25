package harness_test

import (
	"context"
	"os"
	"path/filepath"
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
			wantSets: []string{"set-config:model=claude-haiku-4-5-20251001", "set-config:effort=high"},
		},
		{
			name: "claude resume reapplies model", backend: "claude",
			model: "haiku", resume: true,
			wantSets: []string{"set-config:model=haiku"},
		},
		{
			name: "claude without model sends no config", backend: "claude",
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
			if err := os.WriteFile(rpcLog, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			stepDir := t.TempDir()
			req := engine.StepRequest{
				Step: &workflow.Step{
					ID: "worker", Type: workflow.StepAgent, Backend: tc.backend,
					Isolation: workflow.IsolationNone, Model: tc.model,
					SnapshotAgent: &workflow.AgentSnapshot{Agent: resolvedFixtureAgent(tc.backend, tc.model, tc.effort)},
				},
				TranscriptPath: filepath.Join(stepDir, "transcript.jsonl"),
				ExecutionDir:   stepDir,
			}
			if tc.resume {
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
			var sets []string
			for _, line := range strings.Split(strings.TrimSpace(string(logData)), "\n") {
				if strings.HasPrefix(line, "set-config:") {
					sets = append(sets, line)
				}
			}

			if tc.wantErr != "" {
				if result.Status != step.StatusFailed || !strings.Contains(result.Err, tc.wantErr) {
					t.Fatalf("result = %s %q, want failure containing %q", result.Status, result.Err, tc.wantErr)
				}
				if strings.Contains(string(logData), "prompt") {
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
