package harness_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"jig/internal/agentcfg"
	"jig/internal/sentinel"
	"jig/internal/step"
)

// wirePermission is one scripted permission round-trip in each adapter's real
// wire shape (see fixturePermission).
type wirePermission struct {
	Notify  map[string]any `json:"notify,omitempty"`
	Request map[string]any `json:"request"`
	Options string         `json:"options,omitempty"`
}

func claudeToolMeta(name string) map[string]any {
	return map[string]any{"claudeCode": map[string]any{"toolName": name}}
}

type wireOutcome struct {
	permissions []string
	findings    []sentinel.Finding
	result      *step.Result
	log         string
}

// runWirePermissions runs one fixture step that replays script and returns
// the option jig selected for each request, plus any persisted findings.
func runWirePermissions(t *testing.T, fs fixtureStep, script []wirePermission) wireOutcome {
	t.Helper()
	rpcLog := setupConfigFixture(t)
	raw, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("JIG_ACP_FIXTURE_PERMISSIONS", string(raw))
	if fs.guard != nil {
		fs.findingsPath = filepath.Join(t.TempDir(), "findings.jsonl")
	}
	result, log := runFixtureStep(t, rpcLog, fs)
	if result.Status != step.StatusSucceeded {
		t.Fatalf("result = %s %q; rpc log:\n%s", result.Status, result.Err, log)
	}
	out := wireOutcome{permissions: fixtureLines(log, "permission:"), result: result, log: log}
	if fs.guard != nil {
		findings, err := sentinel.ReadAll(fs.findingsPath)
		if err != nil {
			t.Fatal(err)
		}
		out.findings = findings
	}
	return out
}

func outboundGuard() *sentinel.Guard { return sentinel.NewGuard([]string{"allowed.example.invalid"}) }

// TestGuardBlocksOutboundOnWire proves the Tier-1 outbound-host rule fires on
// every backend's real permission wire shape. Before canonical tool names,
// the guard keyed on the request title, so none of these shapes reached the
// Bash or WebFetch rule.
func TestGuardBlocksOutboundOnWire(t *testing.T) {
	const blocked = "curl https://blocked.example.invalid"
	for _, tc := range []struct {
		name    string
		backend string
		script  []wirePermission
		want    []string
	}{
		{
			// The request carries _meta only for subagent calls, so the name
			// comes from the tool_call notification the adapter sent first.
			name:    "claude _meta on the tool_call only",
			backend: "claude",
			script: []wirePermission{{
				Notify:  map[string]any{"sessionUpdate": "tool_call", "toolCallId": "c1", "title": "`" + blocked + "`", "status": "pending", "rawInput": map[string]any{"command": blocked}, "_meta": claudeToolMeta("Bash")},
				Request: map[string]any{"toolCallId": "c1", "title": "`" + blocked + "`", "rawInput": map[string]any{"command": blocked}},
			}},
			want: []string{"permission:c1:reject"},
		},
		{
			name:    "claude webfetch url",
			backend: "claude",
			script: []wirePermission{{
				Notify:  map[string]any{"sessionUpdate": "tool_call", "toolCallId": "c2", "title": "Fetch", "status": "pending", "rawInput": map[string]any{"url": "https://blocked.example.invalid/x"}, "_meta": claudeToolMeta("WebFetch")},
				Request: map[string]any{"toolCallId": "c2", "title": "Fetch"},
			}},
			want: []string{"permission:c2:reject"},
		},
		{
			name:    "codex untitled request with rawInput command and cwd",
			backend: "codex",
			script: []wirePermission{
				{Request: map[string]any{"toolCallId": "x1", "kind": "execute", "rawInput": map[string]any{"command": blocked, "cwd": "/tmp"}}},
				{Request: map[string]any{"toolCallId": "x2", "kind": "execute", "rawInput": map[string]any{"command": "curl https://allowed.example.invalid", "cwd": "/tmp"}}},
			},
			want: []string{"permission:x1:reject", "permission:x2:allow"},
		},
		{
			name:    "cursor backticked title and no rawInput",
			backend: "cursor",
			script: []wirePermission{{
				Request: map[string]any{"toolCallId": "u1", "kind": "execute", "title": "`" + blocked + "`"},
			}},
			want: []string{"permission:u1:reject"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := runWirePermissions(t, fixtureStep{backend: tc.backend, agent: resolvedFixtureAgent(tc.backend, "", ""), guard: outboundGuard()}, tc.script)
			if !reflect.DeepEqual(out.permissions, tc.want) {
				t.Fatalf("permissions = %q, want %q; rpc log:\n%s", out.permissions, tc.want, out.log)
			}
			if len(out.findings) != 1 || out.findings[0].Monitor != "non-allowlisted-host" || out.findings[0].Tier != sentinel.TierGuard {
				t.Fatalf("findings = %+v, want one non-allowlisted-host guard finding", out.findings)
			}
		})
	}
}

