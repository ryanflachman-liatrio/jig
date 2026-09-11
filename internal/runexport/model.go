package runexport

import "encoding/json"

// formatVersion and redactionPolicyVersion are the archive's own version
// numbers (spec 23). They are independent of jig's unversioned local
// transcript store and only ever increase when this package's exported shape
// changes.
const (
	formatVersion          = 1
	redactionPolicyVersion = 1
)

// Content mode values recorded in Manifest.ContentMode.
const (
	ModeStructural    = "structural"
	ModeSanitizedText = "sanitized_text"
)

// Manifest is the archive's top-level accounting record (manifest.json). It
// never carries source text, matched secrets, or original identifiers —
// only fixed-category counters and per-member digests.
type Manifest struct {
	FormatVersion          int            `json:"format_version"`
	RedactionPolicyVersion int            `json:"redaction_policy_version"`
	ContentMode            string         `json:"content_mode"`
	Completeness           string         `json:"completeness"` // "complete" | "partial"
	Gaps                   []Gap          `json:"gaps,omitempty"`
	Counters               Counters       `json:"counters"`
	Members                []MemberDigest `json:"members"`
}

// MemberDigest records the exported length/digest of one archive member.
// The manifest itself is excluded, since its own bytes are not yet known
// while it is being built.
type MemberDigest struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Gap is one fixed-category completeness reason. Source names the evidence
// category ("journal", "workflow", "transcript"); Alias optionally narrows it
// to one step. Line/Count carry only numeric position/quantity — never a raw
// value, path, or parser message.
type Gap struct {
	Reason string `json:"reason"`
	Source string `json:"source"`
	Alias  string `json:"alias,omitempty"`
	Line   int    `json:"line,omitempty"`
	Count  int    `json:"count,omitempty"`
}

// Fixed gap reason codes. Every reason reported in a Gap must be one of these.
const (
	GapMissingSource        = "missing_source"
	GapCorruptSource        = "corrupt_source"
	GapUnsupportedSemantics = "unsupported_semantics"
	GapTornRecord           = "torn_record"
	GapOversizedRecord      = "oversized_record"
	GapMalformedRecord      = "malformed_record"
	GapInvalidField         = "invalid_field"
	GapExportTruncated      = "export_truncated"
)

// Counters aggregates privacy/completeness accounting. Every map is keyed by
// a fixed category name; no key ever carries a matched value or identifier.
type Counters struct {
	Replacements map[string]int `json:"replacements,omitempty"`
	Omissions    map[string]int `json:"omissions,omitempty"`
	Malformed    int            `json:"malformed,omitempty"`
	Truncations  int            `json:"truncations,omitempty"`
}

// RunSummary is run.json: alias identities, observed (possibly
// non-authoritative) state/totals, and the ordered step table.
type RunSummary struct {
	RunAlias           string        `json:"run_alias"`
	WorkflowAlias      string        `json:"workflow_alias"`
	State              string        `json:"state"`
	StateAuthoritative bool          `json:"state_authoritative"`
	Backend            string        `json:"backend,omitempty"`
	Transport          string        `json:"transport,omitempty"`
	StartedAtMS        *int64        `json:"started_at_ms"`
	UpdatedAtMS        *int64        `json:"updated_at_ms"`
	FinishedAtMS       *int64        `json:"finished_at_ms"`
	TotalCostUSD       *float64      `json:"total_cost_usd"`
	TotalTokens        *int          `json:"total_tokens"`
	Steps              []StepSummary `json:"steps"`
}

// StepSummary is one run.json step row.
type StepSummary struct {
	Alias       string   `json:"alias"`
	Type        string   `json:"type,omitempty"`
	Backend     string   `json:"backend,omitempty"`
	Transport   string   `json:"transport,omitempty"`
	Status      string   `json:"status"`
	Attempt     int      `json:"attempt"`
	Iteration   int      `json:"iteration"`
	Generation  int      `json:"generation"`
	CostUSD     *float64 `json:"cost_usd"`
	Tokens      *int     `json:"tokens"`
	ParentAlias string   `json:"parent_alias,omitempty"`
	FanOutIndex *int     `json:"fan_out_index,omitempty"`
	FanOutTotal *int     `json:"fan_out_total,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
}

// EventRecord is one events.jsonl line: journal order, a validated known kind
// or the fixed "unknown", and the closed per-kind projection in Data.
type EventRecord struct {
	Seq    int             `json:"seq"`
	TimeMS *int64          `json:"time_ms"`
	Kind   string          `json:"kind"`
	Data   json.RawMessage `json:"data,omitempty"`
}

// TranscriptRecord is one transcript.jsonl line, present only in text mode.
type TranscriptRecord struct {
	StepAlias  string            `json:"step_alias"`
	Seq        int               `json:"seq"`
	TimeMS     *int64            `json:"time_ms"`
	Iteration  int               `json:"iteration"`
	Attempt    int               `json:"attempt"`
	Generation int               `json:"generation,omitempty"`
	Role       string            `json:"role"`
	Blocks     []TranscriptBlock `json:"blocks"`
	Malformed  bool              `json:"malformed,omitempty"`
}

// TranscriptBlock is one projected transcript block. Type is a validated
// known kind or "unknown"; unsupported/omitted content carries Omitted
// instead of text.
type TranscriptBlock struct {
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	ToolAlias  string          `json:"tool_alias,omitempty"`
	ToolTitle  string          `json:"tool_title,omitempty"`
	ToolStatus string          `json:"tool_status,omitempty"`
	ToolInput  string          `json:"tool_input,omitempty"`
	ToolOutput string          `json:"tool_output,omitempty"`
	Diff       *TranscriptDiff `json:"diff,omitempty"`
	Truncated  bool            `json:"truncated,omitempty"`
	Omitted    string          `json:"omitted,omitempty"`
}

// TranscriptDiff mirrors toolcall.Diff after sanitization.
type TranscriptDiff struct {
	Path    string  `json:"path,omitempty"`
	OldText *string `json:"old_text,omitempty"`
	NewText string  `json:"new_text,omitempty"`
}
