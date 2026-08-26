# Implementation Plan: Native Document Review Workspace

**Status:** Proposed; ready for human review  
**Primary surfaces:** `internal/review`, `internal/engine`, `internal/tui/review`,
`internal/tui/monitor`  
**Breaks:** scalar `review = "@step"|"diff"` and review-step `max_messages`
(pre-v1; replace both without compatibility shims)  
**Risk:** high  
**Estimated focused implementation time:** 24–30 hours

---

## Summary

Replace jig's verdict-only review gate with a native, read-only document review
workspace: workflows declare one or more labeled review targets, jig snapshots
the exact Markdown/diff content for the review round, the operator attaches
typed comments to source-line ranges, and one atomic submission records the
verdict plus all feedback for the agent. The top risk is preserving deterministic
loop, reset, replay, and concurrent-gate behavior while adding a large, stateful
Bubble Tea surface and removing the old per-message rerun path.

## Feature summary

Add a first-class review workspace so a review step explicitly names every
document the human must inspect, presents immutable source with line/range
annotations and a rendered Markdown preview, persists drafts outside the reviewed
files, and returns one structured batch of comments with the verdict, without
turning jig into an editor or changing the reviewed files during a review round.

## Approach

Build the feature from the data contract outward. First introduce a pure
`internal/review` domain package and replace `workflow.Step.Review string` with a
validated list of `ReviewTarget` tables. Next add per-round snapshot and draft
paths in `internal/datastore`, then change the engine to materialize immutable
review documents and accept one `review.Submission` through
`Run.ResolveReview`. The engine writes deterministic JSON and Markdown feedback,
sets the review step's `Result.OutputPath` to that Markdown projection, and lets
existing loop feedback feed the entire review back to the agent. Once that
contract is stable, add an `internal/tui/review` child model for source navigation,
range selection, comments, preview, document acknowledgements, and submission;
embed it in the Monitor's existing non-blocking input queue. Finish by deleting
`max_messages`, `Run.Message`, and all verdict-only review UI, updating docs and
examples, and proving live, replayed, reset, looped, and persistence-off paths.

The key design decision is that a review round is an immutable snapshot plus a
mutable sidecar. Source files are never edited by the review workspace. Suggested
replacement text is feedback for the agent, not a direct file mutation.

---

## Confirmed design decisions

These are implementation constraints, not open questions.

| # | Topic | Decision |
|---|---|---|
| D1 | Workflow shape | `review` becomes an array of inline tables: `{ source, label }`. The old scalar form is deleted. |
| D2 | Target count | A review step must declare at least one target. Target order is author order and drives the TUI and feedback output. |
| D3 | Target sources | `source` accepts `@step`, `@step.field`, a workflow-relative text file, or the special value `diff`. |
| D4 | Labels | `label` is required, non-blank, and unique within the review step. The author, not the TUI, names the review obligation. |
| D5 | Supported content | Markdown is the default document type. `source = "diff"` is rendered as a diff. Other text files use a verbatim source view; binary and oversized files fail review preparation. |
| D6 | Snapshot identity | Every dispatch materializes a per-round immutable snapshot and records its SHA-256 digest and source-line count. |
| D7 | Round identity | Round IDs derive from the review step's `Generation` and `Iteration` (`gNNN-iNNN`), so manual reset and bounded-loop rounds cannot collide. |
| D8 | Review editing | The workspace is read-only. No embedded file editor and no direct write-back to a review target. |
| D9 | Annotation granularity | Initial annotations target one complete source line or an inclusive multiline source range. Columns/text-range selection are deferred. |
| D10 | Anchor durability | An anchor stores document ID/digest, start/end lines, exact quote, and one surrounding prefix/suffix line. Lines are authoritative within the immutable round; quote/context support later reattachment and readable feedback. |
| D11 | Comment kinds | Fixed kinds: `note`, `question`, `concern`, `blocker`, `suggestion`, `praise`. `suggestion` additionally requires non-empty replacement text. |
| D12 | Submission | Verdict, reviewed-document acknowledgements, optional review summary, and every comment are submitted atomically through `Run.ResolveReview`. |
| D13 | Required review | Every document must be explicitly marked reviewed before any verdict can be submitted. Merely opening a document does not mark it reviewed. |
| D14 | Verdict semantics | The engine validates that the verdict is one of the step's declared choices but does not infer that a particular token means approve/revise. Arbitrary enums remain supported. |
| D15 | Agent feedback | `feedback.md` is deterministic, ordered by document then line then comment creation order, and includes the verdict, summary, excerpts, comments, and suggestions. Loop feedback uses this entire document. |
| D16 | Machine artifact | Per-round `submission.json` is the structured source of truth for the final submission. A sanitized copy is also written to the step's conventional `output.json`; the Markdown projection is copied to conventional `output.md`. |
| D17 | Draft persistence | Draft state is atomically rewritten in the round directory after meaningful edits. It never modifies the target and no-ops when persistence is off. |
| D18 | Persistence-off | The engine still supports review tests/runs with `runDir == ""`; request documents carry an in-memory content fallback and all review writers no-op. Persisted runs use snapshot paths and do not put document bodies in the journal. |
| D19 | File is truth | For persisted runs, the TUI reads snapshots and drafts from disk. `ReviewRequest` carries descriptors and liveness/routing metadata, not bulk content. |
| D20 | Concurrent gates | A review remains one entry in the Monitor's persistent input queue. Each queued review owns independent workspace state and drafts; arrivals never steal focus. |
| D21 | Review opening | The compact Gate entry shows progress and opens the workspace. Verdict digits are removed from the compact gate so a review cannot be approved without entering its summary flow. |
| D22 | Source/preview | Source mode is canonical for exact line selection. Preview renders Markdown block-by-block and maps each rendered block back to its source-line range; commenting a preview block anchors the whole source block. |
| D23 | Old messaging | Delete `max_messages`, `Run.Message`, `humanMessageMsg`, `AllowMessage`, review message counters, and `[m] message`. Batched feedback plus the bounded `[step.loop]` is the only revise path. |
| D24 | Restart behavior | Journal replay reconstructs the review request and reloads its draft for inspection. Resuming a dead scheduler remains out of scope; a replayed Monitor without a live `Run` cannot submit, matching existing replay controls. |

---

## Author-facing workflow schema

