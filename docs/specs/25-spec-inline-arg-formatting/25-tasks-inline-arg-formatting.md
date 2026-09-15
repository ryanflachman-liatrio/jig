# 25-tasks-inline-arg-formatting.md

## Standards Evidence Table

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `AGENTS.md` | yes | Pre-v1: replace mechanisms cleanly, no compat shims; Monitor stays backend-agnostic via `internal/tui/shared`; `go build ./cmd/jig`, `go test ./...`, `go vet ./...`, `gofmt -w` on changed files are the required commands | none |
| `docs/TESTING.md` | yes | Table-driven subtests with behavioral failure names; assert state/output directly, don't recreate the algorithm in assertions; `go test -race ./internal/tui/...` for TUI changes; `gofmt -l`/`git diff --check` for whitespace | none |
| `docs/CONVENTIONS.md` | yes | Keep APIs small, name files after their concern, place helpers near owning behavior, avoid stringly-typed maps for known contracts, validate external data at the boundary that owns the rule | none |
| `docs/TUI.md` | yes | Include width/content/version/expansion in render-cache invalidation; render caches must stay local to their owner; preserve raw evidence separately from ANSI decoration | none |
| `README.md` | yes | Project overview and build/run commands (`go build ./cmd/jig`, `go run ./cmd/jig validate ...`); no package-specific guidance affecting this feature | none |

