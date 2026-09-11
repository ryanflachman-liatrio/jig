package shared

// ToolDisplayState is the semantic execution state of a tool exchange in the
// Monitor transcript. It lives in `shared` so presentation helpers
// (RenderStatusLine, ToolStatusIcon, RenderCard state mapping in callers)
// can be authored without importing `internal/tui/monitor`.
//
// Monitor keeps `toolDisplayState` as a type alias of this type so the
// existing normalizer in `monitor_transcript_items.go` continues to own how
// entries settle from Running/UnknownUse/UnknownResult into Success or Error.
// This type is a presentation contract, not an authoritative status: engine
// and runner packages have their own status enums and must not consume it.
type ToolDisplayState int

const (
	ToolDisplaySuccess ToolDisplayState = iota
	ToolDisplayError
	ToolDisplayRunning
	ToolDisplayUnknownUse
	ToolDisplayUnknownResult
)