```toml
[[step]]
id         = "plan_review"
type       = "review"
depends_on = ["plan"]

review = [
  { source = "@plan.summary", label = "Implementation plan" },
  { source = "docs/ARCHITECTURE.md", label = "Architecture constraints" },
  { source = "diff", label = "Code changes" },
]

output_type = { enum = ["approve", "revise"] }

  [step.loop]
  when           = "plan_review == 'revise'"
  goto           = "plan"
  max_iterations = 3
  feedback       = "@plan_review"
```

### Proposed workflow types

```go
type ReviewTarget struct {
    Source string `toml:"source"`
    Label  string `toml:"label"`

    resolvedPath string
}

type Step struct {
    // ...
    Review []ReviewTarget `toml:"review"`
    // MaxMessages is removed.
}
```

`ReviewTarget.ResolvedPath()` exposes the load-time-resolved path to the engine
without leaking the private field to TOML. `source = "diff"` and `@...` targets
leave `resolvedPath` empty.

### Validation rules

For every review step:

1. `review` must be non-empty.
2. Every target needs a non-blank `source` and `label`.
3. Labels must be unique after trimming.
4. Duplicate sources are rejected; the author should not review the same snapshot
   twice under different labels.
5. `@step` and `@step.field` targets must reference known steps also named in
   `depends_on`.
6. `@step.field` must resolve to a text field in the producer's effective schema.
7. A literal file is resolved relative to the workflow file, must exist at load
   time when `baseDir != ""`, and must be a regular file.
8. `diff` remains a reserved source token.
9. Review steps still require bool or enum `output_type`.
10. `max_messages` becomes an unknown TOML key and is not accepted through a
    deprecated field or compatibility branch.
11. Agent/command steps reject a non-empty `review` array as a wrong-step-type
    field.
12. `internal/engine/context.go:consumedFields` examines every review target so
    graph-derived context lists all reviewed producer fields deterministically.

No `[defaults]` value applies to review targets, so `applyDefaults` requires no
new inheritance logic. The load path does need a `resolveReviewTargets(baseDir)`
pass before validation, parallel to output-template and schema-file resolution.

---

## Review domain model

Create `internal/review` as a pure domain/storage package. It must not import
`engine`, `workflow`, Bubble Tea, Glamour, or TUI styles.

```go
type Kind string

const (
    KindNote       Kind = "note"
    KindQuestion   Kind = "question"
    KindConcern    Kind = "concern"
    KindBlocker    Kind = "blocker"
    KindSuggestion Kind = "suggestion"
    KindPraise     Kind = "praise"
)

type Document struct {
    ID           string `json:"id"`
    Label        string `json:"label"`
    Source       string `json:"source"`
    Format       string `json:"format"` // markdown | diff | text
    SnapshotPath string `json:"snapshot_path,omitempty"`
    SHA256       string `json:"sha256"`
    LineCount    int    `json:"line_count"`
    Content      string `json:"-"` // persistence-off fallback only
}

type DocumentRecord struct {
    ID        string `json:"id"`
    Label     string `json:"label"`
    Source    string `json:"source"`
    Format    string `json:"format"`
    SHA256    string `json:"sha256"`
    LineCount int    `json:"line_count"`
}

type Anchor struct {
    DocumentID string `json:"document_id"`
    SHA256     string `json:"sha256"`
    StartLine  int    `json:"start_line"`
    EndLine    int    `json:"end_line"`
    Quote      string `json:"quote"`
    Prefix     string `json:"prefix,omitempty"`
    Suffix     string `json:"suffix,omitempty"`
}

type Comment struct {
    ID          string `json:"id"`
    Kind        Kind   `json:"kind"`
    Anchor      Anchor `json:"anchor"`
    Body        string `json:"body"`
    Replacement string `json:"replacement,omitempty"`
}

type Draft struct {
    SchemaVersion int               `json:"schema_version"`
    StepID        string            `json:"step_id"`
    RoundID       string            `json:"round_id"`
    Documents     []Document        `json:"documents"`
    Reviewed      []string          `json:"reviewed_documents"`
    Comments      []Comment         `json:"comments"`
    Summary       string            `json:"summary,omitempty"`
    View          ViewState         `json:"view"`
}

type Submission struct {
    SchemaVersion int              `json:"schema_version"`
    StepID        string           `json:"step_id"`
    RoundID       string           `json:"round_id"`
    Verdict       string           `json:"verdict"`
    Documents     []DocumentRecord `json:"documents"`
    Reviewed      []string         `json:"reviewed_documents"`
    Comments      []Comment        `json:"comments"`
    Summary       string           `json:"summary,omitempty"`
}
```

`DocumentRecord` deliberately omits `SnapshotPath` and in-memory content so final
feedback is portable and cannot leak an absolute run-directory path. The active
`Session` retains full `Document` values for source loading and anchor validation.

`ViewState` stores only user-restoration state: active document ID, source/preview
mode, source cursor/range, viewport offsets, active comment, and comment-kind
selection. It must not store derived rendered lines or ANSI.

### Domain validation

`review.ValidateSubmission(session, submission, choices)` must reject:

- mismatched step or round IDs;
- unknown, missing, or duplicate reviewed-document IDs;
- a verdict outside the declared choices;
- a comment with an unknown/duplicate ID, blank body, unknown kind, or unknown
  document;
- an anchor digest that differs from its document;
- start/end lines outside `1..LineCount` or an inverted range;
- a quote that does not exactly match the immutable snapshot lines;
- a suggestion without replacement text, or replacement text on a non-suggestion;
- an approve/reject assumption based on the spelling or position of a choice.

The domain renderer sorts comments by workflow document order, then start/end
line, then original comment order. It emits source excerpts using a stable form:

````markdown
# Review: plan_review

- Verdict: `revise`
- Round: `g000-i000`
- Documents reviewed: 3/3

## Implementation plan

### Lines 42–43 · concern · C002

> 42 | Workers execute each task in order.
> 43 | Failures terminate the workflow.

This conflicts with the scheduler's documented parallel execution.

## Suggested replacement

```text
Ready steps execute concurrently up to max_parallel.
```
````

The renderer must never embed terminal ANSI, timestamps, absolute snapshot paths,
or map-iteration order in `feedback.md`.

---

## Durable artifact layout

For a persisted review step `plan_review`:

