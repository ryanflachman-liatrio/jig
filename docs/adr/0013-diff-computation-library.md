# Diff computation uses hexops/gotextdiff behind a Monitor-local seam

Status: accepted.

The OMP transcript-parity epic's slice 07 (diff rendering) requires the
Monitor's Transcript panel to show *what changed* in an edit tool call, not
just the resulting file. Every ACP-backed edit already carries the previous
file text in `toolcall.Content.Diff.OldText`, but nothing in jig computes a
patch: `github.com/bluekeyes/go-gitdiff v0.8.1` — a direct dependency and the
    10|only diff-adjacent package we ship — only *parses* unified patches.

Slice 07 therefore has to pick a computation path. This ADR records that
choice and pins the caps and library ownership so a future contributor cannot
silently widen the supply-chain surface or move the diff logic outside the
Monitor.

## Decision 1 — Compute unified patches with `github.com/hexops/gotextdiff` and feed the existing parser

The Monitor computes a unified diff string with
`hexops/gotextdiff@v1.0.3` and hands the string to
    20|`internal/tui/diffview.Parse`. `hexops/gotextdiff` is a small, Apache-2.0
port of `golang.org/x/tools/internal/{myers,unified}` — the Go team's own
Myers implementation, exposed as a public module because the internal
package is unimportable. It ships three tiny sub-packages
(`myers`, `span`, `unified`), has no non-stdlib runtime dependencies, and
already appears in jig's `go.sum` because
`github.com/alecthomas/chroma/v2`'s test tree imports it. Promoting it to a
direct `require` entry makes the dependency auditable in `go.mod` without
expanding the transitive closure.

    30|The computation pipeline is:

```go
edits := myers.ComputeEdits(span.URIFromPath(path), oldText, newText)
patch := gotextdiff.ToUnified("a/"+path, "b/"+path, oldText, edits)
pres  := diffview.Parse(patch) // returns *diffview.Presentation
```

Because `diffview.Parse` already owns hunk projection, file-span tracking,
metadata-row identification, and `RenderHunkHeader`/`RenderRawLine`, this
path reuses every downstream affordance the review workspace has spent
    40|specs 16 and 17 stabilising. A future consumer that wants to display the
same diff — the review workspace consolidating on the fused gutter, or the
run export writer emitting an offline patch — asks the Monitor for the
patch string (or lifts `computeDiff` to `shared`) rather than reimplementing
the algorithm.

### Rejected alternative — hand-roll a line-level Myers/LCS

A local ~150-line line-level LCS producing an op list is technically
sufficient and adds zero direct dependencies. It was rejected for two
reasons:

    50|- **No leverage from `diffview`.** A hand-rolled op list bypasses
  `diffview.Parse`, which means every downstream helper — hunk headers,
  `RenderRawLine`, file spans, cursor-to-file mapping — either has to be
  reimplemented against the op list or dropped. That is strictly more code
  to own, not less.
- **Word-level intra-line diff still needs a separate second pass.** The
  slice-07 spec requires reverse-video highlighting on 1↔1 replacements.
  Both options need a token-level LCS; the "no dependency" saving on the
  line-level pass does not extend to the word-level pass.

    60|A hand-rolled Myers remains available as a fallback if `hexops/gotextdiff`
is ever archived or its behavior diverges. The seam in
`internal/tui/monitor/monitor_diff_compute.go` isolates the library so a
future substitution touches one file.

### Rejected alternative — ask the harness to supply a patch

The `toolcall.Diff` wire contract currently carries `OldText *string`
and `NewText string`. Extending it with an adapter-produced `Patch string`
would let the Monitor render whatever the harness produces without running
Myers at all. Rejected because:

    70|- **NG5 and CC-12 of the OMP epic forbid wire-format changes for
  presentation.** The transcript wire is frozen for the length of this
  epic and this presentation-only work is not the reason to break that
  freeze.
- **Not every backend can produce a canonical unified patch.** Claude's
  ACP adapter, the Codex ACP adapter, and the Cursor ACP adapter all
  produce `old_text`/`new_text` pairs; a `patch` field would either be
  empty for two of the three backends or would force each backend to
  bundle its own diff library. Computing centrally in the Monitor keeps
  the harness surface small.

    80|## Decision 2 — Bound computation before it runs; oversized inputs fall through to the resulting-source card

`computeDiff` refuses to run on inputs above two package-private caps:

