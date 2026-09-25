package harness

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"jig/harness/acp"
	"jig/internal/agentcfg"
)

// toolCallWait bounds how long a permission request waits for its tool call's
// notification. acp-go-sdk handles each inbound request in its own goroutine
// but notifications on a sequential queue, so a request can be decided before
// the tool_call the adapter sent first has been observed.
const toolCallWait = 2 * time.Second

// toolCallCacheLimit bounds the per-session cache; the oldest entry is
// evicted past it.
const toolCallCacheLimit = 1024

// toolCallEntry is the latest state seen for one toolCallId.
type toolCallEntry struct {
	// name is Claude's canonical _meta.claudeCode.toolName.
	name     string
	kind     string
	title    string
	rawInput map[string]any
	// diffs holds every diff content item: one tool call can change several
	// files (Codex emits one diff per file of a patch).
	diffs []editDiff
	paths []string
}

// editDiff is one file change carried by a tool call's diff content.
type editDiff struct {
	path    string
	newText string
}

// notificationDiffs returns every diff in an observed notification's content.
func notificationDiffs(content []acp.Content) []editDiff {
	var diffs []editDiff
	for _, item := range content {
		if item.Diff != nil {
			diffs = append(diffs, editDiff{path: item.Diff.Path, newText: item.Diff.NewText})
		}
	}
	return diffs
}

// requestDiffs returns every diff in a permission request's content.
func requestDiffs(content []acpsdk.ToolCallContent) []editDiff {
	var diffs []editDiff
	for _, item := range content {
		if item.Diff != nil {
			diffs = append(diffs, editDiff{path: item.Diff.Path, newText: item.Diff.NewText})
		}
	}
	return diffs
}

// toolCalls is a session's tool-call cache, filled from tool_call and
// tool_call_update notifications and read by permission decisions. It is safe
// for concurrent use: notifications and permission requests arrive on
// different goroutines.
type toolCalls struct {
	mu      sync.Mutex
	entries map[string]*toolCallEntry
	order   []string
	waiters map[string][]chan struct{}
}

// observe records a tool call notification and wakes any decision waiting on
// its id.
func (c *toolCalls) observe(ev acp.Event) {
	if (ev.Kind != acp.EventToolCall && ev.Kind != acp.EventToolCallUpdate) || ev.ToolID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]*toolCallEntry)
	}
	e := c.entries[ev.ToolID]
	if e == nil {
		e = &toolCallEntry{}
		c.entries[ev.ToolID] = e
		c.order = append(c.order, ev.ToolID)
		if len(c.order) > toolCallCacheLimit {
			delete(c.entries, c.order[0])
			c.order = c.order[1:]
		}
	}
	if name := metaToolName(ev.Meta); name != "" {
		e.name = name
	}
	if ev.ToolKind != "" {
		e.kind = ev.ToolKind
	}
	if ev.Title != "" {
		e.title = ev.Title
	}
	if ev.HasInput && len(ev.Input) > 0 {
		var input map[string]any
		if err := json.Unmarshal(ev.Input, &input); err == nil && input != nil {
			// Replaced, never mutated, so a copied entry can share it.
			e.rawInput = input
		}
	}
	if ev.HasContent {
		if diffs := notificationDiffs(ev.Content); len(diffs) > 0 {
			// Replaced, never mutated, so a copied entry can share it.
			e.diffs = diffs
		}
	}
	if ev.HasLocations {
		e.paths = nil
		for _, location := range ev.Locations {
			e.paths = append(e.paths, location.Path)
		}
	}
	for _, ch := range c.waiters[ev.ToolID] {
		close(ch)
	}
	delete(c.waiters, ev.ToolID)
}

// peek returns the cached entry without waiting.
func (c *toolCalls) peek(id string) (toolCallEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[id]; ok {
		return *e, true
	}
	return toolCallEntry{}, false
}