```text
.jig/runs/<run-id>/steps/plan_review/
  result.json
  output.md                       # latest deterministic feedback projection
  output.json                     # latest structured submission
  review/
    g000-i000/
      draft.json                  # mutable sidecar; atomically rewritten
      submission.json             # immutable final structured submission
      feedback.md                 # immutable final projection for this round
      documents/
        01-implementation-plan.md
        02-architecture-constraints.md
        03-code-changes.diff
    g000-i001/
      ...                         # next bounded-loop round
    g001-i000/
      ...                         # manual-reset generation
```

Path helpers belong in `internal/datastore`:

- `ReviewRoot(runDir, stepID)`
- `ReviewRoundDir(runDir, stepID, roundID)`
- `ReviewDraftPath(runDir, stepID, roundID)`
- `ReviewSubmissionPath(runDir, stepID, roundID)`
- `ReviewFeedbackPath(runDir, stepID, roundID)`
- `ReviewDocumentsDir(runDir, stepID, roundID)`

Every helper returns `""` when `runDir == ""`. No caller may turn an empty path
into `.` or write relative to the process working directory.

Snapshot filenames use author order plus a sanitized label; the engine never
uses raw labels or source paths as directory components. Snapshot contents are
written once with `O_EXCL`. Re-dispatch of the same round loads and verifies the
existing digest instead of silently overwriting what the human may already have
reviewed.

Draft and final JSON writes use temp-file-plus-rename in the same directory.
Finalization writes the round artifacts first, then the conventional `output.*`
copies, then transitions the step to succeeded. Writes are idempotent for an
identical step/round/submission digest so retrying after a partial I/O failure does
not corrupt or duplicate evidence. A conflicting existing final artifact fails
closed. Any write failure leaves the step awaiting review and emits a visible
`RunError`; jig must not accept a verdict whose feedback was not durably recorded.

`datastore.ClearStepOutputs` continues clearing `result.json` and conventional
`output.*` files but deliberately retains `review/<round>/` history. Generation
and iteration produce a new directory, so prior evidence is never mistaken for
the active draft.

---

## Engine protocol and message flow

### Events

Replace the current diff/message-shaped request with descriptors:

```go
type ReviewRequest struct {
    RunID      string
    StepID     string
    RoundID    string
    Choices    []string
    Documents  []review.Document
    DraftPath  string
}

type ReviewSubmitted struct {
    RunID       string
    StepID      string
    RoundID     string
    Verdict     string
    CommentCount int
}
```

`ReviewSubmitted` is a small audit event. Bulk comments stay in `output.json` and
the per-round submission file. Add the event kind/decoder to `journal.go`; replay
does not need a state mutation beyond the subsequent terminal `StepStatus`.

For persistence-off only, `review.Document.Content` is populated in memory and
omitted from JSON. Persisted requests carry snapshot paths and never journal the
document body.

### Scheduler state

Add:

```go
reviewSessions map[string]review.Session // review step ID -> active immutable round
```

Delete:

```go
reviewMessages map[string]int
```

and delete the review-only uses of `resumeSessions`/`stepMessage`. Those maps
remain for block-on, recovery, and stopped-agent resume behavior.

### Dispatch preparation

Move review-specific scheduling into `internal/engine/review.go`:

1. Derive the round ID from the review step state.
2. Resolve targets in author order:
   - `diff`: call the existing dependency-diff collector;
   - `@step.field`: extract the text field using a shared structured-ref helper;
   - `@step`: read the producer's `Result.OutputPath`;
   - literal path: read the target's resolved workflow-relative path.
3. Enforce a 256 KiB per-document limit and 1 MiB per-round aggregate limit,
   reject NUL/binary content, normalize no bytes, and count logical source lines.
4. Compute stable document IDs, formats, and SHA-256 digests.
5. Persist snapshots or populate the persistence-off content fallback.
6. Load an existing same-round draft only when its document IDs and digests match;
   otherwise surface a preparation error rather than silently dropping comments.
7. Store the immutable session in `reviewSessions`.
8. Transition to `StatusAwaitingReview` and emit `ReviewRequest`.

An unreadable or invalid source is a review-step execution failure, not an empty
document. Route it through the existing failure/recovery policy rather than
parking an impossible review gate. Tests must prove the step does not emit a
`ReviewRequest` on preparation failure.

### Atomic submission

Replace:

```go
Run.Resolve(stepID, verdict)
Run.Message(stepID, text)
```

with:

```go
func (r *Run) ResolveReview(stepID string, submission review.Submission)
```

`reviewSubmissionMsg.execute` performs, on the scheduler goroutine:

1. stale/status check (`StatusAwaitingReview` only);
2. active-session lookup;
3. domain validation against the session and workflow choices;
4. durable finalization via `internal/review.Store` when persistence is on;
5. `step.Result{Status: Succeeded, Verdict: submission.Verdict,
   OutputPath: datastore.OutputPath(...)}`;
6. emit `ReviewSubmitted`;
7. transition awaiting-review to succeeded;
8. record the loop intent, if present.

On validation or write failure, keep the gate pending and emit `RunError`; do not
remove the TUI entry. The TUI displays the error and retains its draft.

### Loop feedback

Change `recordLoopIntent` so feedback from a review step prefers the review
step's `OutputPath` content and falls back to its scalar verdict only when no
feedback file exists (persistence-off with no comments/summary). Bool/enum guard
evaluation continues to use `Result.Verdict`, so existing `when` and loop
conditions remain scalar and deterministic.

The runner receives the rendered review document through the existing
`StepRequest.Feedback string` and `buildAgentPrompt` feedback section. No harness
or runner protocol change is required.

### Reset, replay, and cleanup

- Reset removes the active review session from scheduler memory when its step is
  in the reset closure. Historical round directories remain.
- Loop rewind clears the old active session before the review step is dispatched
  into the next iteration.
- `ReviewRequest` remains journaled, allowing a historical Monitor to reconstruct
  the document list and load the saved draft/snapshots.
- `ReviewSubmitted` plus terminal `StepStatus` prevents replay from leaving a
  completed review in the pending queue.
- The engine never attempts to resume a completed review's comment state into a
  different round digest.

---

## TUI architecture

### New child module

Create `internal/tui/review` as a focused Bubble Tea child model. It imports the
pure `internal/review` domain and shared TUI styles, but it does not import the
engine or Monitor.

Suggested files:

