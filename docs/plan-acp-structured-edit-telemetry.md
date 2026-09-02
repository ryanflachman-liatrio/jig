# Plan: ACP structured edit telemetry

**Status:** Proposed  
**Research basis:** [ACP structured edit telemetry](research/acp-structured-edit-telemetry.md)  
**Estimated implementation effort:** 1–2 engineering days, excluding any upstream ACP adapter work

## Problem

The run at `.jig/runs/20260901-195720-jq6cs1ap` contains 37 Codex ACP tool uses named `Editing files`. Every persisted tool input is empty and every result is only `completed`. The monitor is correctly rendering the persisted input and output, but there is no edit detail for it to display.

This is an information-loss problem at the ACP boundary, not a monitor-only problem. `harness/acp/client.go` currently retains only a title, status, and serialized `rawInput`; it drops standard ACP `kind`, `locations`, `content`, and `rawOutput` fields. `internal/harness/acp.go` then reduces the event further to title, input, and status before `internal/runner` writes the transcript.

## Goal

When an ACP adapter supplies standard ACP tool-call details, jig must preserve them through the harness and transcript, then render useful edit details in the run monitor. In particular, an adapter-provided ACP `diff` content item must display the affected file and the actual old/new code.

The solution must work with every ACP adapter that implements the stable ACP v1 tool-call model. It must not depend on interpreting tool titles such as `Editing files`, Codex-only metadata, or a particular adapter's undocumented JSON shape.

## Non-goals

- Forcing adapters to emit diffs. ACP makes tool details optional; a compliant adapter may expose only an ID, title, and status.
- Reconstructing per-tool patches from the worktree or Git as the primary mechanism. That would be inaccurate for concurrent tools and is not portable across adapters.
- Making client-executed ACP filesystem requests (`fs/write_text_file`) the edit transport. Adapters may edit their own workspace directly.
- Building an incremental patch-playback UI. The monitor will show the complete structured snapshot available when a tool reaches a terminal state.
- Adding Codex-specific `agentFileChangeReport` metadata to the portable transcript schema. It is an optional path-only, per-turn feature and cannot provide patch content.

## Design decisions

### Preserve the ACP standard model rather than infer edits

Use the standard tool-call fields that ACP already defines: `kind`, `locations`, `rawInput`, `rawOutput`, and `content`. In particular, preserve `content.type == "diff"` as a typed file patch with `path`, nullable `oldText`, and `newText`.

The code must retain unfamiliar ACP content variants as bounded raw JSON rather than discard them. This keeps the boundary forward-compatible while giving the monitor a first-class renderer for text and diff content.

### Introduce a small backend-neutral tool-activity model

Add a pure `internal/toolcall` package as the shared data contract for harnesses, transcript persistence, runner redaction, and monitor rendering. It avoids making `internal/harness` depend on a persistence package and prevents each boundary from inventing a slightly different representation.

The model should contain the following fields:

```go
type Activity struct {
	ID        string
	Title     string
	Kind      string
	Status    string
	Input     json.RawMessage
	Output    json.RawMessage
	Locations []Location
	Content   []Content
}

type Location struct {
	Path  string
	Line  *int
	Column *int
}

type Content struct {
	Type string
	Text string
	Diff *Diff
	Raw  json.RawMessage
}

type Diff struct {
	Path    string
	OldText *string
	NewText string
}
```

The exact field names may follow existing Go conventions, but `OldText` must remain distinguishable between an absent old file (new file) and an empty old file. The type belongs at the jig boundary; ACP-specific SDK types must not leak into runner, transcript, or TUI packages.

### Respect ACP replacement semantics

ACP `tool_call_update` fields such as `content` and `locations` replace the previous value when present; they are not append-only deltas. Maintain one pending `toolcall.Activity` per ACP tool-call ID and replace each supplied field. A terminal event emits the final merged activity to the harness and transcript.

Emit the initial tool use as soon as the tool call is observed so the monitor has a live row. Coalesce non-terminal updates in the pending activity, then attach the full final activity to the existing tool result at terminal status. This retains one logical transcript item per tool without creating a transcript event for every progress update.

### Make information availability explicit in the UI

