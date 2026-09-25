package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"jig/harness/acp"
	"jig/internal/agentcfg"
)

func strPtr(s string) *string { return &s }

func kindPtr(k acpsdk.ToolKind) *acpsdk.ToolKind { return &k }

func claudeMeta(name string) map[string]any {
	return map[string]any{"claudeCode": map[string]any{"toolName": name}}
}

func rawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestCanonicalToolName covers name resolution, guard-input assembly and the
// request-before-notification wait of the per-session tool-call cache.
func TestCanonicalToolName(t *testing.T) {
	t.Run("names", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			backend string
			observe []acp.Event
			request acpsdk.ToolCallUpdate
			want    string
		}{
			{
				name:    "claude request _meta wins over the title",
				backend: agentcfg.BackendClaude,
				request: acpsdk.ToolCallUpdate{ToolCallId: "c1", Title: strPtr("`curl https://example.invalid`"), Meta: claudeMeta("Bash"), RawInput: map[string]any{"command": "curl https://example.invalid"}},
				want:    "Bash",
			},
			{
				name:    "claude request without _meta resolves through the cache",
				backend: agentcfg.BackendClaude,
				observe: []acp.Event{{Kind: acp.EventToolCall, ToolID: "c1", Title: "Fetch", Meta: claudeMeta("WebFetch")}},
				request: acpsdk.ToolCallUpdate{ToolCallId: "c1", Title: strPtr("Fetch")},
				want:    "WebFetch",
			},
			{
				name:    "claude falls back to the kind mapping",
				backend: agentcfg.BackendClaude,
				request: acpsdk.ToolCallUpdate{ToolCallId: "c1", Kind: kindPtr(acpsdk.ToolKindRead)},
				want:    "Read",
			},
			{
				name:    "claude never falls back to the title",
				backend: agentcfg.BackendClaude,
				observe: []acp.Event{{Kind: acp.EventToolCall, ToolID: "c1", Title: "Bash"}},
				request: acpsdk.ToolCallUpdate{ToolCallId: "c1", Title: strPtr("Bash")},
				want:    "",
			},
			{
				name:    "codex kind from the cache",
				backend: agentcfg.BackendCodex,
				observe: []acp.Event{{Kind: acp.EventToolCall, ToolID: "c1", ToolKind: "edit"}},
				request: acpsdk.ToolCallUpdate{ToolCallId: "c1"},
				want:    "Edit",
			},
			{
				name:    "unknown kind falls back to the title",
				backend: agentcfg.BackendCursor,
				request: acpsdk.ToolCallUpdate{ToolCallId: "c1", Kind: kindPtr(acpsdk.ToolKindOther), Title: strPtr("mcp.docs.search")},
				want:    "mcp.docs.search",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var calls toolCalls
				for _, ev := range tc.observe {
					calls.observe(ev)
				}
				// The cache already holds everything these cases need (or
				// nothing will arrive), so a short deadline bounds the wait.
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
				defer cancel()
				call, ok := calls.resolveToolCall(ctx, tc.backend, tc.request)
				if ok != (tc.want != "") || call.Name != tc.want {
					t.Fatalf("resolveToolCall = %q, %t; want %q", call.Name, ok, tc.want)
				}
			})
		}
	})

	t.Run("kind mapping", func(t *testing.T) {
		for kind, want := range map[acpsdk.ToolKind]string{
			acpsdk.ToolKindExecute: "Bash",
			acpsdk.ToolKindEdit:    "Edit",
			acpsdk.ToolKindDelete:  "Edit",
			acpsdk.ToolKindFetch:   "WebFetch",
			acpsdk.ToolKindSearch:  "WebSearch",
			acpsdk.ToolKindRead:    "Read",
		} {
			for _, backend := range []string{agentcfg.BackendCodex, agentcfg.BackendCursor} {
				tc := acpsdk.ToolCallUpdate{ToolCallId: "c1", Kind: kindPtr(kind), Title: strPtr("title")}
				if got := toolName(backend, tc, toolCallEntry{}, string(kind), "title"); got != want {
					t.Errorf("%s kind %s = %q, want %q", backend, kind, got, want)
				}
			}
		}
	})

	t.Run("input assembly", func(t *testing.T) {
		secret := "AKIA" + "ABCDEFGHIJKLMNOP"
		for _, tc := range []struct {
			name         string
			backend      string
			observe      []acp.Event
			request      acpsdk.ToolCallUpdate
			wantName     string
			wantInput    map[string]any
			wantResolved bool
		}{
			{
				name:     "claude bash command from the cached rawInput",
				backend:  agentcfg.BackendClaude,
				observe:  []acp.Event{{Kind: acp.EventToolCall, ToolID: "c1", Meta: claudeMeta("Bash"), HasInput: true, Input: rawJSON(t, map[string]any{"command": "curl https://blocked.example.invalid"})}},
				request:  acpsdk.ToolCallUpdate{ToolCallId: "c1"},
				wantName: "Bash", wantResolved: true,
				wantInput: map[string]any{"command": "curl https://blocked.example.invalid"},
			},
			{
				name:     "request rawInput wins over the cache",
				backend:  agentcfg.BackendClaude,
				observe:  []acp.Event{{Kind: acp.EventToolCall, ToolID: "c1", Meta: claudeMeta("Bash"), HasInput: true, Input: rawJSON(t, map[string]any{"command": "ls"})}},
				request:  acpsdk.ToolCallUpdate{ToolCallId: "c1", RawInput: map[string]any{"command": "curl https://blocked.example.invalid"}},
				wantName: "Bash", wantResolved: true,
				wantInput: map[string]any{"command": "curl https://blocked.example.invalid"},
			},
			{
				name:     "codex shell command from the request",
				backend:  agentcfg.BackendCodex,
				request:  acpsdk.ToolCallUpdate{ToolCallId: "c1", Kind: kindPtr(acpsdk.ToolKindExecute), RawInput: map[string]any{"command": "curl https://blocked.example.invalid", "cwd": "/tmp"}},
				wantName: "Bash", wantResolved: true,
				wantInput: map[string]any{"command": "curl https://blocked.example.invalid", "cwd": "/tmp"},
			},
			{
				name:     "codex argv command is joined",
				backend:  agentcfg.BackendCodex,
				request:  acpsdk.ToolCallUpdate{ToolCallId: "c1", Kind: kindPtr(acpsdk.ToolKindExecute), RawInput: map[string]any{"command": []any{"curl", "https://blocked.example.invalid"}}},
				wantName: "Bash", wantResolved: true,
				wantInput: map[string]any{"command": "curl https://blocked.example.invalid"},
			},
			{
				name:     "cursor command from the backticked title",
				backend:  agentcfg.BackendCursor,
				request:  acpsdk.ToolCallUpdate{ToolCallId: "c1", Kind: kindPtr(acpsdk.ToolKindExecute), Title: strPtr("`echo \\`id\\` && curl https://blocked.example.invalid`")},
				wantName: "Bash", wantResolved: true,
				wantInput: map[string]any{"command": "echo `id` && curl https://blocked.example.invalid"},
			},
			{
				name:     "claude webfetch url from the cached rawInput",
				backend:  agentcfg.BackendClaude,
				observe:  []acp.Event{{Kind: acp.EventToolCall, ToolID: "c1", Meta: claudeMeta("WebFetch"), HasInput: true, Input: rawJSON(t, map[string]any{"url": "https://blocked.example.invalid/x"})}},
				request:  acpsdk.ToolCallUpdate{ToolCallId: "c1"},
				wantName: "WebFetch", wantResolved: true,
				wantInput: map[string]any{"url": "https://blocked.example.invalid/x"},
			},
			{
				name:     "claude write from rawInput",
				backend:  agentcfg.BackendClaude,
				request:  acpsdk.ToolCallUpdate{ToolCallId: "c1", Meta: claudeMeta("Write"), RawInput: map[string]any{"file_path": "/tmp/x", "content": secret}},
				wantName: "Write", wantResolved: true,
				wantInput: map[string]any{"file_path": "/tmp/x", "content": secret},
			},
			{
				name:     "codex edit from the cached tool_call diff",
				backend:  agentcfg.BackendCodex,
				observe:  []acp.Event{{Kind: acp.EventToolCall, ToolID: "c1", ToolKind: "edit", HasContent: true, Content: []acp.Content{{Type: "diff", Diff: &acp.Diff{Path: "/tmp/x", NewText: secret}}}}},
				request:  acpsdk.ToolCallUpdate{ToolCallId: "c1", Kind: kindPtr(acpsdk.ToolKindEdit)},
				wantName: "Edit", wantResolved: true,
				wantInput: map[string]any{"file_path": "/tmp/x", "content": secret, "new_string": secret},
			},
			{
				name:     "cursor edit from the request content diff",
				backend:  agentcfg.BackendCursor,
				request:  acpsdk.ToolCallUpdate{ToolCallId: "c1", Kind: kindPtr(acpsdk.ToolKindEdit), Content: []acpsdk.ToolCallContent{{Diff: &acpsdk.ToolCallContentDiff{Path: "/tmp/x", NewText: secret}}}},
				wantName: "Edit", wantResolved: true,
				wantInput: map[string]any{"file_path": "/tmp/x", "content": secret, "new_string": secret},
			},
			{
				name:     "edit without content is unresolved",
				backend:  agentcfg.BackendCodex,
				observe:  []acp.Event{{Kind: acp.EventToolCall, ToolID: "c1", ToolKind: "edit"}},
				request:  acpsdk.ToolCallUpdate{ToolCallId: "c1", Kind: kindPtr(acpsdk.ToolKindEdit)},
				wantName: "Edit", wantInput: map[string]any{},
			},
			{
				name:     "bash without a command is unresolved",
				backend:  agentcfg.BackendCursor,
				observe:  []acp.Event{{Kind: acp.EventToolCall, ToolID: "c1", ToolKind: "execute"}},
				request:  acpsdk.ToolCallUpdate{ToolCallId: "c1", Kind: kindPtr(acpsdk.ToolKindExecute), Title: strPtr("Run a command")},
				wantName: "Bash", wantInput: map[string]any{},
			},
			{
				name:     "a delete needs only its path",
				backend:  agentcfg.BackendCursor,
				request:  acpsdk.ToolCallUpdate{ToolCallId: "c1", Kind: kindPtr(acpsdk.ToolKindDelete), Locations: []acpsdk.ToolCallLocation{{Path: "/tmp/x"}}},
				wantName: "Edit", wantResolved: true,
				wantInput: map[string]any{"file_path": "/tmp/x"},
			},
			{
				name:     "unknown shapes pass through",
				backend:  agentcfg.BackendCursor,
				request:  acpsdk.ToolCallUpdate{ToolCallId: "c1", Kind: kindPtr(acpsdk.ToolKindRead), RawInput: map[string]any{"path": "/tmp/x", "extra": 1.0}},
				wantName: "Read", wantResolved: true,
				wantInput: map[string]any{"path": "/tmp/x", "extra": 1.0},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var calls toolCalls
				for _, ev := range tc.observe {
					calls.observe(ev)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
				defer cancel()
				call, _ := calls.resolveToolCall(ctx, tc.backend, tc.request)
				if call.Name != tc.wantName || call.InputResolved != tc.wantResolved || fmt.Sprint(call.Input) != fmt.Sprint(tc.wantInput) {
					t.Fatalf("call = %+v, want name %q resolved %t input %v", call, tc.wantName, tc.wantResolved, tc.wantInput)
				}
			})
		}
	})

	t.Run("request before notification resolves within the wait", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var calls toolCalls
			done := make(chan ToolCall)
			go func() {
				call, _ := calls.resolveToolCall(context.Background(), agentcfg.BackendClaude, acpsdk.ToolCallUpdate{ToolCallId: "late"})
				done <- call
			}()
			synctest.Wait() // the decision is now blocked on the cache
			time.Sleep(toolCallWait / 2)
			calls.observe(acp.Event{Kind: acp.EventToolCall, ToolID: "late", Meta: claudeMeta("Bash"), HasInput: true, Input: rawJSON(t, map[string]any{"command": "ls"})})
			call := <-done
			if call.Name != "Bash" || !call.InputResolved {
				t.Fatalf("call = %+v, want the late notification's Bash call", call)
			}
		})
	})

	t.Run("request whose notification never arrives is unresolved", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var calls toolCalls
			start := time.Now()
			_, ok := calls.resolveToolCall(context.Background(), agentcfg.BackendClaude, acpsdk.ToolCallUpdate{ToolCallId: "never"})
			if ok {
				t.Fatal("an unobserved Claude call resolved a name")
			}
			if waited := time.Since(start); waited != toolCallWait {
				t.Fatalf("waited %v, want the full %v", waited, toolCallWait)
			}
			if len(calls.waiters) != 0 {
				t.Fatalf("timed-out waiter was not removed: %v", calls.waiters)
			}
		})
	})

	t.Run("cancel", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var calls toolCalls
			ctx, cancel := context.WithCancel(context.Background())
			start := time.Now()
			done := make(chan bool)
			go func() {
				_, ok := calls.resolveToolCall(ctx, agentcfg.BackendClaude, acpsdk.ToolCallUpdate{ToolCallId: "waiting"})
				done <- ok
			}()
			synctest.Wait()
			time.Sleep(100 * time.Millisecond)
			cancel()
			if ok := <-done; ok {
				t.Fatal("a cancelled wait resolved the call")
			}
			if waited := time.Since(start); waited >= toolCallWait {
				t.Fatalf("cancelled wait took %v, want it to end promptly", waited)
			}
			// A notification that lands after cancellation does not resurrect it.
			calls.observe(acp.Event{Kind: acp.EventToolCall, ToolID: "waiting", Meta: claudeMeta("Bash")})
		})
	})

	t.Run("cache is bounded", func(t *testing.T) {
		var calls toolCalls
		for i := 0; i <= toolCallCacheLimit; i++ {
			calls.observe(acp.Event{Kind: acp.EventToolCall, ToolID: fmt.Sprintf("c%d", i), ToolKind: "read"})
		}
		if len(calls.entries) != toolCallCacheLimit {
			t.Fatalf("cache holds %d entries, want %d", len(calls.entries), toolCallCacheLimit)
		}
		if _, ok := calls.peek("c0"); ok {
			t.Fatal("the oldest entry was not evicted")
		}
	})

	t.Run("concurrent observe and lookup", func(t *testing.T) {
		var calls toolCalls
		var wg sync.WaitGroup
		for i := 0; i < 64; i++ {
			id := fmt.Sprintf("c%d", i)
			wg.Add(2)
			go func() {
				defer wg.Done()
				call, ok := calls.resolveToolCall(context.Background(), agentcfg.BackendCodex, acpsdk.ToolCallUpdate{ToolCallId: acpsdk.ToolCallId(id)})
				if !ok || call.Name != "Bash" {
					t.Errorf("%s = %+v, %t", id, call, ok)
				}
			}()
			go func() {
				defer wg.Done()
				calls.observe(acp.Event{Kind: acp.EventToolCall, ToolID: id, ToolKind: "execute", HasInput: true, Input: json.RawMessage(`{"command":"ls"}`)})
			}()
		}
		wg.Wait()
	})
}