| File | Responsibility |
|---|---|
| `model.go` | Model, modes, focus, document state, comment/editor state, draft/submission accessors |
| `keys.go` | Workspace-local bindings and compact/full help groups |
| `document.go` | Snapshot loading, source lines, range/anchor construction, line-to-comment indexes |
| `preview.go` | Goldmark top-level block parsing, source-range mapping, per-block Glamour rendering/cache |
| `update.go` | Navigation, range selection, document acknowledgement, comment lifecycle, summary/submission state |
| `view.go` | Responsive document list, source/preview viewport, comments pane, composer, summary/confirmation |

The package returns `string` from `View`; only `rootModel.View` continues to return
`tea.View` and own alt-screen/background behavior.

### Workspace modes

```go
type mode uint8

const (
    modeBrowse mode = iota
    modeSelectRange
    modeComposeComment
    modeEditComment
    modeSummary
)

type documentMode uint8

const (
    documentSource documentMode = iota
    documentPreview
)
```

Only compose/edit modes capture printable text. `Model.CapturesText()` lets the
Monitor/root keep `?`, quit, and navigation keys from stealing textarea input.

### Layout

Wide terminals:

```text
 Review: plan_review                         2 / 3 reviewed · 4 comments

┌ Documents ─────────────────┐ ┌ Implementation plan · SOURCE · L42 ──────┐
│ ✓ Implementation plan   3  │ │  40  ## Execution                       │
│ ● Architecture          1  │ │  41                                     │
│ ○ Code changes             │ │▌ 42  Workers execute each task in order. │
│                             │ │▌ 43  Failures terminate the workflow.    │
└─────────────────────────────┘ │  44                                     │
                                ├ Comments on lines 42–43 ─────────────────┤
                                │ C002 · concern                           │
                                │ This conflicts with parallel scheduling. │
                                └───────────────────────────────────────────┘
```

Narrow terminals stack the Documents panel above the document viewport and show
the selected comment in a bounded bottom strip. Width/height thresholds must be
derived from panel frame sizes, not hard-coded ANSI widths.

The source viewport uses Bubbles v2 `viewport.LeftGutterFunc` for fixed-width
line numbers, selection markers, comment counts, and reviewed/error state. It
uses `StyleLineFunc` for current/selected/commented lines. Soft-wrapped visual
continuations show a continuation gutter and remain mapped to the original
source line.

### Preview mapping

Promote `github.com/yuin/goldmark` from indirect to direct dependency. Parse the
Markdown once per document and walk top-level block nodes. Goldmark source
segments give byte offsets; convert them to inclusive source-line ranges. Render
each block independently through the existing Charmtone Glamour configuration,
then concatenate the rendered blocks while recording visual-line-to-source-range
metadata.

Preview behavior:

- `j/k` navigates rendered blocks, not terminal-wrapped rows;
- `c` comments the selected block's full source range;
- comment badges appear in the preview gutter on the block's first visual line;
- `s` toggles to source mode and places the cursor at the block's first line;
- resize rebuilds the Glamour renderer and invalidates only rendered-block
  caches, preserving document, selection, comments, and source offsets;
- parse/render failure falls back to source mode with a visible warning rather
  than making the document unreviewable.

### Key map

| Context | Keys | Action |
|---|---|---|
| Browse | `j/k`, arrows | Move source line or preview block |
| Browse | `ctrl+d/u`, `pgdown/up` | Half/full-page movement |
| Browse | `g/G` | First/last line or block |
| Browse | `{` / `}` | Previous/next document |
| Browse | `n/N` | Next/previous comment |
| Browse | `u` | Next unreviewed document |
| Browse | `v` | Begin range selection in source mode |
| Select range | `j/k` | Extend inclusive selection |
| Select range | `c` or `enter` | Open comment composer for range |
| Select range | `esc` | Cancel selection |
| Browse | `c` | Comment current source line or preview block |
| Browse | `r` | Toggle current document reviewed |
| Browse | `s` | Toggle source/preview |
| Browse | `e` | Edit selected comment |
| Browse | `x` | Delete selected comment after confirmation |
| Composer | `tab` | Cycle comment kind |
| Composer | `alt+enter` | Insert newline |
| Composer | `enter` | Save comment |
| Composer | `esc` | Cancel/back without losing existing comment |
| Browse | `S` | Open review summary |
| Summary | digits | Choose declared verdict |
| Summary | `enter` | Submit after validation/confirmation |
| Any non-editor mode | `esc` | Return to Monitor, preserving draft |

The final bindings may use the existing shared key helpers, but key help must be
generated from the actual bindings. No user-visible shortcut may exist only in a
comment or plan.

### Comment flow

1. Create an anchor from the immutable loaded source lines.
2. Open `shared.NewInputTextarea` for the comment body; suggestions show a second
   replacement textarea or a two-phase composer.
3. Save into the child model with a monotonic round-local ID (`C001`, `C002`, ...).
4. Rebuild per-line/per-block comment indexes.
5. Return a save-draft command to the Monitor.
6. Keep the viewport and selection stable after save.

Deleting a comment never reuses its ID. Editing preserves ID and anchor unless
the user explicitly re-anchors it by deleting/recreating it.

### Submission summary

The summary view must show, before verdict selection:

- every document and reviewed/unreviewed state;
- comment counts by kind and document;
- unresolved validation errors;
- optional overall summary textarea;
- the workflow's exact verdict choices.

Submission is disabled until all documents are explicitly reviewed and the
draft passes domain validation. The child model produces a
`review.Submission`; it does not call the engine directly.

---

## Monitor integration

The Monitor retains ownership of gate queueing and engine routing.

### Queue entry state

Extend `pendingInputEntry` for `inputKindReview` with a `reviewui.Model` and the
latest save/submit error. Because multiple gates can wait concurrently, the
workspace state belongs to the entry, not to one global Monitor field.

On `engine.ReviewRequest`:

1. create the child model from descriptors;
2. load snapshot bodies from path (or `Content` fallback);
3. load `draft.json` when present and matching;
4. append the queue entry without stealing focus;
5. show document/comment progress in the compact input bar.

### Opening and closing

When a review Gate entry is focused, `enter` (and the existing context action)
opens that entry's workspace. The Monitor adds a `focusReview`/`reviewOpen` branch
that temporarily replaces the Steps+Transcript body while leaving the global
footer and queued-input count visible. `esc` closes the workspace back to the
Gate; it does not leave the Monitor or drop drafts.