- `diffComputeMaxBytes = 128 * 1024` — combined size of `OldText` and
  `NewText`.
- `diffComputeMaxLines = 4000` — line count of either side.

Over-limit inputs return `computeSkippedOversize` without invoking Myers.
The Monitor renders the existing resulting-source card and prepends a
    90|`shared.DiffUnavailableHint()` row (`"… diff unavailable; showing resulting
source"`) so the operator sees the payload's evidence in some shape.

### Why cap at authorship time

Myers is O(ND) in the input length and the edit distance. A pathological
input — a 1 MiB rewrite of a generated file — would burn a full frame's
budget on one card. The 100 ms throttled repaint contract that the Monitor
operates under does not tolerate that. Capping before computation keeps the
worst case bounded by two `len()` checks.

   100|Transcript payloads are already clamped by `internal/transcript/writer.go`
at 128 KiB per aggregate block and 32 KiB per value; the caps here mirror
the aggregate ceiling so a payload that made it through the writer's clamp
without truncation will always fit. When the writer *did* truncate
(`transcript.Block.Truncated == true`), the diff renderer additionally
prepends a `shared.DiffClampedHint()` row so the reader knows the change
spans may be artifacts of truncation, not real edits.

### Rejected alternative — tune caps at render time from terminal geometry

An earlier draft considered scaling the caps by
   110|`transcriptInnerW * transcriptDetailRows` so a wider viewport allowed a
larger diff. Rejected: the caps are correctness bounds on Myers, not
cosmetic bounds on the rendered rows. The renderer has its own budget
(`diffCollapsedHunks = 8`, `diffCollapsedLines = 40`) and the shared detail
bound already clips at `transcriptDetailRows = 12`. Two independent
constants read more clearly than one adaptive knob and preserve
determinism across widths.

## Decision 3 — The computation seam lives in `internal/tui/monitor`, not `internal/tui/shared`

`computeDiff` and `renderDiffRows` are Monitor-package-private today. The
   120|two functions have signatures free of Monitor state — they take a
`*toolcall.Diff`, a width, a bool, and a `*glamour.TermRenderer` — so a
future lift to `shared` (or to a new `internal/tui/diffcard` package) is a
package-level move, not a rewrite.

### Why not `internal/tui/shared` today

`internal/tui/shared` is the ownership boundary for TUI primitives that
have zero Monitor coupling. As of this ADR the diff producer is called
from exactly one caller (`writeNewCodeCards` in the Monitor). Landing it
in `shared` would encode a future consumer (the review workspace, per
   130|slice 07 Q-07.3) that does not yet exist. The review workspace can adopt
the same producer through a small refactor when it opts in; that is a
better trigger than speculatively moving the code today.

### Rejected alternative — put the producer in `internal/tui/diffview`

`diffview` is a *parser*: it consumes a patch string and projects it onto
row structures. Adding a computation entry point to it would mix the two
concerns (compute vs parse) and force `diffview` to gain a
`glamour.TermRenderer` argument for context-run syntax highlighting.
Keeping the producer next to its only caller preserves both packages'
   140|single-responsibility shapes.

---

## Consequences

- **One new direct dependency, license-audited.** `hexops/gotextdiff` v1.0.3
  is Apache-2.0 and derives from `golang.org/x/tools`. `go.mod`'s require
  block gains one entry; `go.sum` already lists the exact version. No
  transitive package is added.
- **Determinism is a tested property, not a hope.** `computeDiff` on the
   150|  same `(OldText, NewText, path)` returns byte-equal patch strings on
  every call. The Monitor's per-item render cache (`chatItemRendered`)
  keys on the exchange's identity and width; determinism lets it hit on
  repeat frames rather than recompute per repaint.
- **Fallback paths are unified.** File creation (`OldText == nil`), byte
  or line-cap bypass, and parse errors all route through the same
  `renderNewCodeCard` fallback with slice-06-vocabulary hints — the
  operator never sees a raw error and never loses the diff payload's
  evidence.
- **The library seam is one file.** Substituting a different unified-diff
   160|  producer (a hand-rolled Myers, `sergi/go-diff`, or a future stdlib
  addition) requires editing only
  `internal/tui/monitor/monitor_diff_compute.go`. The renderer, the
  cache, the header badge, and the review workspace are unaffected.
- **No wire-format change.** `internal/toolcall`, `internal/transcript`,
  `internal/runner`, and `internal/harness` are untouched. This ADR
  authorises presentation-only work.
