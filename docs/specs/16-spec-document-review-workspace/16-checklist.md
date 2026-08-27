# Checklist: Native Document Review Workspace (Spec 16)

Use this as a phase-by-phase execution list for context-window-sized work.

## Phase 1 — Schema, domain model, and persistence foundations

- [ ] Add `ReviewTarget` + remove old scalar review/max_messages from `internal/workflow/schema.go`
  - Add `Step.Review []ReviewTarget`
  - Add `ResolvedPath` on target (non-TOML field)
  - Remove `Step.MaxMessages`
- [ ] Resolve literal review sources relative to workflow in `internal/workflow/load.go`
- [ ] Enforce review validation in `internal/workflow/validate.go`
  - non-empty `review`
  - non-blank/required `source`, `label`
  - unique trimmed labels
  - unique resolved sources
  - valid `@step` / `@step.field` refs
  - text-only `@step.field` only
  - literal file exists + regular file
  - reject scalar legacy review syntax
  - reject `max_messages`
  - reject review on non-review steps
- [ ] Update field consumption in `internal/engine/context.go` to include all review targets
- [ ] Add review domain entities in `internal/review/model.go`
  - `Kind`, `Document`, `Anchor`, `Comment`, `Draft`, `Submission`, `ViewState`
  - submission/anchor/comment validation
- [ ] Add deterministic review renderer in `internal/review/render.go`
- [ ] Implement review storage in `internal/review/store.go`
  - immutable snapshots
  - atomic draft/final writes
  - digest verification and mismatch handling
  - persistence-off fallback behavior
- [ ] Add review path helpers in `internal/datastore/datastore.go`
  - `ReviewRoot`, `ReviewRoundDir`, `ReviewDraftPath`, `ReviewSubmissionPath`, `ReviewFeedbackPath`, `ReviewDocumentsDir`
  - ensure empty runDir returns `""`
  - ensure `ClearStepOutputs` keeps `review/` history
- [ ] Add/adjust tests
  - `internal/workflow/workflow_test.go`
  - `internal/review/*_test.go`
  - `internal/datastore/datastore_test.go`
  - `internal/engine/context_test.go`
- [ ] Proof: `go test ./internal/workflow ./internal/review ./internal/datastore -count=1`

## Phase 2 — Engine protocol and atomic submission

- [ ] Replace review event/request model in `internal/engine/event.go`
  - descriptor-only `ReviewRequest`
  - add `ReviewSubmitted`
- [ ] Add journal encoding/decoding for review events in `internal/engine/journal.go` and tests
- [ ] Replace verdict/message command path in `internal/engine/commands.go`
- [ ] Add session and submission API in `internal/engine/engine.go`
  - `reviewSessions` map
  - remove `reviewMessages` and message-round review paths
  - add `Run.ResolveReview`
- [ ] Implement review dispatch/finalization path in `internal/engine/review.go`
  - target resolution for `diff`, `@step`, `@step.field`, literal path
  - size limits + binary/NUL rejection
  - per-round snapshot writing and round ID from generation/iteration
  - stale-session checks
  - failure path does not emit review request
- [ ] Update loop feedback selection to prefer review output projection
- [ ] Replace scalar-only loop assumptions in related engine tests
- [ ] Add/adjust review tests in `internal/engine/review_test.go`
  - target forms, order, bounds, binary/unreadable checks
  - digest reuse/mismatch
  - persistence-on/off
  - stale/invalid submissions, unreviewed docs rejection
  - successful atomic finalization
- [ ] Run phase proofs
  - `go test ./internal/engine -run 'TestReview|TestLoop|TestReplay|TestReset' -count=1`
  - `go test ./internal/engine -race -count=1`

## Phase 3 — Review workspace core (source-line TUI model)

- [x] Build review child model in `internal/tui/review/model.go`
- [x] Add review key map/help in `internal/tui/review/keys.go`
- [x] Add document indexing and anchors in `internal/tui/review/document.go`
- [x] Implement workspace update flow in `internal/tui/review/update.go`
  - document navigation
  - range selection + draft lifecycle
  - add/edit/delete comments
  - mark docs reviewed
  - summary editing
- [x] Implement workspace rendering in `internal/tui/review/view.go`
- [x] Add tests in `internal/tui/review/model_test.go`
  - navigation, range anchors, suggestion validation, restoration, draft behavior
- [x] Proof: `go test ./internal/tui/review -count=1`

## Phase 4 — Monitor integration and routing

- [x] Add per-entry review model storage in `internal/tui/monitor/monitor_model.go`
- [x] Handle review events in `internal/tui/monitor/monitor_events.go`
- [x] Route keys/text to open child workspace in `internal/tui/monitor/monitor_update.go`
- [x] Replace gate behavior in `internal/tui/monitor/monitor_gate.go` and `internal/tui/monitor/monitor_gate_view.go`
- [x] Compose review workspace in monitor views (`monitor_view.go`, `monitor_layout.go`)
- [x] Reuse shared review/TUI theme styles in `internal/tui/shared/styles.go`
- [x] Wire `ReviewSubmissionMsg` path
  - `internal/tui/monitor/msgs.go`
  - `internal/tui/root_update.go`
- [ ] Add monitor tests for:
  - concurrent review queue entries
  - no focus steal
  - draft persistence errors
  - restore after queue navigation
  - replay state
- [x] Proofs
  - `go test ./internal/tui/monitor ./internal/tui/shared -count=1`
  - `go test ./internal/tui/... -race -count=1`

## Phase 5 — Markdown preview, cleanup, docs, examples, acceptance

- [ ] Add/upgrade preview dependency in `go.mod`
- [ ] Implement markdown source-block mapping in `internal/tui/review/preview.go`
- [ ] Add preview/source workspace behavior and resize cache invalidation
- [ ] Remove legacy review-message mechanics from implementation
  - remove/update references: `Run.Message`, `humanMessageMsg`, `AllowMessage`, `max_messages`
- [ ] Refresh docs/examples
  - `docs/workflow-schema.md`
  - `CONTEXT.md`
  - relevant spec/ADR docs
  - all review examples in `examples/` to array `review` targets
- [ ] Add immutability/sidecar ADR in `docs/adr/`
- [ ] Run final quality and migration checks
  - `go run ./cmd/jig validate examples/feature.toml`
  - `rg 'max_messages|Run\.Message|AllowMessage|humanMessageMsg' --glob '!docs/specs/**'`
  - `go test ./... -count=1`
  - `go test ./... -race -count=1`
  - `gofmt -l -w .`
  - `go vet ./...`
  - `go build ./cmd/jig`

## Completion acceptance markers (end-to-end)

- [ ] Review request uses ordered, immutable document descriptors, not full body payload.
- [ ] Review submission is atomic and includes verdict + comments in one structured batch.
- [ ] Engine rejects submissions missing required reviewed documents.
- [ ] Loop feedback receives full rendered comments/excerpts (stable order).
- [ ] Replay can reconstruct pending reviews and draft state.
- [ ] No legacy `max_messages`/per-message review API remains in live docs/examples.