Other pending input kinds retain the current fixed overlay and non-blocking panel
behavior. Navigating queue entries while a review workspace is open is disabled;
the operator exits the workspace first, preventing one entry's document keys from
changing another entry's draft.

### Message routing

Add Monitor messages:

```go
type SaveReviewDraftMsg struct {
    StepID string
    Draft  review.Draft
}

type ReviewSubmissionMsg struct {
    RunID      string
    StepID     string
    Submission review.Submission
}
```

Draft save commands call `review.Store.SaveDraft` and return a result message so
write errors are visible inside the workspace. Submission routes through
`rootModel` to `Run.ResolveReview`; the queue entry is not optimistically removed.
It remains until the engine emits `ReviewSubmitted`/terminal `StepStatus`, so an
engine validation or persistence error cannot make the action disappear.

### Styling

Add semantic fields under `Styles.Review` in
`internal/tui/shared/styles.go`, derived from the existing palette:

- document current/reviewed/unreviewed rows;
- source gutter, current line, selected range, continuation line;
- comment badge and each comment severity/kind treatment;
- excerpt, suggestion, preview block selection;
- summary valid/error state.

Do not add `lipgloss.NewStyle()` calls in `internal/tui/review` or Monitor files.
The only construction site remains `shared.DefaultTheme()`.

### Existing surfaces to reuse

- `shared.NewInputTextarea` for all comment, replacement, and summary input;
- `shared.Panel`/`PanelFrame` for layout and frame math;
- `shared.Theme.Markdown` and the Monitor's renderer construction conventions;
- the Monitor input queue and no-focus-steal event behavior;
- model-driven `tea.KeyPressMsg` tests;
- conventional `output.md`/`output.json` file rows after review submission.

Do not reuse the transcript's `chatVP` for the workspace. The review child owns
its viewport so returning to the transcript preserves transcript scroll/follow,
search, filters, and selection exactly.

---

## Goals

1. Make review obligations explicit and load-time validated.
2. Let an operator review several labeled Markdown/diff artifacts in one gate.
3. Support exact source-line and multiline comments without editing the source.
4. Preserve drafts while navigating documents, gates, Monitor panels, and TUI
   focus.
5. Batch verdict and feedback atomically so the agent sees the entire review in
   one bounded loop iteration.
6. Preserve a durable, inspectable record of what content was reviewed and what
   feedback was submitted.
7. Provide both readable Markdown feedback and machine-readable JSON.
8. Make rendered Markdown comfortable to read without sacrificing exact source
   anchors.
9. Keep review state deterministic across resize, loop, reset, and journal replay.
10. Keep persistence-off and concurrent input queues first-class.

## Non-goals

1. No in-place Markdown editing, syntax-aware IDE, language server, undo tree, or
   direct source-file writes.
2. No `$EDITOR` subprocess integration in this slice.
3. No mouse text-range selection; keyboard line/range selection is the initial
   complete interaction.
4. No column-level or arbitrary rendered-text selection.
5. No threaded live agent replies per comment. The agent receives the batch only
   after submission.
6. No automatic application of suggestions by jig; the loop target agent decides
   how to apply them.
7. No multi-reviewer identity, permissions, remote collaboration, or cloud state.
8. No PDF, DOCX, image, or HTML review. Initial content is Markdown, text, and
   dependency diffs.
9. No scheduler resurrection for historical runs; replay remains inspectable but
   not actionable without a live `Run`.
10. No inference that the first choice is approval or that a choice named
    `revise` must contain comments.

---

## Repository constraints and invariants

- **Pre-v1 replacement:** remove the scalar review/message mechanisms in the same
  change. No dual TOML syntax, deprecated fields, env flags, or compatibility
  branches.
- **File is truth, bus is liveness:** persisted document bodies, drafts, and final
  feedback live on disk. Events carry descriptors and counts.
- **Persistence-off:** every new path helper and writer no-ops on empty run dir;
  engine/TUI tests prove the content fallback.
- **Static validation:** new review targets are parsed, resolved, validated, and
  tested with valid and every invalid shape.
- **Deterministic order:** target order comes from TOML; comment ordering has an
  explicit stable rule; no map is ranged into user-visible artifacts.
- **Theme singleton:** all styles are constructed in
  `internal/tui/shared/styles.go` from semantic palette tokens.
- **Bubble Tea v2:** handle `tea.KeyPressMsg`; submodels return strings; only root
  returns `tea.View`.
- **Glamour resize:** rebuild the renderer and clear only preview render caches
  when width changes.
- **Verbatim non-Markdown:** diff/text output stays off the Glamour prose path.
- **Bounded content:** document and round byte caps apply before snapshots or TUI
  allocation.
- **No optimistic gate removal:** engine acceptance, not a local keypress, ends a
  review queue entry.
- **Examples are executable:** update and validate `examples/feature.toml` and all
  other affected example workflows.
- **Comments explain why:** document only non-obvious invariants such as immutable
  round snapshots and digest checks, not ordinary control flow.

---

## Current code map

| File/package | Current responsibility | Planned change |
|---|---|---|
| `internal/workflow/schema.go` | `Step.Review string`, `MaxMessages int` | `[]ReviewTarget`; delete `MaxMessages` |
| `internal/workflow/validate.go` | Validates one `@ref` or `diff` | Validate every target, label, dependency, field type, and literal file |
| `internal/workflow/load.go` | Resolves schemas, agent files, templates | Resolve workflow-relative review file targets |
| `internal/engine/event.go` | `ReviewRequest{Choices, Diff, AllowMessage}` | Descriptor-based request plus audit-only `ReviewSubmitted` |
| `internal/engine/engine.go` | Inline review dispatch, message rounds, scheduler maps | Wire active review sessions and atomic submission; remove message state |
| `internal/engine/commands.go` | `verdictMsg` and `humanMessageMsg` | One `reviewSubmissionMsg` command |
| `internal/engine/context.go` | Parses one review ref for consumed fields | Iterate all review targets |
| `internal/engine/journal.go` | Journals/decodes review requests | Decode the new request shape and submitted audit event |
| `internal/datastore/datastore.go` | Step result/transcript/output paths | Add round/draft/snapshot paths; retain history on reset |
| `internal/step/step.go` | Result verdict/output path | Reuse fields; no comment payload added to step state |
| `internal/runner/agent.go` | Appends loop feedback to agent prompt | Reuse unchanged after engine renders full feedback text |
| `internal/tui/monitor/monitor_gate*.go` | Immediate digit verdict and `[m] message` | Open/progress review workspace; remove message compose |
| `internal/tui/monitor/monitor_events.go` | Enqueues review request and retains diff | Instantiate per-entry review model and handle submitted/error events |
| `internal/tui/monitor/monitor_update.go` | Routes three focus regions | Route open review child and preserve existing regions |
| `internal/tui/monitor/monitor_view.go` | Steps/transcript/gate composition | Render full review workspace branch |
| `internal/tui/shared/styles.go` | Shared theme | Add semantic `Styles.Review` group |
| `internal/tui/root_update.go` | Calls `Run.Resolve`/`Run.Message` | Route only `ReviewSubmissionMsg` to `Run.ResolveReview` |