For edit tools, the expanded monitor view should prefer typed diffs, then locations, then formatted input/output/text content. If no structured edit detail was supplied, render a short explicit message such as `Adapter did not provide edit details.` A successful tool status alone must not be presented as a patch.

### Bound and redact before persistence

Patch text, raw input, and raw output can include secrets and can be much larger than normal transcript content. Apply the same redaction policy used for text transcript blocks to every structured string field before writing. Apply per-value and per-tool aggregate bounds before JSONL persistence and before rendering so a large patch cannot make a run file or TUI unusable.

## Data flow

```text
ACP ToolCall / ToolCallUpdate
          |
          v
harness/acp client: capture all standard fields
          |
          v
internal/harness/acp: merge updates by tool-call ID
          |
          v
backend-neutral toolcall.Activity on Harness events
          |
          v
runner: redact + bound, then transcript JSONL
          |
          v
monitor: typed diff -> locations -> raw details -> explicit fallback
```

## Implementation tasks

### 1. Add the canonical tool-activity contract

**Area:** `internal/toolcall`  
**Estimate:** 25 minutes  
**Risk:** Low

Create `internal/toolcall/toolcall.go` and focused tests in `internal/toolcall/toolcall_test.go`.

- Define the backend-neutral activity, location, content, and diff types described above.
- Use JSON-friendly fields and `json.RawMessage` for opaque input, output, and unknown content; avoid ACP SDK imports.
- Define helpers only where they remove ambiguity, such as a safe deep copy or an `IsEdit` predicate based on standard `kind` and typed diff content.
- Test JSON round-tripping, nullable `OldText`, an empty old string, multiple locations, multiple diffs, and unknown raw content.

**Done when:** All downstream packages can share the same type without importing `harness/acp` or ACP SDK types.

### 2. Extend the durable transcript schema and safety controls

**Area:** `internal/transcript`  
**Estimate:** 45 minutes  
**Risk:** Medium

Update `internal/transcript/transcript.go` and its tests so `BlockToolUse` and `BlockToolResult` carry `*toolcall.Activity` rather than separate lossy `Name`, `Input`, and string `Content` fields.

- Replace the legacy tool-specific block fields in the same change; this is an internal pre-v1 format and should not retain a parallel schema.
- Preserve transcript compatibility within a run by using the existing reader's permissive JSON decoding for older records that lack the new field. Do not add migration shims or rewrite old run directories.
- Extend `clampBlock` to bound activity title, kind, status, JSON input/output, locations, text content, raw unknown content, and both sides of every diff. Enforce an aggregate per-tool cap in addition to individual field caps.
- Ensure block cloning and reading do not alias mutable raw JSON or slices.
- Add tests for bounded oversized diff payloads, new-file diffs, unknown content, and a legacy record that still renders as a tool with no structured details.

**Done when:** A transcript JSONL record can safely and losslessly represent the normalized tool activity supplied by any harness.

### 3. Capture the full standard ACP tool-call payload

**Area:** `harness/acp` nested module  
**Estimate:** 40 minutes  
**Risk:** Medium

Update `harness/acp/client.go` and `harness/acp/client_test.go`.

- Extend the local event type to retain ACP `ToolCall.Kind`, `Locations`, `Content`, `RawInput`, and `RawOutput`, along with title, ID, and status.
- Convert ACP SDK content variants into either the typed local representation needed by the parent harness or a preserved raw value. Map `ToolCallContentDiff` directly, including nullable old text.
- Keep serialization of opaque values deterministic and safe for later persistence; do not stringify structured values prematurely.
- Respect update optionality: an omitted update field means no change, while a present empty collection replaces the previous collection.
- Add table-driven SDK fixture tests for a start event, a partial update, a replacement update, a typed diff, and an unknown content shape.

**Done when:** No standard ACP tool-call detail is dropped by the nested ACP client before `internal/harness/acp.go` can normalize it.

### 4. Refactor the public harness event seam to carry a tool activity

**Area:** `internal/harness`  
**Estimate:** 35 minutes  
**Risk:** High

Update `internal/harness/harness.go`.

