// Package transcript defines the per-step agent transcript format and provides
// an append writer and a windowed reader for it.
//
// A transcript is an append-only JSONL file (one Entry per line) living beside
// the per-step result.json under .jig/runs/<run-id>/steps/<step-id>/. It is the
// durable source of truth for an agent's conversation: assistant text,
// reasoning, tool calls with their inputs, and tool results. The engine event
// bus carries only lightweight liveness signals; the full content rides this
// file so nothing is lost to a dropped channel send or a monitor re-entry.
//
// The package is pure data + file I/O: it imports nothing from engine, runner,
// or tui, mirroring the internal/step and internal/datastore style.
//
// # Compatibility contract
//
// The schema is versioned by SchemaVersion. Readers MUST tolerate unknown block
// types (rendering them as an "unsupported" placeholder rather than erroring) so
// a newer writer never breaks an older reader. Retries and loop iterations
// append to the same file; entries are distinguished by Attempt and Iteration.
package transcript

import "encoding/json"

// SchemaVersion is the current transcript schema version, stamped on every
// Entry as "v". Bump this when the on-disk shape changes; readers stay
// best-effort compatible across versions (see package doc).
const SchemaVersion = 1

// Role identifies the source of an Entry's blocks.
type Role string

const (
	RoleAssistant Role = "assistant" // model turn: text, thinking, tool_use
	RoleUser      Role = "user"      // tool results fed back to the model
	RoleSystem    Role = "system"    // non-agent output (e.g. command steps)
	RoleResult    Role = "result"    // terminal success/error summary
)

// BlockType is the discriminator for a Block. Readers must tolerate values not
// listed here.
type BlockType string

const (
	BlockText       BlockType = "text"
	BlockThinking   BlockType = "thinking"
	BlockToolUse    BlockType = "tool_use"
	BlockToolResult BlockType = "tool_result"
)

// Block is one unit of content within an Entry. The meaningful fields depend on
// Type; unused fields are omitted from the JSON so each block stays minimal:
//
//	text        – Text
//	thinking    – Text (may be empty or redacted)
//	tool_use    – ToolUseID, Name, Input (raw JSON of the tool arguments)
//	tool_result – ToolUseID, Content, IsError, Truncated
type Block struct {
	Type BlockType `json:"type"`

	// Text carries text and thinking block content.
	Text string `json:"text,omitempty"`

	// ToolUseID correlates a tool_use with its tool_result.
	ToolUseID string `json:"tool_use_id,omitempty"`

	// Name is the tool name on a tool_use block.
	Name string `json:"name,omitempty"`

	// Input is the raw JSON of a tool_use block's arguments.
	Input json.RawMessage `json:"input,omitempty"`

	// Content is the tool_result payload, always a string (structured content
	// is JSON-encoded by the writer before storage).
	Content string `json:"content,omitempty"`

	// IsError marks a tool_result that reported failure.
	IsError bool `json:"is_error,omitempty"`

	// Truncated marks a block whose text/content exceeded MaxBlockBytes and was
	// clipped at write time (distinct from the render-time 80-char collapse).
	Truncated bool `json:"truncated,omitempty"`
}

// Entry is one line of the transcript: a single message (one model turn, one
// batch of tool results, or a terminal summary) with its ordered blocks.
type Entry struct {
	V         int     `json:"v"`       // schema version (SchemaVersion at write time)
	Seq       int     `json:"seq"`     // monotonic per file, 1-based
	Ts        string  `json:"ts"`      // RFC3339 UTC, second precision
	Iteration int     `json:"iter"`    // loop iteration (step.State.Iteration)
	Attempt   int     `json:"attempt"` // retry attempt (step.State.Attempt)
	Role      Role    `json:"role"`    // assistant | user | system | result
	Blocks    []Block `json:"blocks"`
}