---

## Demoable implementation units

### Unit 1 — Review schema, domain, and durable rounds

**Completion criteria:** Workflows declare multiple labeled targets; invalid
targets fail at load time; pure review types can validate/render comments; and
persisted/persistence-off stores behave deterministically.

#### Tasks

1. Add `ReviewTarget`, replace `Step.Review`, remove `MaxMessages`, and expose
   resolved literal paths in `internal/workflow/schema.go`.
2. Add review-target resolution to `internal/workflow/load.go`.
3. Rewrite `checkReview`, wrong-step-type checks, and field/dependency validation
   in `internal/workflow/validate.go`.
4. Update context field-consumption helpers in `internal/engine/context.go` to
   iterate all targets.
5. Add valid/invalid decode cases covering array syntax, multiple targets,
   duplicate labels/sources, missing fields, unknown refs, missing dependencies,
   non-text fields, missing files, scalar legacy syntax, and removed
   `max_messages` in `internal/workflow/workflow_test.go`.
6. Add pure document, anchor, comment, draft, session, and submission types plus
   validation in `internal/review/model.go`.
7. Add deterministic Markdown rendering in `internal/review/render.go`.
8. Add atomic draft/final storage and snapshot digest verification in
   `internal/review/store.go`.
9. Add table-driven model, renderer golden, atomic-write, same-round reuse,
   digest-mismatch, and persistence-off tests in `internal/review/*_test.go`.
10. Add review round/path helpers and retention semantics in
    `internal/datastore/datastore.go` with focused tests.

#### Proofs

- `go test ./internal/workflow ./internal/review ./internal/datastore -count=1`
- Golden `feedback.md` fixture demonstrates stable document/comment ordering and
  no absolute paths or timestamps.
- Persistence-off tests prove no file/directory creation when the base path is
  empty.

### Unit 2 — Engine snapshots and atomic review submission

**Completion criteria:** The engine snapshots every target, emits descriptor-only
requests for persisted runs, validates one batch submission, writes feedback,
drives scalar guards plus full-text loop feedback, and journals/replays review
events.

#### Tasks

1. Add descriptor-based `ReviewRequest` and audit-only `ReviewSubmitted` in
   `internal/engine/event.go`.
2. Add event encoding/decoding coverage in `internal/engine/journal.go` and
   `journal_test.go`.
3. Add `reviewSubmissionMsg` and delete verdict/human-message commands in
   `internal/engine/commands.go`.
4. Add `Run.ResolveReview`, active session state, reset/loop cleanup, and remove
   message-round state/APIs in `internal/engine/engine.go`.
5. Create `internal/engine/review.go` for round derivation, target resolution,
   content bounds/binary checks, snapshot materialization, dispatch failure, and
   submission finalization.
6. Refactor structured field extraction used by normal inputs and review targets
   into one engine helper so `@step.field` semantics cannot drift.
7. Change loop feedback resolution to prefer review `output.md` while preserving
   scalar verdict guard evaluation.
8. Add `internal/engine/review_test.go` cases for each target form, target order,
   diff collection, bounds, binary/unreadable content, digest reuse/mismatch,
   stale/invalid submissions, unreviewed docs, persistence failure, persistence
   off, and successful finalization.
9. Update engine integration, loop-coalescing, reset, context, journal, and replay
   tests for the new protocol and full feedback content.
10. Delete every review use of `max_messages`, `reviewMessages`, `Run.Message`,
    `humanMessageMsg`, `Diff`, and `AllowMessage`; retain similarly named agent
    input/recovery paths that serve different semantics.

#### Proofs

- `go test ./internal/engine -run 'TestReview|TestLoop|TestReplay|TestReset' -count=1`
- `go test ./internal/engine -race -count=1`
- An integration fixture proves a revise submission with two anchored comments
  re-runs the target once and injects both comments in stable order.
- A persisted request journal line contains snapshot metadata but no source body.

### Unit 3 — Native source-line review workspace

**Completion criteria:** The operator can open any queued review, navigate and
acknowledge documents, select source ranges, create/edit/delete typed comments,
leave/re-enter without losing state, inspect a summary, and submit one batch.

#### Tasks

1. Define the child model, state machine, draft restoration, and submission
   projection in `internal/tui/review/model.go`.
2. Add workspace bindings/help in `internal/tui/review/keys.go`.
3. Add source loading, line indexing, anchor construction, and comment indexes in
   `internal/tui/review/document.go`.
4. Add browse/range/compose/edit/delete/reviewed/summary update paths in
   `internal/tui/review/update.go` using `tea.KeyPressMsg`.
5. Add responsive panels, source gutter, comment list/composer, progress header,
   and summary view in `internal/tui/review/view.go`.
6. Add `Styles.Review` fields and palette-derived construction in
   `internal/tui/shared/styles.go`.
7. Add model-driven tests for navigation, multiline range anchors, soft-wrap
   gutters, comment lifecycle, suggestion validation, reviewed requirements,
   draft restoration, narrow/wide layout, input capture, and summary submission.
8. Extend `pendingInputEntry` and Monitor model/focus state for per-entry child
   workspaces in `internal/tui/monitor/monitor_model.go`.
9. Instantiate/reload review models and handle draft/submitted/error events in
   `internal/tui/monitor/monitor_events.go`.
10. Replace immediate verdict/message gate behavior with progress/open behavior
    in `monitor_gate.go` and `monitor_gate_view.go`.
11. Route open workspace keys/messages and text capture in
    `internal/tui/monitor/monitor_update.go`.
12. Compose the workspace without disturbing Steps/Transcript state in
    `internal/tui/monitor/monitor_view.go` and `monitor_layout.go`.