No conflicts detected between sources. `AGENTS.md`, `docs/TESTING.md`, `docs/CONVENTIONS.md`, and root `README.md` were all read (exceeds the 2-source minimum, and both files the audit gate treats as mandatory-if-present); `docs/TUI.md` was read for the package-specific rendering rules referenced by the spec.

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/tui/monitor/monitor_tool_summary.go` | Owns `summarizeActivity`/`summarizeToolCall`, the per-kind switch, `primaryToolArg`, `decodeToolArgs`, and `sanitizeToolSummary`. The new fair-share formatter and its width/secret-redaction helpers are added here, alongside the existing small helpers (`shortFile`, `shortHost`) it already follows the pattern of. |
| `internal/tui/monitor/monitor_tool_summary_test.go` | Existing tests for `summarizeToolCall`/`summarizeActivity`; gains the new formatter's table-driven unit tests and updated call sites once the functions take a width parameter. |
| `internal/tui/monitor/monitor_transcript_items_view.go` | Calls `summarizeActivity` (`:193`) and `composeToolHeader` (`:194`, `:280`); both need to pass/consume the real panel width (`m.transcriptInnerW`) and, for `grep`, populate `StatusLine.Meta` from the formatter's secondary-argument output. |
| `internal/tui/monitor/monitor_transcript_items_view_test.go` | Existing `TestToolExchangeHeader*` suite exercising `composeToolHeader`; gains a case asserting `grep`'s Meta slot carries `path`/`case` while `Description` keeps the curated `pattern` detail unchanged. |
| `internal/tui/shared/status_line.go` | Already documents `StatusLine.Description`/`.Meta` as the slice-15 consumer; no functional change expected, read-only reference during implementation to confirm slot semantics (flattening, styling) are respected. |

### Notes

- Place new tests alongside the files they test, matching every existing
  `*_test.go` pairing in `internal/tui/monitor`.
- Run tests with `go test ./internal/tui/monitor -run <Name> -v` for a single
  target, and `go test -race ./internal/tui/... -count=1` for the full
  package pass required by `docs/TESTING.md`'s TUI-behavior row.
- Format only files actually changed: `gofmt -l internal/tui/monitor` then
  `gofmt -w <files>` if anything is listed; do not reformat untouched files.
- No new package or file is being created; both the formatter and its tests
  live in the existing `monitor_tool_summary.go` / `monitor_tool_summary_test.go`
  pair per `docs/CONVENTIONS.md`'s "name files after the concern they own."

## Tasks

### [x] 1.0 Fair-share inline argument formatter (core algorithm)

#### 1.0 Proof Artifact(s)

- Test: table-driven `TestFormatArgsInline` (or equivalent) in
  `internal/tui/monitor` covering — one long value plus three short keys (all
  four keys present); budget exhaustion (ellipsis appended); array/object
  arguments rendered as `[N items]`/`{N keys}`; string values with embedded
  `\n`/`\t` escaped and output remaining one line; empty/noise-only arguments
  producing an empty string; a secret-shaped key (`token`, `api_key`, ...)
  rendered as `key=<redacted>`; output width never exceeding the supplied
  budget across varied inputs; identical output across repeated calls on the
  same unordered input map (determinism) demonstrates FR-15.1 through
  FR-15.6 and FR-15.8 from the spec.
- CLI: `go test ./internal/tui/monitor -run TestFormatArgsInline -v` passing
  output demonstrates the algorithm is correct in isolation.

#### 1.0 Tasks

- [x] 1.1 Add `formatArgsInline(args map[string]json.RawMessage, maxWidth int) string` to `monitor_tool_summary.go`, next to `primaryToolArg`. Compute a deterministic key order up front via `orderArgKeys`: the fixed priority list (`path`, `file_path`, `command`, `pattern`, `query`, `url`) for any keys present, in that order, followed by the remaining keys sorted lexicographically (`sort.Strings`). *Deviation from plan:* noise-key exclusion is folded into `orderArgKeys` via `isHiddenArgKey` (prefix check) rather than a separate `hidden` parameter — simpler call sites, same effect, since every caller in this codebase wants the same exclusion rule.
- [x] 1.2 Implement the fair-share width loop: for each key in order, reserve the minimal footprint (`", " + key + "=" + 4` placeholder chars) of every key still pending before spending width on the current key, mirroring the reserved-tail calculation in the omp reference (`docs/epics/omp-transcript-parity/slices/15-inline-arg-formatting.md:59-66`). When the budget is exhausted before a key fits, the accumulated pieces plus an ellipsis are passed through `shared.TruncateTitle` as a final width-safety net (covers the audit's extreme-narrow-width flag).
- [x] 1.3 Add `formatScalarArg(raw json.RawMessage, valueMaxLen int) string` that inspects the raw JSON's leading byte (no generic `interface{}` unmarshal) and renders: `null` for JSON null, the literal text for booleans/numbers, a quoted string with `\n`/`\t` escaped and width-truncated via `shared.TruncateTitle`, `[N items]` for arrays via a shallow `[]json.RawMessage` decode, `{N keys}` for objects via a shallow `map[string]json.RawMessage` decode — avoiding a full decode of large nested content. Uses `lipgloss.Width` for all width math.
- [x] 1.4 Add `isSecretArgKey(key string) bool` matching case-insensitively via substring on `token`, `key`, `password`, `secret`. When true, `formatArgsInline` renders `key=<redacted>` for that pair without spending width budget on a value length.
- [x] 1.5 Noise-key exclusion: no existing `__partialJson`-equivalent list was found elsewhere in jig's tool pipeline (grepped `internal/tui/monitor`, `internal/toolcall`), so `isHiddenArgKey` introduces a `__`-prefix rule (matching the omp convention this slice is modeled on) rather than a hardcoded key list.
- [x] 1.6 Wrote `TestFormatArgsInline` in `monitor_tool_summary_test.go` as a table-driven test (subtests) covering every case listed in the 1.0 Proof Artifact, including an explicit width-budget assertion across widths `0..200` (covers the audit's extreme-narrow-width flag) and a determinism assertion (20 repeated calls on the same map literal).
- [x] 1.7 Ran `go test ./internal/tui/monitor -run TestFormatArgsInline -v` (all subtests pass) and `gofmt -l internal/tui/monitor` (no output).

### [x] 2.0 Width-aware wiring into the unmapped-tool fallback

#### 2.0 Proof Artifact(s)

- Test: updated `monitor_tool_summary_test.go` cases showing an unmapped/MCP-
  style tool activity now produces a multi-key preview (more than one
  `key=value` pair) where the prior fixture showed only `primaryToolArg`'s
  single value, demonstrates FR-15.1/FR-15.2 wired end-to-end.
- Test: rendering the same unmapped-tool fixture through `summarizeActivity`
  at a narrow width and a wide width produces a shorter and a longer preview
  respectively, each within its supplied width, demonstrates the budget is
  applied before render rather than clipped after (spec Unit 2, third proof
  artifact).
- CLI: `go test ./internal/tui/monitor -run TestSummarize -v` passing output
  demonstrates no regression to any per-kind mapping's existing detail.

#### 2.0 Tasks

- [x] 2.1 Changed `summarizeActivity(activity *toolcall.Activity)` to
  `summarizeActivity(activity *toolcall.Activity, width int)` and
  `summarizeToolCall(blk transcript.Block)` to
  `summarizeToolCall(blk transcript.Block, width int)` in
  `monitor_tool_summary.go`. Updated the doc comment on `toolCallSummary` and
  added one on `summarizeActivity` noting the fallback/Meta paths are now
  width-budgeted.
- [x] 2.2 In the unmapped-tool fallback branch, replaced the
  `primaryToolArg(args)` call with `formatArgsInline(args, width)` and
  deleted `primaryToolArg` (confirmed via repo-wide grep it had no other
  callers).
- [x] 2.3 Updated the call site in `monitor_transcript_items_view.go` from
  `summarizeActivity(activity)` to `summarizeActivity(summaryActivity,
  m.transcriptInnerW)`, budgeting against the full panel content width since
  `RenderStatusLine` never truncates internally and the composed row is
  clipped to its real on-screen space downstream by the card frame — documented
  inline. *Unplanned but required fix surfaced by task 3.5's test:* the
  existing activity-selection logic above this call could swap `activity` to
  a settled result's activity, which normally carries no `Input`, silently
  dropping all arguments for exchanges whose result also carries a `Kind`
  (a pre-existing, previously untested gap — no existing test asserted
  Description text on a settled exchange). Added a scoped `summaryActivity`
  copy that restores `Input` from the original tool-use activity without
  mutating `detailActivity` (still used verbatim for the expanded view).
- [x] 2.4 Grepped the package for both function names; the only non-test
  caller was the one at `monitor_transcript_items_view.go:193`, and the only
  test file constructing summaries was `monitor_tool_summary_test.go`
  (updated in 1.6/1.7's edits).
- [x] 2.5 Added `TestSummarizeActivityUnmappedToolUsesMultiKeyPreview` to
  `monitor_tool_summary_test.go`: an MCP-style fixture with 3 arguments
  asserts `detail` contains all three `key=value` pairs.
- [x] 2.6 Added `TestSummarizeActivityUnmappedToolBudgetsAgainstRealWidth`:
  same fixture rendered at width 15 vs. 200 — both stay within budget and the
  narrow preview is strictly shorter than the wide one.
- [x] 2.7 Ran `go test ./internal/tui/monitor -run TestSummarize -v` and the
  full `go test ./internal/tui/monitor/...`; every pre-existing per-kind test
  passes. One pre-existing, unrelated failure
  (`TestBoundaryBannerFoldsIntoClosingItemLineRange`) reproduces identically
  on `main` before this change (verified via `git stash`) — not attributable
  to this feature. One pre-existing test
  (`TestErrorFilterKeepsAtomicToolContext`) needed a wider test window: with
  the 2.3 Input-restoration fix, its `Read` exchange now correctly shows a
  `broken.go` detail, which at the test's original narrow width left too
  little room for the full "permission denied" error hint — widened the
  test's `WindowSizeMsg` rather than weakening the assertion.

### [x] 3.0 Meta-slot secondary arguments for known kinds (grep)

#### 3.0 Proof Artifact(s)

- Test: a `grep` tool-call fixture with `pattern`, `path`, and `case`
  arguments asserts the existing curated `pattern` detail stays in the
  `Description` slot unchanged, and `path`/`case` appear in the composed
  header's `Meta` slot via the fair-share formatter, demonstrates the spec's
  Unit 2 second proof artifact and the "known kinds stay curated" non-goal
  boundary.
- CLI: `go test ./internal/tui/monitor -run TestComposeToolHeader -v` (or the
  existing header-composition test target) passing demonstrates the Meta
  slot renders alongside the unchanged primary detail.

#### 3.0 Tasks

- [x] 3.1 Added a `meta []string` field to `toolCallSummary`, documented as
  carrying known-kind secondary-argument info from `summarizeActivity` (grep
  case) through to `composeToolHeader`; nil for every other kind so existing
  rows are byte-for-byte unchanged.
- [x] 3.2 In the `grep` case, after the existing curated detail, added
  `grepMetaArgs(args, width)` which runs `formatArgsInline` over just
  `path`, `case`, and `gitignore` (whichever are present) and stores the
  result in `s.meta`. Every other `case` in the switch is untouched.
- [x] 3.3 In `composeToolHeader`, `s.meta` is appended only in the non-error
  branch (after the diff badge), so an error row shows only its hint —
  never a stale argument preview alongside it — per the existing precedence
  comment, which was extended to describe the new third Meta source.
- [x] 3.4 Added `TestSummarizeActivityGrepKeepsCuratedDetailAndPopulatesMeta`
  to `monitor_tool_summary_test.go`: asserts `detail == "TODO"` unchanged
  and `meta` contains `path=`/`case=` entries.
- [x] 3.5 Added `TestToolExchangeHeaderGrepMetaAlongsideCuratedPattern` to
  `monitor_transcript_items_view_test.go`: asserts the rendered header keeps
  the curated `Search: TODO` title/description pair and shows `path=`/`case=`
  in the meta position. This test is what surfaced the 2.3 Input-restoration
  gap (it failed against the real `itemTranscriptBody` render path even
  though the unit-level 3.4 test passed, because only the former exercises
  the activity-selection logic that previously dropped `Input`).
- [x] 3.6 Ran `go test ./internal/tui/monitor -run TestToolExchangeHeader -v`
  and `-run TestSummarize -v`; every pre-existing case in both test files
  passes unchanged.

### [x] 4.0 Full-suite regression and repository verification

#### 4.0 Proof Artifact(s)

- CLI: `gofmt -l internal/tui/monitor` returning no output demonstrates
  formatting compliance on changed files.
- CLI: `go build ./cmd/jig` succeeding demonstrates the binary still builds
  with the changed signatures.
- CLI: `go vet ./...` returning no findings demonstrates no vet regressions
  from the signature changes threaded through `summarizeActivity`'s callers.
- CLI: `go test -race ./internal/tui/... -count=1` passing demonstrates no
  behavioral or concurrency regression across the whole TUI package,
  including every existing per-kind mapping test untouched by this feature.

#### 4.0 Tasks

- [x] 4.1 Ran `gofmt -l internal/tui/monitor`: no output.
- [x] 4.2 Ran `go build ./cmd/jig`: succeeds.
- [x] 4.3 Ran `go vet ./...` from the repository root: no findings.
- [x] 4.4 Ran `go test -race ./internal/tui/... -count=1`: all packages pass
  except `jig/internal/tui/monitor`, which fails only on
  `TestBoundaryBannerFoldsIntoClosingItemLineRange` — reproduced identically
  on `main` via `git stash` before any of this feature's changes, so it is a
  pre-existing failure unrelated to this work, not a regression.
- [x] 4.5 Grepped the repository for `primaryToolArg`: no remaining Go
  references. Remaining hits are prose in `docs/specs/25-spec-inline-arg-formatting/`
  and `docs/epics/omp-transcript-parity/slices/15-inline-arg-formatting.md`,
  which correctly describe it as removed/superseded, not as still existing.
- [x] 4.6 Commands and outcomes recorded above (4.1-4.4) and in
  `25-proofs/25-task-04-proofs.md`.