- Replace top-level tool-only event fields (`ToolUseID`, `Name`, `Input`, and result `Content`) with `Tool *toolcall.Activity` on tool-use and tool-result events.
- Keep non-tool event fields narrowly scoped to text, lifecycle, and errors.
- Document the ownership rule: event producers must not mutate an activity after sending it.
- Update all compile-time callers in the same change. Do not maintain a lossy legacy field set alongside the activity model.

**Done when:** The harness interface can represent a structured ACP edit without ACP imports or vendor-specific fields.

### 5. Normalize both ACP and Claude events into the shared seam

**Area:** `internal/harness`  
**Estimate:** 70 minutes  
**Risk:** High

Update `internal/harness/acp.go`, `internal/harness/acp_test.go`, and `internal/harness/claude.go` (plus a focused Claude mapping test if existing coverage is insufficient).

- In `AcpHarness`, keep a pending activity for each tool ID and apply the ACP update replacement rules from this plan.
- Emit the initial `EventToolUse` with a copy of the current activity; emit `EventToolResult` with the completed/failed status and the final merged activity. Continue to preserve existing lifecycle ordering required by the runner.
- Eliminate the title-only `Read file` special case as a source of truth. It may remain only as a display fallback if the standard fields are unavailable, never as a fabricated patch.
- Map Claude tool uses and results into the same activity model: title and input for the use; completed/failed status and text output for the result. Claude need not fabricate `kind`, locations, or diffs.
- Test ACP updates that add a diff after the initial call, replace an earlier diff list, fail, and complete with no edit detail. Test that a Claude event still produces the expected non-ACP transcript data.

**Done when:** Every harness emits the same data shape, and only adapters that actually supply edit details produce structured edit content.

### 6. Persist normalized activities through the agent runner

**Area:** `internal/runner`  
**Estimate:** 55 minutes  
**Risk:** High

Update `internal/runner/agent.go` and `internal/runner/agent_test.go`.

- Map harness tool-use and tool-result events to transcript blocks using copies of `toolcall.Activity`.
- Update pairing/guard logic to use the activity ID and preserve existing behavior for malformed or missing IDs.
- Extend `redactTranscriptBlocks` to redact title, input, output, locations where applicable, text content, raw content, diff paths, old text, and new text before the transcript writer receives them.
- Add regression tests that inject a diff and raw output containing redactable values, verify persisted content is redacted, and verify a normal structured edit produces paired tool-use and tool-result blocks.

**Done when:** A structured ACP diff survives from harness event to transcript while sensitive values do not.

### 7. Render structured edit details in the monitor

**Area:** `internal/tui/monitor`  
**Estimate:** 80 minutes  
**Risk:** Medium

Update the monitor's transcript item, detail, summary, and search paths. Expected files include `monitor_transcript_items.go`, `monitor_transcript_detail.go`, `monitor_transcript_items_view.go`, `monitor_tool_summary.go`, and their existing focused test files.

- Make tool-item construction retain the tool activity from both the tool-use and tool-result blocks; prefer the terminal activity when it has richer data.
- Prefer typed diffs in the expanded body. Render each diff with its file path and a readable old/new or unified-diff-style code presentation using existing TUI theme styles and the existing detail bounds.
- Treat `OldText == nil` as a new-file change, not an empty deletion. Support multiple diffs from one tool call.
- Then render locations, formatted JSON input/output, and text/unknown content as secondary diagnostics.
- For a successful edit tool with no structured details, render `Adapter did not provide edit details.` rather than an empty Input/Output section.
- Update tool summaries to prefer `kind == edit` and diff locations over title heuristics. Preserve useful summaries for adapters that only provide titles.
- Include path, kind, diff text, and locations in transcript search without making the hidden raw payload the default visible summary.
- Add tests for one diff, a new-file diff, several diffs, no-details fallback, large/truncated content, search matching a diff path/text, and the current title-only Codex record.

**Done when:** An adapter-supplied ACP diff is visible as code in the expanded monitor item, while the September 1 run visibly explains why it has no patch content.

### 8. Validate the current Codex ACP adapter and upgrade only with proof

**Area:** `harness/acp` nested module  
**Estimate:** 45 minutes plus an authenticated manual run  
**Risk:** Medium

Update `harness/acp/conn.go` and `harness/acp/codex_integration_test.go`.