13. Add draft/submission message types in `internal/tui/monitor/msgs.go` and route
    submissions through `internal/tui/root_update.go`.
14. Add Monitor tests for concurrent review entries, no focus steal, queue
    navigation, open/close restoration, transcript-state preservation, draft save
    errors, no optimistic removal, successful terminal pruning, replay, and
    persistence-off documents.

#### Proofs

- `go test ./internal/tui/review ./internal/tui/monitor ./internal/tui/shared -count=1`
- `go test ./internal/tui/... -race -count=1`
- Sanitized ANSI captures at 120×40 and 80×24 show document obligations, line
  selection, comments, composer, summary, and gate return behavior.

### Unit 4 — Markdown preview, cleanup, docs, and acceptance

**Completion criteria:** Markdown preview remains source-addressable across
resize; legacy review syntax/UI/code is gone; docs/examples describe only the new
model; all repository gates pass.

#### Tasks

1. Promote Goldmark to a direct dependency in `go.mod`.
2. Implement top-level AST block/source-range mapping and rendered-block cache in
   `internal/tui/review/preview.go`.
3. Add preview/source toggle, block navigation, comment anchoring, resize cache
   invalidation, and source fallback behavior to the child model.
4. Add parser/mapping tests for headings, paragraphs, lists, tables, blockquotes,
   fenced code, blank lines, Unicode, soft wrap, and malformed Markdown.
5. Remove obsolete review message controls, tests, help labels, and docs from
   `internal/tui/monitor` and root routing.
6. Update `docs/workflow-schema.md`, `CONTEXT.md`, `AGENTS.md` if needed, and the
   relevant architecture/ADR documentation with the immutable-round and atomic
   submission contracts.
7. Update `examples/feature.toml` and every example review step to array targets;
   remove `max_messages` commentary and exercise multiple document targets.
8. Add an ADR recording why reviewed files are immutable and why feedback is a
   sidecar/batched submission rather than an editor or message-per-comment loop.
9. Generate sanitized acceptance captures and run the full quality suite.

#### Proofs

- `go run ./cmd/jig validate examples/feature.toml`
- All `examples/*.toml` validate.
- `rg 'max_messages|Run\.Message|AllowMessage|humanMessageMsg' --glob '!docs/specs/**'`
  finds no live implementation or current documentation references.
- `gofmt -l -w .`, `go vet ./...`, `go test ./... -count=1`,
  `go test ./... -race -count=1`, and `go build ./cmd/jig` pass.

---

## Requirement-to-proof traceability

| Requirement | Planned proof |
|---|---|
| Multiple explicit review documents | Workflow valid decode + ReviewRequest document-order test |
| Unknown/missing/wrong-type targets rejected | Table-driven workflow invalid cases |
| Exact reviewed bytes preserved | Snapshot digest/reuse tests |
| No document modification | Snapshot/target before-after test; TUI has no write API |
| Single and multiline comments | Child model anchor tests + feedback golden |
| Comments survive navigation/focus | Draft round-trip and Monitor queue tests |
| Every document explicitly reviewed | TUI disabled-submit + engine rejection test |
| Atomic verdict/comments | One scheduler command and integration test |
| Full feedback reaches agent | Revise-loop prompt capture assertion |
| Scalar guard remains valid | Review enum/bool loop condition tests |
| Source/preview mapping | Goldmark block-range table tests |
| Resize preserves review state | Child model resize test with cache-only invalidation |
| Concurrent review gates independent | Two-entry Monitor test with distinct drafts |
| Replay reconstructs pending review | Journal replay + draft load test |
| Persistence-off works | Engine and TUI in-memory fallback/no-write tests |
| Old mechanism deleted | Source grep and strict-TOML legacy rejection |
| Output inspectable | `output.md`, `output.json`, and per-round artifact assertions |

---

## Risk register

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Review target changes between rounds and comments attach to wrong content | medium | high | Immutable snapshots, digests in every anchor, round-scoped drafts, fail closed on mismatch |
| Large documents make rendering or journal events expensive | medium | high | Per-document/aggregate caps; paths in persisted events; cached block rendering |
| Preview visual lines lose source identity | medium | high | Goldmark source segments, block-level preview selection, source mode canonical |
| Submission disappears after an engine/store error | medium | high | No optimistic queue removal; engine terminal event owns pruning |
| Loop agent receives only verdict again | medium | high | Dedicated integration test asserts complete rendered feedback in `StepRequest.Feedback` |
| Concurrent queue entries share child state | low | high | Store child model per `pendingInputEntry`; two-review regression tests |
| Resize destroys selection/drafts | medium | medium | Persist logical source/block identities; invalidate render cache only |
| Draft atomic rename fails on platform/filesystem | low | medium | Same-directory temp file, surfaced save error, retained in-memory draft |
| Removed `max_messages` breaks examples/docs silently | high | medium | Strict decoder, repository grep, validate every example |
| Engine review preparation error bypasses failure policy | medium | medium | Review-specific failure tests for retry/continue/recovery behavior |
| TUI package becomes another monolith | medium | medium | Separate pure domain, child model, document mapper, preview renderer, and thin Monitor adapter |
| Direct Goldmark use diverges from Glamour parsing extensions | low | medium | Configure the same GFM/definition-list extensions Glamour uses and test supported constructs |

---

## Ordered task list and estimates

Each task is intentionally scoped to one package or file; tests follow each
substantive change before downstream consumers depend on it.