// TestGuardSecretInWriteOnWire proves the secret-in-write rule sees each
// backend's edit content: Codex's cached tool_call diff (its request carries
// none), Cursor's request content diff, and Claude's rawInput. A guarded edit
// whose content cannot be resolved is denied.
func TestGuardSecretInWriteOnWire(t *testing.T) {
	secret := "AKIA" + "ABCDEFGHIJKLMNOP"
	diff := func(path, text string) []any {
		return []any{map[string]any{"type": "diff", "path": path, "oldText": "", "newText": text}}
	}
	for _, tc := range []struct {
		name         string
		backend      string
		script       []wirePermission
		want         []string
		wantFindings int
	}{
		{
			name:    "codex cached tool_call diff and a bare edit request",
			backend: "codex",
			script: []wirePermission{{
				Notify:  map[string]any{"sessionUpdate": "tool_call", "toolCallId": "e1", "title": "Edit config.env", "kind": "edit", "status": "pending", "content": diff("/tmp/config.env", "KEY="+secret)},
				Request: map[string]any{"toolCallId": "e1", "kind": "edit"},
			}},
			want: []string{"permission:e1:reject"}, wantFindings: 1,
		},
		{
			name:    "cursor request content diff",
			backend: "cursor",
			script: []wirePermission{{
				Request: map[string]any{"toolCallId": "e2", "kind": "edit", "title": "Edit config.env", "content": diff("/tmp/config.env", "KEY="+secret)},
			}},
			want: []string{"permission:e2:reject"}, wantFindings: 1,
		},
		{
			name:    "claude rawInput",
			backend: "claude",
			script: []wirePermission{{
				Notify:  map[string]any{"sessionUpdate": "tool_call", "toolCallId": "e3", "title": "Write config.env", "kind": "edit", "status": "pending", "_meta": claudeToolMeta("Write")},
				Request: map[string]any{"toolCallId": "e3", "title": "Write config.env", "rawInput": map[string]any{"file_path": "/tmp/config.env", "content": "KEY=" + secret}},
			}},
			want: []string{"permission:e3:reject"}, wantFindings: 1,
		},
		{
			// codex-acp emits one diff per file of a patch in one tool_call.
			name:    "codex multi-file patch with the secret in a later diff",
			backend: "codex",
			script: []wirePermission{{
				Notify: map[string]any{"sessionUpdate": "tool_call", "toolCallId": "e6", "title": "Editing files", "kind": "edit", "status": "pending", "content": append(
					diff("/tmp/notes.md", "hello"), diff("/tmp/config.env", "KEY="+secret)...)},
				Request: map[string]any{"toolCallId": "e6", "kind": "edit"},
			}},
			want: []string{"permission:e6:reject"}, wantFindings: 1,
		},
		{
			name:    "cursor request with the secret in a later diff",
			backend: "cursor",
			script: []wirePermission{{
				Request: map[string]any{"toolCallId": "e7", "kind": "edit", "title": "Edit files", "content": append(
					diff("/tmp/notes.md", "hello"), diff("/tmp/config.env", "KEY="+secret)...)},
			}},
			want: []string{"permission:e7:reject"}, wantFindings: 1,
		},
		{
			name:    "a clean edit is allowed once",
			backend: "cursor",
			script: []wirePermission{{
				Request: map[string]any{"toolCallId": "e4", "kind": "edit", "title": "Edit notes.md", "content": diff("/tmp/notes.md", "hello")},
			}},
			want: []string{"permission:e4:allow"},
		},
		{
			// No tool_call ever arrives, so the request waits out the cache
			// and is denied for its unresolvable content.
			name:    "guarded edit with unresolvable content is denied",
			backend: "codex",
			script:  []wirePermission{{Request: map[string]any{"toolCallId": "e5", "kind": "edit"}}},
			want:    []string{"permission:e5:reject"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := runWirePermissions(t, fixtureStep{backend: tc.backend, agent: resolvedFixtureAgent(tc.backend, "", ""), guard: outboundGuard()}, tc.script)
			if !reflect.DeepEqual(out.permissions, tc.want) {
				t.Fatalf("permissions = %q, want %q; rpc log:\n%s", out.permissions, tc.want, out.log)
			}
			if len(out.findings) != tc.wantFindings {
				t.Fatalf("findings = %+v, want %d", out.findings, tc.wantFindings)
			}
			for _, f := range out.findings {
				if f.Monitor != "secret-in-write" || strings.Contains(f.Detail, secret) {
					t.Fatalf("finding = %+v, want a redacted secret-in-write finding", f)
				}
			}
		})
	}
}

