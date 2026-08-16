// Package harness defines jig's own seam between AgentExecutor and whatever
// agent backend actually runs a step. A Harness (ClaudeHarness, AcpHarness)
// names jig's Go type; a backend (Claude, Cursor, Gemini) names the
// vendor/model/CLI it targets — the two are not synonyms, since one Harness
// could in principle target more than one backend. This file and
// capability.go define the seam only: no SDK import of any kind, so
// engine/runner tests can exercise it via FakeHarness without any real
// backend.
package harness

import (
	"context"
	"encoding/json"
)

// EventType is the discriminator for an Event, mirroring transcript.BlockType
// so a harness's output maps onto the transcript's normalization boundary
// with no parallel model.
type EventType string

const (
	EventText       EventType = "text"
	EventThinking   EventType = "thinking"
	EventToolUse    EventType = "tool_use"
	EventToolResult EventType = "tool_result"

	// EventTextDelta is an incremental, not-yet-finalized text preview (the
	// "live-typing tail"), emitted only when CapPartialStreaming was
	// requested. It is never written to the transcript — only the finalized
	// EventText block is authoritative.
	EventTextDelta EventType = "text_delta"

	// EventSessionID reports the backend's session/conversation id as soon as
	// it becomes known, which may be well before EventResult (e.g. Claude's
	// partial-message stream surfaces it on the first chunk). It may fire more
	// than once; callers keep the first non-empty value, mirroring the
	// existing "earliest reliable source" capture idiom.
	EventSessionID EventType = "session_id"

	// EventResult is the terminal event for a turn: success or failure, plus
	// whatever cost/usage/structured-output the backend reported. Exactly one
	// EventResult, if any, precedes the Messages() channel closing; a channel
	// that closes without one means the connection dropped before a result
	// was reported (e.g. a deliberate stop).
	EventResult EventType = "result"

	// EventAssistantEnd marks the end of one assistant turn: the consumer
	// flushes whatever Text/Thinking/ToolUse events it has buffered since the
	// last boundary into a single transcript Entry, matching the "one Entry
	// per SDK message" grouping the pre-harness capture path produced (an
	// empty buffer is simply not flushed, mirroring the existing
	// skip-empty-entries behavior).
	EventAssistantEnd EventType = "assistant_end"

	// EventUserEnd marks the end of one user-role turn (a batch of tool
	// results), the same flush-boundary role for ToolResult events that
	// EventAssistantEnd plays for assistant events.
	EventUserEnd EventType = "user_end"

	// EventSystemText is a standalone system-role text event (e.g. an
	// assistant-error note), written as its own single-block transcript Entry
	// with no buffering — unlike EventText it needs no matching *End marker.
	EventSystemText EventType = "system_text"
)

// Event is one unit of content a Session emits, normalized onto
// transcript.Block's categories (plus EventTextDelta/EventSessionID/
// EventResult, which have no transcript.Block analog — they carry
// session-lifecycle metadata AgentExecutor needs to build a *step.Result).
// The meaningful fields depend on Type, same convention as transcript.Block.
type Event struct {
	Type EventType

	// Text carries text/thinking/text_delta event content.
	Text string

	// ToolUseID correlates a tool_use event with its tool_result.
	ToolUseID string

	// Name is the tool name on a tool_use event.
	Name string

	// Input is the raw JSON of a tool_use event's arguments.
	Input json.RawMessage

	// Content is the tool_result payload.
	Content string

	// IsError marks a tool_result or result event that reported failure.
	IsError bool

	// ErrText is the human-readable failure reason on a failed result event.
	ErrText string

	// SessionID carries the backend's session/conversation id on a
	// session_id or result event.
	SessionID string

	// Subtype is the backend's terminal-outcome subtype (e.g. a clean finish
	// vs. hitting a turn/budget limit) on a result event.
	Subtype string

	// TotalCostUSD is the dollar cost reported for the turn on a result
	// event. A nil pointer means the backend did not report cost.
	TotalCostUSD *float64

	// Usage is the backend's token-usage map on a result event.
	Usage *map[string]any

	// Structured is the raw JSON structured-output envelope on a result
	// event, present only when CapStructuredOutput was requested and the
	// backend returned one.
	Structured json.RawMessage
}

// ToolResult is a tool outcome sent back into a live session (e.g. an
// AskUserQuestion answer, or a queued tool result), mirroring the shape of a
// tool_result Event.
type ToolResult struct {
	ToolUseID string
	Content   string
	IsError   bool
}

// Session is live for exactly one AgentExecutor.Execute call — one Open() per
// step-execution, not a long-lived handle reused across steps. Mid-turn human
// input goes to the same live Session via Send(), never a reconnect; resuming
// a stopped step is a separate mechanism (SessionSpec.Resume on a fresh
// Open()).
type Session interface {
	// Messages streams the session's events in order; the channel closes when
	// the turn ends (success, failure, or the context is cancelled).
	Messages() <-chan Event
	// Send injects a tool result into the running session.
	Send(ctx context.Context, result ToolResult) error
	// Close ends the session, releasing any underlying connection/subprocess.
	Close() error
}

// Harness is a jig-owned backend seam: AgentExecutor depends on this
// interface instead of calling any specific SDK directly.
type Harness interface {
	// Name identifies the harness (e.g. "claude", "acp") for error messages.
	Name() string
	// Capabilities reports what this harness supports, queryable before Open
	// is ever called — never inferred via runtime type assertion afterward.
	Capabilities() CapabilitySet
	// Open starts one session for one step-execution.
	Open(ctx context.Context, spec SessionSpec) (Session, error)
}