// lookup returns the cached entry, waiting up to toolCallWait for it to be
// observed. It returns a miss on timeout or when ctx is cancelled.
func (c *toolCalls) lookup(ctx context.Context, id string) (toolCallEntry, bool) {
	c.mu.Lock()
	if e, ok := c.entries[id]; ok {
		c.mu.Unlock()
		return *e, true
	}
	if c.waiters == nil {
		c.waiters = make(map[string][]chan struct{})
	}
	ch := make(chan struct{})
	c.waiters[id] = append(c.waiters[id], ch)
	c.mu.Unlock()

	timer := time.NewTimer(toolCallWait)
	defer timer.Stop()
	select {
	case <-ch:
		return c.peek(id)
	case <-timer.C:
	case <-ctx.Done():
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	waiting := c.waiters[id]
	for i, w := range waiting {
		if w == ch {
			c.waiters[id] = append(waiting[:i:i], waiting[i+1:]...)
			break
		}
	}
	if len(c.waiters[id]) == 0 {
		delete(c.waiters, id)
	}
	return toolCallEntry{}, false
}

// resolveToolCall turns a permission request into the canonical call jig's
// permission callback decides on. The request is merged with the cached
// notification state, the request winning; a request that cannot be resolved
// from what is already cached waits for its notification. ok is false when no
// tool name resolves (or ctx is cancelled while waiting): the caller denies.
func (c *toolCalls) resolveToolCall(ctx context.Context, backend string, tc acpsdk.ToolCallUpdate) (ToolCall, bool) {
	id := string(tc.ToolCallId)
	entry, found := c.peek(id)
	call := assembleToolCall(backend, tc, entry)
	if (call.Name == "" || !call.InputResolved) && !found && id != "" {
		entry, found = c.lookup(ctx, id)
		if ctx.Err() != nil {
			return ToolCall{}, false
		}
		if found {
			call = assembleToolCall(backend, tc, entry)
		}
	}
	return call, call.Name != ""
}

// permissionDecider adapts a PermissionFn to the ACP client's Decider. A call
// whose tool identity cannot be resolved is denied here, before permission
// runs, on every step.
func permissionDecider(backend string, calls *toolCalls, permission PermissionFn) acp.Decider {
	if permission == nil {
		return nil
	}
	return func(ctx context.Context, tc acpsdk.ToolCallUpdate) bool {
		call, ok := calls.resolveToolCall(ctx, backend, tc)
		if !ok {
			return false
		}
		return permission(call).Allow
	}
}

func assembleToolCall(backend string, tc acpsdk.ToolCallUpdate, e toolCallEntry) ToolCall {
	kind := e.kind
	if tc.Kind != nil && *tc.Kind != "" {
		kind = string(*tc.Kind)
	}
	title := e.title
	requestTitle := ""
	if tc.Title != nil {
		requestTitle = *tc.Title
		if requestTitle != "" {
			title = requestTitle
		}
	}
	name := toolName(backend, tc, e, kind, title)
	input, resolved := guardInput(backend, name, kind, requestTitle, tc, e)
	return ToolCall{ID: string(tc.ToolCallId), Name: name, Input: input, InputResolved: resolved}
}

// toolName resolves the canonical tool name:
//   - Claude: the request's _meta.claudeCode.toolName, then the cached name,
//     then the kind mapping.
//   - Codex and Cursor: the kind mapping, then the title for other kinds (for
//     example an MCP tool).
//
// An empty result is an unresolved tool identity.
func toolName(backend string, tc acpsdk.ToolCallUpdate, e toolCallEntry, kind, title string) string {
	if backend == agentcfg.BackendClaude {
		if name := metaToolName(tc.Meta); name != "" {
			return name
		}
		if e.name != "" {
			return e.name
		}
		return kindToolName(kind)
	}
	if name := kindToolName(kind); name != "" {
		return name
	}
	return title
}

// kindToolName maps an ACP tool kind onto jig's tool names.
func kindToolName(kind string) string {
	switch kind {
	case "execute":
		return "Bash"
	case "edit", "delete":
		return "Edit"
	case "fetch":
		return "WebFetch"
	case "search":
		return "WebSearch"
	case "read":
		return "Read"
	}
	return ""
}

// guardInput assembles the input the Tier-1 rules read (command for Bash, url
// for WebFetch, file_path and content for edits) from the request and the
// cache, the request winning. resolved is false when a Bash call has no
// command or an edit has no path or content; guarded steps deny those calls.
func guardInput(backend, name, kind, requestTitle string, tc acpsdk.ToolCallUpdate, e toolCallEntry) (map[string]any, bool) {
	input := make(map[string]any, len(e.rawInput))
	for k, v := range e.rawInput {
		input[k] = v
	}
	if request, ok := tc.RawInput.(map[string]any); ok {
		for k, v := range request {
			input[k] = v
		}
	}
	switch name {
	case "Bash":
		command := commandString(input["command"])
		if backend == agentcfg.BackendCursor {
			// Cursor requests carry the command only in a backticked title.
			if titled := backtickedCommand(requestTitle); titled != "" {
				command = titled
			}
		}
		if command == "" {
			return input, false
		}
		input["command"] = command
		return input, true
	case "Edit", "Write", "MultiEdit":
		if backend != agentcfg.BackendClaude {
			return diffInput(backend, kind, tc, e, input)
		}
		_, hasPath := input["file_path"].(string)
		return input, hasPath && hasEditContent(input)
	}
	return input, true
}

// diffInput fills file_path and content from the diffs: the request's content
// on Cursor, the cached tool_call on Codex (whose requests carry neither). A
// call can change several files, so content joins every diff's new text and
// file_paths lists every path; file_path is the first. The content is also
// exposed as new_string, which the secret-in-write rule reads for Edit. A
// delete needs only its path.
func diffInput(backend, kind string, tc acpsdk.ToolCallUpdate, e toolCallEntry, input map[string]any) (map[string]any, bool) {
	diffs := e.diffs
	if backend == agentcfg.BackendCursor {
		if requested := requestDiffs(tc.Content); len(requested) > 0 {
			diffs = requested
		}
	}
	path, ok := "", len(diffs) > 0
	if ok {
		texts := make([]string, 0, len(diffs))
		paths := make([]any, 0, len(diffs))
		for _, diff := range diffs {
			if diff.path == "" {
				ok = false
			}
			texts = append(texts, diff.newText)
			paths = append(paths, diff.path)
		}
		path = diffs[0].path
		text := strings.Join(texts, "\n")
		input["file_path"] = path
		input["file_paths"] = paths
		input["content"] = text
		input["new_string"] = text
	}
	if kind == "delete" {
		if path == "" {
			for _, location := range tc.Locations {
				path = location.Path
			}
			if path == "" && len(e.paths) > 0 {
				path = e.paths[0]
			}
			if path != "" {
				input["file_path"] = path
			}
		}
		return input, path != ""
	}
	return input, ok && path != ""
}

func hasEditContent(input map[string]any) bool {
	for _, key := range []string{"content", "new_string"} {
		if _, ok := input[key].(string); ok {
			return true
		}
	}
	_, ok := input["edits"].([]any)
	return ok
}

// commandString normalizes a shell command given as a string or an argv list.
func commandString(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, part := range v {
			if s, ok := part.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// backtickedCommand extracts the command from a Cursor title such as
// "`curl https://example.invalid`", unescaping backslash-escaped backticks.
func backtickedCommand(title string) string {
	start := strings.IndexByte(title, '`')
	end := strings.LastIndexByte(title, '`')
	if start < 0 || end <= start {
		return ""
	}
	return strings.ReplaceAll(title[start+1:end], "\\`", "`")
}

// metaToolName reads Claude's canonical name from _meta.claudeCode.toolName.
func metaToolName(meta map[string]any) string {
	claudeCode, _ := meta["claudeCode"].(map[string]any)
	name, _ := claudeCode["toolName"].(string)
	return name
}