| # | Title | Area | Estimate |
|---|---|---|---:|
| 1 | Add pure review types, kinds, anchors, drafts, sessions, submissions, and validation | `internal/review/model.go` | 60 min |
| 2 | Add review-domain validation tests | `internal/review/model_test.go` | 45 min |
| 3 | Add deterministic feedback Markdown renderer | `internal/review/render.go` | 40 min |
| 4 | Add renderer golden and ordering tests | `internal/review/render_test.go` | 35 min |
| 5 | Add atomic review draft/final store and snapshot verification | `internal/review/store.go` | 60 min |
| 6 | Add store, digest mismatch, and persistence-off tests | `internal/review/store_test.go` | 50 min |
| 7 | Add review round/path helpers and reset-retention behavior | `internal/datastore/datastore.go` | 30 min |
| 8 | Add datastore path/no-op/retention tests | `internal/datastore/datastore_test.go` | 25 min |
| 9 | Replace scalar workflow review fields with `[]ReviewTarget` | `internal/workflow/schema.go` | 25 min |
| 10 | Resolve literal review targets relative to workflow files | `internal/workflow/load.go` | 30 min |
| 11 | Validate multi-target review contracts and reject legacy syntax | `internal/workflow/validate.go` | 60 min |
| 12 | Add exhaustive valid/invalid review schema tests | `internal/workflow/workflow_test.go` | 60 min |
| 13 | Iterate review targets in graph-derived consumed-field context | `internal/engine/context.go` | 20 min |
| 14 | Update context tests for multiple reviewed fields | `internal/engine/context_test.go` | 20 min |
| 15 | Replace review request event and add submitted audit event | `internal/engine/event.go` | 25 min |
| 16 | Add journal codecs for the new review events | `internal/engine/journal.go` | 20 min |
| 17 | Add review journal/replay tests | `internal/engine` | 35 min |
| 18 | Add review submission command; delete verdict/message commands | `internal/engine/commands.go` | 25 min |
| 19 | Wire active sessions and `Run.ResolveReview`; delete message-round state | `internal/engine/engine.go` | 60 min |
| 20 | Implement review target resolution, snapshot dispatch, and atomic finalization | `internal/engine/review.go` | 90 min |
| 21 | Add engine unit coverage for review preparation/submission/failure/persistence | `internal/engine/review_test.go` | 90 min |
| 22 | Update loop, reset, integration, and replay tests for batched feedback | `internal/engine` | 75 min |
| 23 | Define review child model and restoration/submission seams | `internal/tui/review/model.go` | 60 min |
| 24 | Add review workspace key map and help | `internal/tui/review/keys.go` | 25 min |
| 25 | Implement source loading, line mapping, anchors, and comment indexes | `internal/tui/review/document.go` | 60 min |
| 26 | Implement workspace state transitions and comment lifecycle | `internal/tui/review/update.go` | 90 min |
| 27 | Implement responsive workspace/source/comment/summary views | `internal/tui/review/view.go` | 90 min |
| 28 | Add child-model source workflow tests and ANSI snapshots | `internal/tui/review/model_test.go` | 90 min |
| 29 | Add semantic review styles | `internal/tui/shared/styles.go` | 35 min |
| 30 | Add shared-style/panel width regression tests | `internal/tui/shared` | 25 min |
| 31 | Extend Monitor queue entries and focus state for review child models | `internal/tui/monitor/monitor_model.go` | 35 min |
| 32 | Instantiate/reload review models and process review events | `internal/tui/monitor/monitor_events.go` | 45 min |
| 33 | Replace verdict/message gate UI with workspace progress/open behavior | `internal/tui/monitor` | 45 min |
| 34 | Route workspace keys, child commands, and text capture | `internal/tui/monitor/monitor_update.go` | 50 min |
| 35 | Render/resize the workspace without disturbing Monitor panels | `internal/tui/monitor` | 50 min |
| 36 | Add review draft/submission Bubble Tea messages | `internal/tui/monitor/msgs.go` | 20 min |
| 37 | Route atomic submissions to the live run | `internal/tui/root_update.go` | 20 min |
| 38 | Add Monitor concurrent-queue, restoration, persistence, and routing tests | `internal/tui/monitor` | 90 min |
| 39 | Promote Goldmark and configure direct preview parsing | `go.mod` | 15 min |
| 40 | Implement Markdown block/source mapping and preview cache | `internal/tui/review/preview.go` | 75 min |
| 41 | Add preview mapping, resize, fallback, and construct coverage | `internal/tui/review/preview_test.go` | 60 min |
| 42 | Delete legacy review-message engine APIs, state, and tests | `internal/engine` | 25 min |
| 43 | Delete legacy review-message Gate UI, help, routing, and tests | `internal/tui/monitor` | 25 min |
| 44 | Update workflow schema and architecture documentation | `docs/` | 45 min |
| 45 | Add immutable-review-round ADR | `docs/adr/` | 35 min |
| 46 | Update all example workflows and validate them | `examples/` | 45 min |
| 47 | Run full quality gates and generate sanitized acceptance captures | `.` | 60 min |

**Estimated total:** approximately 1,585 focused minutes (26–27 hours), excluding
human review and remediation discovered by the full race suite.

---

## Verification commands

```bash
go test ./internal/review ./internal/datastore ./internal/workflow -count=1
go test ./internal/engine -count=1
go test ./internal/tui/review ./internal/tui/monitor ./internal/tui/shared -count=1
go test ./... -count=1
go test ./... -race -count=1
go run ./cmd/jig validate examples/feature.toml
go build ./cmd/jig
gofmt -l -w .
go vet ./...
```

Repository cleanup checks:

```bash
rg 'max_messages|Run\.Message|AllowMessage|humanMessageMsg' \
  --glob '!docs/specs/**' --glob '!docs/adr/**'

rg 'review\s*=\s*"(@|diff)' \
  --glob '*.toml' --glob '!docs/specs/**'
```

Both searches should return no live implementation/current-example matches after
the intentional migration. Historical specs may retain the old vocabulary as an
accurate record of what they implemented.

## Acceptance walkthrough

1. Run a workflow whose review step targets an agent field, a static Markdown
   file, and a dependency diff.
2. Confirm the compact Gate names the review step and shows `0/3 reviewed` without
   stealing focus.
3. Open the workspace and verify all three author labels appear in declared order.
4. Navigate source lines, select a multiline range, create a concern, create a
   suggestion with replacement text, and add a praise comment from preview mode.
5. Mark two documents reviewed, open Summary, and verify submission is blocked
   with the remaining document named.
6. Leave to the Monitor, inspect another step transcript, return to the Gate, and
   verify document, cursor, range, comments, modes, and scroll positions persist.
7. Close/replay the Monitor and verify the persisted draft and snapshots are
   inspectable.
8. Mark the last document reviewed, submit `revise`, and verify the gate remains
   until engine acceptance.
9. Verify `output.json`, `output.md`, round `submission.json`, and round
   `feedback.md` contain the complete batch in stable order.
10. Verify the loop target runs exactly once and its captured `input.md` contains
    both comments, excerpts, suggestion, summary, and verdict.
11. On the next iteration, verify a new round directory and fresh target digests;
    prior comments remain historical and are not silently attached to new bytes.
12. Submit the approving verdict after reviewing the revised documents and verify
    downstream execution proceeds normally.