// TestExitPlanModeDenied proves an agent can never leave plan mode on its own:
// the adapter's ExitPlanMode request is answered with its "keep planning"
// reject option whether or not the guard is on.
func TestExitPlanModeDenied(t *testing.T) {
	script := []wirePermission{{
		Notify:  map[string]any{"sessionUpdate": "tool_call", "toolCallId": "p1", "title": "Ready to code?", "kind": "switch_mode", "status": "pending", "rawInput": map[string]any{"plan": "do it"}, "_meta": claudeToolMeta("ExitPlanMode")},
		Request: map[string]any{"toolCallId": "p1", "title": "Ready to code?", "kind": "switch_mode", "rawInput": map[string]any{"plan": "do it"}},
		Options: "exit_plan",
	}}
	for _, guarded := range []bool{true, false} {
		name := "security off"
		fs := fixtureStep{backend: "claude", agent: agentcfg.ClaudeAgent{PermissionMode: "plan"}}
		if guarded {
			name = "security on"
			fs.guard = outboundGuard()
		}
		t.Run(name, func(t *testing.T) {
			out := runWirePermissions(t, fs, script)
			if want := []string{"permission:p1:plan"}; !reflect.DeepEqual(out.permissions, want) {
				t.Fatalf("permissions = %q, want %q", out.permissions, want)
			}
		})
	}
}

// TestUnguardedPermissionPath proves an unguarded step still applies the
// Claude tool-list second layer, and no longer rejects every prompt.
func TestUnguardedPermissionPath(t *testing.T) {
	call := func(id, tool string, input map[string]any) wirePermission {
		return wirePermission{
			Notify:  map[string]any{"sessionUpdate": "tool_call", "toolCallId": id, "title": tool, "status": "pending", "rawInput": input, "_meta": claudeToolMeta(tool)},
			Request: map[string]any{"toolCallId": id, "title": tool, "rawInput": input},
		}
	}
	bash := call("b1", "Bash", map[string]any{"command": "ls"})
	edit := call("e1", "Edit", map[string]any{"file_path": "/tmp/x", "old_string": "a", "new_string": "b"})
	read := call("r1", "Read", map[string]any{"file_path": "/tmp/x"})
	ask := call("q1", agentcfg.AskUserQuestion, map[string]any{"questions": []any{}})
	for _, tc := range []struct {
		name    string
		agent   agentcfg.ClaudeAgent
		askUser bool
		script  []wirePermission
		want    []string
	}{
		{
			name:   "disallowed Bash is denied and Edit is allowed",
			agent:  agentcfg.ClaudeAgent{DisallowedTools: []string{"Bash"}},
			script: []wirePermission{bash, edit},
			want:   []string{"permission:b1:reject", "permission:e1:allow"},
		},
		{
			name:    "empty tools denies Read but not AskUserQuestion",
			agent:   agentcfg.ClaudeAgent{Tools: []string{}},
			askUser: true,
			script:  []wirePermission{read, ask},
			want:    []string{"permission:r1:reject", "permission:q1:allow"},
		},
		{
			name:   "plan mode denies Edit and Bash and allows Read",
			agent:  agentcfg.ClaudeAgent{PermissionMode: "plan"},
			script: []wirePermission{edit, bash, read},
			want:   []string{"permission:e1:reject", "permission:b1:reject", "permission:r1:allow"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := runWirePermissions(t, fixtureStep{backend: "claude", agent: tc.agent, askUser: tc.askUser}, tc.script)
			if !reflect.DeepEqual(out.permissions, tc.want) {
				t.Fatalf("permissions = %q, want %q; rpc log:\n%s", out.permissions, tc.want, out.log)
			}
		})
	}
}
