# ACP structured edit telemetry

**Question:** How can jig render the code edited by any ACP agent without
depending on a Codex-only event or extension?

## Verdict

Use ACP v1's standard tool-call payload, especially `ToolCallContent` of type
`diff`. It contains the absolute path, old text, and new text needed for a
portable edit view. Preserve it verbatim from `session/update` through the
harness and transcript; render it in the monitor as a diff.

This is a standard *representation*, not a guarantee that every adapter will
emit it. ACP makes tool-call detail optional. A compliant adapter may send only
an ID and status, so jig must distinguish “adapter supplied no patch data” from
an empty edit and retain useful fallback detail such as locations or raw input.

## Verified protocol facts

- ACP v1 tool calls can include `kind`, `locations`, `rawInput`, `rawOutput`,
  and `content`; updates may replace those values. [`Tool Calls: Creating and
  Updating`](https://agentclientprotocol.com/protocol/v1/tool-calls)
- `kind: "edit"` is the standard category for file/content modification.
  `rawInput` and `rawOutput` are opaque tool arguments/results, so they are
  useful fallback evidence but should not be parsed as a cross-adapter edit
  schema. [`ACP tool-call fields`](https://agentclientprotocol.com/protocol/v1/tool-calls)
- Standard `content` can include a `diff` with an absolute `path`, nullable
  `oldText`, and required `newText`. This is the cross-adapter contract that
  supports an actual code-change view. [`ACP diff content`](https://agentclientprotocol.com/protocol/v1/tool-calls)
- A `tool_call_update` replaces its `content` and `locations` collection; it
  does not append to them. A consumer needs per-tool-call state keyed by
  `toolCallId` before persisting or rendering a final snapshot. [`ACP v1 JSON
  schema`](https://github.com/agentclientprotocol/agent-client-protocol/blob/main/schema/v1/schema.json)
- jig's pinned `github.com/coder/acp-go-sdk v0.13.5` already models all these
  fields, including `ToolCallContentDiff`; no SDK upgrade is needed merely to
  receive them. [`acp-go-sdk v0.13.5 generated
  types`](https://github.com/coder/acp-go-sdk/blob/v0.13.5/types_gen.go)

## What jig currently loses

`harness/acp.Client.Event` records only title, status, and marshalled raw
input. Its `SessionUpdate` handler therefore drops the standard `kind`,
`locations`, `content`, and `rawOutput` fields before `internal/harness` can
see them. [`harness/acp/client.go`](../../harness/acp/client.go)

The latest run confirms the resulting gap: all 37 Codex `"Editing files"`
activities in `implement_task__implement_code` have no transcript input and a
result of only `"completed"`. That run cannot be reconstructed after the fact.

The monitor is not the source of the loss. It already renders the input and
result it receives. [`monitor_transcript_items_view.go`](../../internal/tui/monitor/monitor_transcript_items_view.go)

## Recommended portable design

1. Expand the ACP-side event model to retain the full standard tool snapshot:
   `id`, `title`, `kind`, `status`, `locations`, `rawInput`, `rawOutput`, and
   typed `content` variants. Keep raw JSON for unknown future variants.
2. Maintain a state machine keyed by ACP `toolCallId`. Apply a start update,
   then each update using ACP replacement semantics for `content` and
   `locations`. Do not infer a patch from a title.
3. Introduce a backend-neutral structured tool-activity event and transcript
   representation. It should carry `ToolDiff{Path, OldText?, NewText}` plus
   generic text/resource/terminal content. Do not overload the existing
   `tool_result.Content string` with a vendor-specific JSON convention.
4. Emit the initial tool activity when ACP emits `tool_call`, then persist
   updates as they arrive. This permits a live “editing `file.go`” row and a
   completed diff without waiting until the tool ends. The monitor correlates
   records by tool-use ID.
5. Render standard diffs first; fall back in order to locations, raw input,
   then the title/status. If none is present, say `adapter did not provide edit
   details` rather than rendering an empty expandable card.
6. Bound and redact persisted patch content like other transcript data. A full
   source snapshot can be large or contain secrets; retain a byte/line cap and
   clearly mark truncation.

This design treats ACP as the source of truth and applies equally to Codex,
Claude, Cursor, Gemini, and future adapters that emit ACP v1 diff content.

## Codex-specific observations

jig currently pins `@agentclientprotocol/codex-acp@1.6.2`. The current package
release is 1.8.0, so a live compatibility probe is required before deciding
whether an upgrade changes emitted edit data. [`1.8.0 package
metadata`](https://raw.githubusercontent.com/agentclientprotocol/codex-acp/main/package.json)

Current Codex ACP source demonstrates the desired standard mapping: patch
events are published with `kind: edit`, locations, raw input/output, and ACP
content derived from file changes. [`Codex ACP patch
mapping`](https://raw.githubusercontent.com/zed-industries/codex-acp/main/src/thread.rs)
This is encouraging, but jig must first preserve the fields before an adapter
upgrade can help the monitor.

Codex ACP also offers `agentFileChangeReport`, but it is a vendor-specific
extension that returns only a per-turn list of paths—not file content or diffs
—and runs a hidden read-only fork. It is useful only as an optional
Codex-specific supplemental file list, not as the core transcript mechanism.
[`agentFileChangeReport`](https://raw.githubusercontent.com/agentclientprotocol/codex-acp/main/docs/agent-file-change-report.md)

## Non-recommendations

- Do not parse strings such as `"Editing files"` to guess paths or patches.
- Do not make a git/worktree snapshot diff the primary implementation. It
  cannot reliably attribute changes to one ACP tool call, misses non-git
  workspaces, and gives different semantics from adapters that supply live
  structured diffs. It can be an explicitly-labelled per-turn fallback.
- Do not rely on ACP `fs/write_text_file` for this feature. That is an optional
  client-executed filesystem capability, whereas many agents—including Codex
  in the observed run—edit their workspace directly.