- Add an opt-in authenticated integration test that asks Codex ACP to make one deterministic edit in a temporary workspace. Capture session updates and assert that the edited file changes and that a terminal tool activity has `kind == edit` plus at least one standard diff or other structured edit detail.
- Gate the test behind an explicit environment variable and keep it out of normal `go test ./...`; it consumes a real Codex session and must not run in CI without deliberate credentials and approval.
- Evaluate the currently pinned `@agentclientprotocol/codex-acp@1.6.2` against that test. If it fails to provide structured details, evaluate `1.8.0`, the version current at research time, and pin the newest version that passes.
- Do not claim that a package bump fixes the issue unless the integration artifact proves it. If no released adapter emits the standard data, retain the capture work and open an upstream adapter issue with a minimized trace.

**Done when:** jig has an opt-in regression test that distinguishes “adapter made an edit” from “adapter reported structured edit telemetry,” and the pinned version is evidence-based.

### 9. Align operator-facing documentation with the verified adapter behavior

**Area:** repository documentation  
**Estimate:** 20 minutes  
**Risk:** Low

Update the version references and capabilities in `AGENTS.md`, `docs/workflow-schema.md`, and any current Codex ACP adapter documentation only after Task 8 establishes the final pin.

- State that monitor patches are available when an ACP adapter emits standard tool-call detail; do not promise this for every adapter or tool.
- Link the troubleshooting guidance to the explicit monitor fallback message and to the opt-in integration check.
- Keep the research note as the primary protocol rationale rather than duplicating a vendor-specific implementation narrative in workflow docs.

**Done when:** Documentation neither overpromises cross-adapter patches nor points operators at a stale adapter version.

### 10. Run focused and full verification

**Area:** verification  
**Estimate:** 30 minutes  
**Risk:** Low

Run these checks after implementation:

```bash
go test ./internal/toolcall ./internal/transcript ./internal/harness ./internal/runner ./internal/tui/monitor
go test ./...
go vet ./...
gofmt -l .
```

Run the opt-in Codex ACP integration test separately only with an authenticated operator session and its explicit environment gate enabled. Record the adapter version, test result, and a sanitized transcript fixture or assertion output in the implementation PR.

**Done when:** Focused tests cover the data path, the complete repository test suite and vet pass, formatting reports no changed Go files, and adapter-specific behavior has a reproducible evidence record.

## Acceptance criteria

- ACP standard `diff` content reaches transcript JSONL as typed path/old/new data without title parsing.
- ACP update replacement semantics do not leave stale locations or stale diffs in the final transcript record.
- The monitor shows the actual edited code for a structured diff, including new-file edits and multiple-file tool calls.
- When no structured detail is supplied, the monitor explicitly says so instead of displaying blank input/output sections.
- Existing Claude tool calls remain observable and do not fabricate ACP fields.
- Structured input, output, and patch strings receive transcript redaction and size limits before persistence.
- The Codex adapter pin is retained or changed only after the opt-in integration test demonstrates its observed structured-edit behavior.
- The existing September 1 run remains readable and clearly reports the absence of adapter-provided edit details; no historical run migration is required.

## Risks and mitigations

| Risk | Mitigation |
| --- | --- |
| An adapter reports edits but no ACP `content` or `locations`. | Render the explicit fallback; do not infer a patch from title, status, or filesystem state. |
| ACP sends several partial updates. | Store a per-ID pending activity and apply documented replacement semantics in one tested location. |
| Large or sensitive patch text is persisted. | Redact and bound all structured fields in runner and transcript writer tests before disk I/O. |
| A Codex package upgrade changes unrelated behavior. | Gate it on an authenticated integration test and run the full harness suite before updating documentation. |
| Internal seam refactor regresses non-ACP harnesses. | Map Claude to the same activity type and retain focused runner/harness regression coverage. |

## Follow-up if adapters still omit details

If the validated current Codex adapter still produces only titles and status, ship Tasks 1–7 so jig accurately preserves data from adapters that do provide it, retain the explicit fallback for Codex, and file an upstream issue requesting standard ACP `kind`, `locations`, and `content` emission for edit operations. A later, clearly labeled best-effort per-turn workspace diff may be considered separately, but it must not be represented as adapter-provided per-tool telemetry.
