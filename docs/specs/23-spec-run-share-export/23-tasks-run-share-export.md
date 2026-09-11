# 23-tasks-run-share-export.md

Spec: [`23-spec-run-share-export.md`](./23-spec-run-share-export.md)
Clarifications: [`23-questions-1-run-share-export.md`](./23-questions-1-run-share-export.md)
Audit: `23-audit-run-share-export.md` (generated after sub-tasks)

## Requirement Coverage

| Functional Requirement | Parent Task(s) | Planned Test Evidence |
| --- | --- | --- |
| FR-01 — CLI contract | 2.0 | CLI parser/help/exit-code tests in `cmd/jig` |
| FR-02 — Target resolution | 1.0 | Table-driven run/destination refusal tests and no-write assertions |
| FR-03 — Structural archive | 2.0 | Parsed ZIP member and structural projection assertions |
| FR-04 — Closed disclosure policy | 2.0 | Seeded-private-value exclusion scan across members and ZIP metadata |
| FR-05 — Identity and time | 2.0 | Alias/reference ordering and signed relative-time tests |
| FR-06 — Publication and exits | 2.0, 4.0 | Atomic no-overwrite publication, stream, failure, and signal tests |
| FR-07 — Explicit content mode | 3.0 | Paired structural/text CLI and manifest assertions |
| FR-08 — Included and excluded text | 3.0 | Transcript projection and invariant omission tests |
| FR-09 — Sanitization | 3.0 | Adversarial nested/escaped identifier, path, and secret fixtures |
| FR-10 — Omission and safe representation | 3.0 | Invalid JSON, control-sequence, diff, and sanitize-before-truncate tests |
| FR-11 — Privacy accounting | 3.0 | Manifest counter and no-private-detail assertions |
| FR-12 — Ownership lease | 1.0, 4.0 | Separate-process scheduler/export contention and lock-lifetime tests |
| FR-13 — Inactive and damaged states | 4.0 | Settled, interrupted, paused, orphaned, corrupt, and no-evidence fixtures |
| FR-14 — Completeness | 4.0 | Exact gap-code and partial/complete archive assertions |
| FR-15 — Confinement and concurrent changes | 1.0, 4.0 | Symlink/special-file/traversal and source-change injection tests |
| FR-16 — Resource bounds | 4.0 | Oversized-record, total-budget, inventory, archive-cap, and streaming tests |
| FR-17 — Documentation and verification | 5.0 | Documentation contract checks and repository-wide offline quality gates |

## Relevant Files

| File | Why It Is Relevant |
| --- | --- |
| `internal/runexport/export.go` | New. Narrow `Export(context.Context, Options)` orchestration seam used by the CLI; coordinates collection, projection, archive construction, and publication without exposing source records. |
| `internal/runexport/source.go` | New. Existing-run and destination validation, `os.Root`-confined selected-file discovery, source fingerprints, consistency recheck, and total-read accounting. |
| `internal/runexport/model.go` | New. Dedicated versioned archive records for manifest, gaps/counters, run/step summaries, projected events, and text entries; no persisted source type is marshaled directly. |
| `internal/runexport/journal.go` | New. Bounded streaming journal decoder, valid-prefix/error classification, closed event projection, and accepted-record status folding. |
| `internal/runexport/aliases.go` | New. Deterministic run/workflow/step/tool aliases and relative-time anchoring/references. |
| `internal/runexport/archive.go` | New. Generated README, sanitized-member spooling, sizes/digests, fixed ZIP metadata/order, private temporary archive, and no-replace publication. |
| `internal/runexport/sanitize.go` | New. Full deterministic free-text replacement, identifier/path boundaries, prior-marker removal, control stripping, UTF-8-safe post-sanitization truncation, and replacement accounting. |
| `internal/runexport/transcript.go` | New. Bounded streaming transcript decoder and explicit text-mode projection for supported roles, blocks, tool exchanges, diffs, omissions, and malformed lines. |
| `internal/runexport/export_test.go` | New. End-to-end package tests for request resolution, lease lifetime, source preservation, structural/text bundles, publication, and cancellation. |
| `internal/runexport/journal_test.go` | New. Table-driven event projection, aliases/times, status authority, valid-prefix, unknown-kind, and gap tests. |
| `internal/runexport/sanitize_test.go` | New. Adversarial credential, entropy, prior-marker, JSON, path/identifier boundary, control, invalid UTF-8, and truncation tests. |
| `internal/runexport/transcript_test.go` | New. Transcript inclusion/omission, tool correlation, malformed-line continuation, deterministic ordering, and streaming/bounds tests. |
| `internal/runexport/archive_test.go` | New. Exact member contract, ZIP metadata/order, digest/length, permissions, collision, cleanup, archive-cap, and disclosure scans. |
| `internal/runexport/testutil_test.go` | New. Synthetic run-store/event/transcript fixtures using `t.TempDir()` and placeholder-only private values, including retry/reset/route/fan-out and damaged histories. |
| `internal/engine/runlock.go` | New. Narrow shared scheduler ownership lease used by Start, Resume, lock probes, and export; owns descriptor lifetime and inode identity. |
| `internal/engine/runlock_test.go` | New. Lock creation, contention, release, file identity, permission, and separate-process ownership tests. |
| `internal/engine/engine.go` | Modify. Store/release the shared lease type on a live `Run` instead of owning raw lock mechanics. |
| `internal/engine/resume.go` | Modify. Remove embedded lock helpers in favor of `runlock.go`; expose a pure checked workflow-snapshot decoder for already-confined bytes. |
| `internal/engine/resume_test.go` | Modify. Preserve Start/Resume and workflow-snapshot behavior after the lock/snapshot seam extraction. |
| `internal/sentinel/rules.go` | Modify. Extract the existing credential/high-entropy detectors into a pure match API so export can fully replace the same recognized values without changing live-monitor policy. |
| `internal/sentinel/guard_test.go` | Modify. Prove detector extraction preserves existing guard and partial live-redaction behavior while exposing complete match spans/categories to export. |
| `cmd/jig/export.go` | New. Thin `export` flag parser, injected stdout/stderr wrapper, signal-aware context, notices, and exit mapping. |
| `cmd/jig/export_test.go` | New. CLI help/arity/flags-after-ID, success/partial/failure/signal, exact stream, notice, and destination-collision tests. |
| `cmd/jig/main.go` | Modify. Dispatch `jig export` before the TUI and list it in unknown-command usage. |
| `cmd/jig/ops.go` | Modify. Add `export` discovery text to top-level help; reuse the existing argument-reordering convention. |
| `cmd/jig/ops_test.go` | Modify. Assert top-level help and argument reordering continue to cover the new command. |
| `internal/datastore/datastore.go` | Read-only reference. Existing run-ID resolution and canonical journal/workflow/transcript/lock paths define the persisted layout. |
| `internal/engine/journal.go` | Read-only reference. `Envelope`, known event decoders, and unknown-kind semantics are the source vocabulary for the closed projection. |
| `internal/engine/event.go` | Read-only reference. Enumerates fields that must be explicitly allowed or omitted for each event family. |
| `internal/ops/status.go` | Read-only reference. `FoldStatus` is the existing state/totals semantic seam for accepted journal records. |
| `internal/transcript/transcript.go` | Read-only reference. Public entry/block/tool normalization types define supported and unknown persisted transcript content. |
| `internal/toolcall/toolcall.go` | Read-only reference. Normalized tool input/output/content/diff structure informs text-mode parsing and exclusions. |
| `internal/workflow/schema.go` | Read-only reference. Captured resolved step type/backend/transport/dependency fields allowed into structural output. |
| `internal/headless/types.go` | Read-only reference. Reuse repository exit-code meanings for 0/1/2/130; map SIGTERM to 143 in the CLI. |
| `docs/operations.md` | Modify. Document the CLI, archive v1 fields, selected/excluded sources, limits, states, completeness, sanitization limitations, streams, exits, and cancellation. |
| `README.md` | Modify. Add export discovery and link the recipient/operator contract. |
| `docs/TESTING.md` | Modify. Correct stale package coverage notes and record the offline export/race verification seam. |
| `docs/plans/open-goals.md` | Modify only after all acceptance checks pass. Mark A22 done and link the completed spec artifacts. |
| `docs/specs/23-spec-run-share-export/artifacts/` | New during implementation. Sanitized CLI captures, archive listings/parsed summaries, source-hash checks, and quality-gate outputs for the five parent tasks. |

### Notes

- Write tests before implementation within each parent task; use table-driven
  cases, `t.Run`, `t.TempDir()`, injected `io.Writer`s, and helper processes for
  cross-process advisory-lock assertions. Tests must not invoke a model, backend,
  credentials, or network.
- Keep `cmd/jig` thin and put export policy in `internal/runexport`. The engine
  remains free of CLI, ZIP, transcript-export, and sanitizer concerns; only its
  generic lock and workflow-snapshot seams are shared.
- Treat every source value as untrusted. Tests and proof captures use only
  synthetic identifiers and placeholder credential shapes; never commit a real
  run archive, source mapping, raw transcript, or temporary staging file.
- New files follow `docs/CONVENTIONS.md`: one concern per file, constants near
  their reader, named event projection functions instead of repeated large
  switch arms, and comments that explain non-obvious safety decisions.
- Run targeted tests throughout, then `go test ./... -count=1`, targeted `-race`,
  `go vet ./...`, `gofmt -l .`, and validation of every `.agents/jig/*.toml`
  before closing A22.

## Tasks

### [x] 1.0 Safe export entry and evidence acquisition

Establish the focused internal export boundary and make selection fail closed
before any run evidence is consumed.
Resolve the run with existing identifier rules, validate a non-existing
destination outside the persistence root, acquire the scheduler's exact
non-blocking ownership lease, and inventory only explicitly allowed regular
files through traversal-resistant opens. This slice is demoable at the package
seam through deterministic refusal of invalid, live, aliased, or unsafe targets
without publishing an archive or modifying run payloads. Production command
wiring lands with the first valid archive in 2.0 so no intermediate CLI accepts
an eligible request it cannot complete.

Covers spec FRs: FR-02, FR-12, and the acquisition/confinement portion of FR-15.

#### 1.0 Proof Artifact(s)

- Test: `go test ./internal/runexport -run 'Test(ResolveRequest|AcquireLease|ConfinedInventory)' -v`
  passes — demonstrates empty/path-shaped/missing/file/symlink run rejection,
  destination containment and collision refusal (including dangling symlinks),
  live-owner refusal, and selected-file-only inventory.
- Test: `go test ./internal/engine ./internal/runexport -run 'TestRunLeaseSeparateProcess|TestExportRejectsLiveScheduler' -v`
  rejects export while Start/Resume owns
  `scheduler.lock`, then succeeds in acquiring the same inode after release —
  demonstrates that a parked scheduler is still live and lock unavailability is
  never treated as inactivity.
- Diff: `artifacts/1-source-preservation.json` records before/after hashes of
  the synthetic run payload as identical, with an
  absent lock allowed to become an empty coordination file only — demonstrates
  the source-preservation boundary.

#### 1.0 Tasks

- [x] 1.1 Add `internal/runexport/testutil_test.go` with builders for a temporary
  `.jig/runs/<id>` store, exact journal envelopes, captured workflow snapshots,
  direct step directories, transcripts, symlinks/special files, and payload
  hashing. Use only conspicuous synthetic private strings and credentials.
- [x] 1.2 Write failing table-driven `internal/runexport/export_test.go` cases
  for empty roots, invalid/path-shaped run IDs, missing runs, file/symlink run
  targets, missing destination parents, destinations under the resolved
  persistence root, existing files/directories/symlinks, and dangling symlinks;
  assert no directory, destination, or run payload is created or changed.
- [x] 1.3 Define the narrow package seam in `internal/runexport/export.go`:
  `Options` carries root, run ID, destination, text mode, and test-only hooks;
  `Export(context.Context, Options)` returns only fixed-category result/notices
  and typed usage/operational/cancellation errors, never source text.
- [x] 1.4 Extract scheduler locking from `internal/engine/resume.go` into
  `internal/engine/runlock.go` as a closeable non-blocking ownership lease.
  Keep the lock descriptor open for the lease lifetime, expose only the inode
  identity needed for consistency checks, create an absent file without
  truncating it, and make Start, Resume, and `RunLockState` use the same code.
- [x] 1.5 Update `internal/engine/engine.go` to store and close the lease on
  `Run`, preserving persistence-off behavior and every existing Start/Resume
  release path; remove the superseded raw `*os.File` helpers from `resume.go`.
- [x] 1.6 Add `internal/engine/runlock_test.go` coverage for absent-file
  creation with the existing mode contract, non-blocking contention,
  release/reacquire, unchanged pre-existing lock bytes, and stable file
  identity. Retain the existing `RunLockState` assertions.
- [x] 1.7 Add a helper-process lock test that owns the lease in a separate
  process while the parent attempts export acquisition, including a simulated
  scheduler parked with no worker. Assert immediate fixed-category refusal,
  then successful acquisition of the same file after the child releases it.
- [x] 1.8 Implement request resolution in `internal/runexport/source.go` by
  calling `datastore.ResolveRunDir`, resolving the persistence-root and
  destination-parent identities, rejecting any destination within the selected
  root, and using `Lstat`-style existence checks so dangling links collide.
  Do not create the destination parent or run directory.
- [x] 1.9 Open the resolved run as an `os.Root` and inventory only literal
  `journal.jsonl`, `workflow.json`, `scheduler.lock`, `steps/`, direct step
  directories, and their literal `transcript.jsonl` entries. Reject selected
  symlinks and non-regular/special files, never recurse into artifacts/reviews,
  and never open a path found inside persisted data.
- [x] 1.10 Capture an immutable inventory record for every selected source and
  coordination object (relative category, identity, type, size, modification
  time, and expected presence), plus the sorted direct step-directory names and
  resolved run-root identity. Provide a recheck operation that reports only
  fixed source category/alias/count data; full change-injection coverage lands
  in 4.0.
- [x] 1.11 Ensure acquisition closes all roots/files and releases the lease on
  every validation, inventory, and context-cancellation error. Add tests that
  immediately reacquire the lease after each forced failure and find no export
  temporary file or destination.
- [x] 1.12 Run `go test ./internal/engine ./internal/runexport -run 'Test(ResolveRequest|AcquireLease|ConfinedInventory|RunLock)' -v`
  and capture the sanitized result plus before/after payload hashes under the
  spec artifact directory as the task 1.0 proof.

### [x] 2.0 Deterministic structural archive for an intact inactive run

Deliver the `jig export` CLI and default end-to-end export for a synthetic
intact run. Stream a
closed projection of the captured workflow, accepted journal records, current
observed step state, and ordered events into the fixed ZIP layout; allocate
stable run/workflow/step aliases and relative times; preserve retry, route,
reset, and dynamic fan-out relationships; generate a source-text-free README
and manifest; and publish privately without overwriting a competing target.
Unknown values remain unknown and unsupported enums become the fixed `unknown`
value. The command succeeds with only the caller-supplied destination on stdout
and produces an ordinary archive that can be inspected offline.

Covers spec FRs: FR-01, FR-03, FR-04, FR-05, and FR-06.

#### 2.0 Proof Artifact(s)

- CLI: `jig export --help` shows the exact positional/flag contract, both
  content modes, supported inactive states, local-only behavior, and no raw,
  upload, prompt, or overwrite mode — demonstrates FR-01 discoverability.
- Test: `go test ./cmd/jig -run 'TestExport(Usage|Help|TargetRefusals)' -v`
  passes — demonstrates flags after `RUN_ID`, required destination, exact
  usage/operational exit mapping, and stdout/stderr separation.
- CLI: `artifacts/2-structural-cli.txt` captures a structural export from an
  inactive synthetic run with a
  retry, route, reset, and dynamic fan-out children, followed by `unzip -l` and
  `unzip -p <bundle> README.md` — demonstrates the complete operator and
  recipient flow using ordinary offline tools.
- Test: `go test ./internal/runexport -run 'TestStructuralArchive' -v` passes
  parsed assertions for exact member names/order, versioned manifest fields,
  member sizes/digests, status/totals, event ordering, and relationships —
  demonstrates FR-03's fixed contract.
- Test: `go test ./internal/runexport -run 'TestClosedProjectionExcludesPrivateData' -v`
  scans every member plus ZIP headers/comments/extra fields and finds none of
  the fixture's original IDs, prose, paths, sessions, workflow source, tool
  payloads, code, Git identifiers, or private-content hashes — demonstrates
  the default closed disclosure policy.
- Test: `go test ./internal/runexport -run 'TestAliasesAndRelativeTimes' -v`
  passes with declared, journal-discovered, and directory-discovered steps,
  missing timestamps, and clock reversal — demonstrates consistent references,
  prescribed allocation order, nullable time, and signed millisecond offsets.
- Test: `go test ./internal/runexport ./cmd/jig -run 'TestArchivePublication|TestExportDestinationRace|TestExportStreamsAndExits' -v`
  asserts owner-only temporary and
  destination permissions, fixed ZIP timestamps, cleanup before publication,
  no replacement of a racing destination, and exact stdout/stderr/exit behavior
  — demonstrates FR-06.

#### 2.0 Tasks

- [x] 2.1 Write failing structural-export tests in
  `internal/runexport/{journal,archive}_test.go` from one intact synthetic run
  containing retry attempts, iterations, generations, a route, reset closure,
  gate/review activity, costs/tokens, and dynamic fan-out children. Parse ZIP
  members as JSON/JSONL instead of snapshotting opaque bytes.
- [x] 2.2 Define dedicated archive-v1 types in `internal/runexport/model.go` for
  manifest/member metadata, run/step summaries, event families, completeness
  gaps, and counters. Give every serialized field an explicit JSON tag and
  avoid embedding or marshaling `workflow.Workflow`, `step.Result`,
  `engine.Event`, `transcript.Entry`, or `toolcall.Activity` directly.
- [x] 2.3 Implement a streaming journal reader in
  `internal/runexport/journal.go` over an already-confined file handle. Preserve
  envelope sequence/time and unknown kinds, accept only newline-complete JSON,
  stop at the first malformed complete record, distinguish a torn final record,
  and never use `ReplayJournal`'s hydrated or synthetic display events.
- [x] 2.4 Extract a pure `DecodeWorkflowSnapshot([]byte)` seam from
  `internal/engine/resume.go`, make the existing path-based loader delegate to
  it, and preserve checksum/module validation tests. Feed it only bytes already
  read through the exporter's rooted source handle; never resolve current
  author files or hydrate review content.
- [x] 2.5 Implement alias allocation in `internal/runexport/aliases.go`: fixed
  `run-1`/`workflow-1`; step aliases from `RunStarted.Steps`, then first journal
  occurrence, then sorted direct-directory discovery; and consistent reference
  lookup for dependencies, route/reset closure, and fan-out parent/children.
  Keep original-to-alias maps in memory only.
- [x] 2.6 Compute the time anchor from the first valid accepted journal
  timestamp, falling back to the earliest retained transcript timestamp when
  needed. Emit nullable signed integer milliseconds, preserve clock reversal,
  and add no current or original absolute date to member content or metadata.
- [x] 2.7 Implement named closed-projection functions for every event family in
  the spec table. Validate statuses, step types, backends, transports, roles,
  tool states, counters, tokens, and costs against explicit known/finite/range
  rules; output fixed `unknown` or null plus a fixed gap/counter rather than
  copying an unsupported value or parser error.
- [x] 2.8 Fold the accepted journal prefix through `ops.FoldStatus` (or a pure
  equivalent proven against it) with the pre-export ownership fact, then build
  aliased ordered step/totals/state records. Do not probe the held export lease
  as scheduler activity, invent terminal events, or claim authority without
  sufficient `RunStarted` and intact journal evidence.
- [x] 2.9 Generate `README.md`, `run.json`, and `events.jsonl` from export-only
  records. Keep README prose fully generated from fixed templates and validated
  enums/numbers/aliases; never interpolate workflow names, IDs, errors,
  conditions, source paths, or any other source text.
- [x] 2.10 Implement sanitized-member spooling and accounting in
  `internal/runexport/archive.go`: stream only already-projected bytes to
  owner-only temporary storage, calculate byte lengths/SHA-256 for exported
  members, construct `manifest.json` after those values are known, and remove
  every spool on success or failure. Never stage raw evidence or alias maps.
- [x] 2.11 Write the ZIP in the specified literal member order with regular-file
  modes, a fixed format-neutral timestamp, empty comments, and no source-derived
  extra fields. Enforce literal member names and omit `transcript.jsonl` in
  structural mode; digests cover exported members other than the manifest.
- [x] 2.12 Publish from an owner-only temporary archive in the destination
  directory using a no-replace operation that cannot overwrite a destination
  created after validation. Close all writers before publication, clean up the
  exporter's temporary files on every pre-publication failure, and preserve a
  competing file byte-for-byte.
- [x] 2.13 Complete `Export` orchestration so it holds the ownership lease from
  before evidence reads through final inventory recheck and publication, checks
  context at streaming boundaries, returns complete/partial status without raw
  detail, and closes all resources on every exit.
- [x] 2.14 Add `cmd/jig/export.go` with an injected-writer `exportMain` and thin
  `runExport`: parse exactly one `RUN_ID`, required `--destination`, optional
  `--root` (default `.jig`) and `--include-text`, using `reorderArgs` for flags
  after the identifier. Map usage/operational/signal results to 2/1/130/143;
  print only the supplied destination plus newline on success and fixed notices
  or errors to stderr.
- [x] 2.15 Register `export` in `cmd/jig/main.go` and top-level `printHelp`, and
  add CLI tests for help, arity, flag ordering, empty root, target refusals,
  structural success, destination races, exact streams, and exit codes. Help
  must explain both modes, eligible states, local-only operation, and the
  absence of raw/upload/overwrite behavior.
- [x] 2.16 Add archive-wide disclosure tests that seed original run/workflow/
  step IDs, source/home paths, sessions, prose, code, tool payloads, Git SHAs,
  private digests, unknown fields, and hostile ZIP-looking strings, then scan
  member names/content and every ZIP header/comment/extra field for absence.
- [x] 2.17 Capture the task 2.0 CLI proof: build `jig`, export the intact
  synthetic fixture, record exact stdout/stderr and exit code, run `unzip -l`,
  extract the generated README, and save parsed manifest/run/event assertions
  plus unchanged source hashes under the spec artifact directory.

### [x] 3.0 Explicit sanitized conversation-text export

Add the opt-in `sanitized_text` mode without weakening the structural default.
Project supported transcript prose and normalized tool exchanges into ordered
JSONL with stable scoped tool aliases, while representing thinking,
attachments, binary/external/unknown content, and malformed payloads only by
fixed omission markers. Route every retained free-text value through one
deterministic full-replacement sanitizer before truncation, covering existing
sentinel secret detectors, prior suffix-bearing redaction markers, known
identifiers, bounded paths, nested JSON keys/values, inline diffs, and terminal
controls. Record only fixed aggregate replacement, omission, and truncation
counters, and warn both the operator and recipient that review is still
required before sharing.

Covers spec FRs: FR-07, FR-08, FR-09, FR-10, and FR-11.

#### 3.0 Proof Artifact(s)

- CLI: `artifacts/3-text-mode-cli.txt` records paired exports from the same
  synthetic fixture, with archive listings and parsed
  manifests, show `transcript.jsonl` absent in structural mode and present only
  after `--include-text`; stderr and both README modes carry the required
  best-effort/review notices — demonstrates explicit opt-in and residual-risk
  communication.
- Test: `go test ./internal/runexport -run 'TestTextArchiveProjection' -v`
  passes — demonstrates retained roles, block types, sequences, coordinates,
  tool-use/result correlation, deterministic transcript order, and invariant
  thinking/attachment/session/artifact exclusions.
- Test: `go test ./internal/runexport -run 'TestSanitizer' -v` passes
  adversarial cases for nested and escaped JSON keys/values, overlapping IDs,
  token/path boundaries, tool titles and payloads, inline code/diffs, every
  supported credential shape, high-entropy tokens, and prior sentinel markers
  — demonstrates one complete deterministic sanitization boundary.
- Test: `go test ./internal/runexport -run 'TestSanitizerMalformedAndTruncation' -v`
  covers malformed JSON, unsupported blocks, C0/C1 controls, invalid UTF-8, and
  secrets crossing the 64 KiB output boundary produce fixed safe markers or
  post-sanitization truncation without raw parser/source text — demonstrates
  FR-10's safe representation order.
- Test: `go test ./internal/runexport -run 'TestPrivacyCounters' -v` parses
  `manifest.json` and asserts exact fixed-category aggregate counts
  for replacements, omissions, malformed content, and truncations, while an
  archive-wide scan finds no matched text, suffixes, maps, or hashes —
  demonstrates privacy accounting without a secondary disclosure.

#### 3.0 Tasks

- [x] 3.1 Write failing paired-mode and adversarial sanitizer tests before
  implementation. Cover every spec-listed role/block/tool field, nested and
  escaped JSON object keys/values, overlapping identifiers and paths, all
  existing credential patterns, high-entropy tokens, suffix-bearing sentinel
  markers, control sequences, invalid UTF-8, malformed payloads, and credentials
  crossing the retained-text boundary.
- [x] 3.2 Refactor `internal/sentinel/rules.go` so its known-pattern and
  high-entropy detection is available through a pure match API returning only
  category and byte/rune spans. Make guard checks, `RedactJSON`, and `RedactText`
  delegate to the same detector and prove their existing live-monitor behavior
  and four-character preview markers remain unchanged.
- [x] 3.3 Implement the export sanitizer in `internal/runexport/sanitize.go`
  with full replacement of sentinel matches and prior sentinel markers, fixed
  replacement tokens, and aggregate counts by fixed category. Never call the
  partial `sentinel.Redact` preview at the export boundary or retain a matched
  suffix in output/counters.
- [x] 3.4 Add a deterministic replacement plan for original run/workflow/step/
  tool IDs, captured source/base/run-root paths, and current home-directory
  prefixes. Sort longer overlaps first; apply token boundaries to identifier
  values and path boundaries to prefixes so common substrings are not globally
  rewritten. Keep replacement maps memory-only.
- [x] 3.5 Parse tool input/output JSON into ordinary values before sanitizing;
  recursively sanitize string keys and values, render the result as stable
  plain text inside a JSON string field, and emit a fixed malformed-payload
  omission marker/counter on any invalid payload. Never insert raw JSON into an
  exported record or echo `json` parser errors.
- [x] 3.6 Remove C0/C1 controls and terminal escape sequences from every
  retained text value except newline/tab, normalize invalid UTF-8 safely, then
  truncate to 64 KiB on a valid UTF-8 boundary with a fixed marker. Ensure
  secret/identifier/path sanitization always runs before truncation and count
  each truncated value without exposing its tail.
- [x] 3.7 Implement bounded streaming transcript projection in
  `internal/runexport/transcript.go`: deterministic step order then file order;
  preserve sequence, relative time, generation/iteration/attempt, validated
  role/block kind, and tool-use/result correlation; allocate tool aliases within
  the exact step/generation/iteration/attempt scope by first occurrence.
- [x] 3.8 Include sanitized text, tool title/input/output, text content, and
  inline diff path/old/new text only. Convert thinking to a content-free marker;
  convert unknown/unsupported blocks and raw variants to fixed markers; never
  copy/dereference locations, attachments, images/binary data, reviews,
  `input.md`, `session.json`, artifacts, or embedded external paths.
- [x] 3.9 Extend manifest records with `content_mode`, redaction-policy version,
  fixed-category replacement totals, thinking/unsupported/malformed omission
  counts, and truncation counts. Keep expected policy omissions separate from
  completeness gaps and never include samples, mappings, matched text, suffixes,
  or hashes of private values.
- [x] 3.10 Add `transcript.jsonl` to the archive only for `--include-text`; emit
  the mandatory best-effort/review-before-sharing warning to stderr and both
  README modes, explicitly distinguishing retained inline tool code from
  excluded attachments. Structural-mode member bytes other than their declared
  mode/notice fields must remain independent of transcript prose.
- [x] 3.11 Add parsed paired-archive tests for useful prose, role/block
  coordinates, scoped tool correlation, deterministic ordering, and counters,
  plus an archive-wide negative scan proving thinking, attachments, private
  seeds, retained secret suffixes, controls, and original mappings are absent.
- [x] 3.12 Capture task 3.0 proof artifacts from structural and text exports of
  the same synthetic run: CLI notices, archive listings, representative
  sanitized transcript records, parsed counters, and automated disclosure-scan
  output. Do not save any raw pre-sanitization fixture payload in the captures.

### [x] 4.0 Partial and damaged-run export with bounded consistency

Extend collection to settled failed/succeeded, interrupted, paused, orphaned,
and corrupt histories without converting uncertainty into authoritative state.
Keep only a journal's valid prefix, continue past malformed transcript lines,
retain unknown events structurally, classify exact fixed gap reasons, and fail
when no safely projectable evidence exists. Complete the lease-held consistency
protocol by rechecking the run, lock, inventory, identity, size, and modification
metadata before publication. Enforce record, total-input, retained-text,
archive-content, and step-inventory limits before allocation; stream large
transcripts; and make cancellation or any unsafe/concurrent change abort cleanly
without a destination.

Covers spec FRs: FR-13, FR-14, FR-16, and the full lifecycle/concurrency portions
of FR-06, FR-12, and FR-15.

#### 4.0 Proof Artifact(s)

- Test: `go test ./internal/runexport -run 'TestDamagedRunExport' -v` passes a
  fixture matrix for succeeded, failed, interrupted, paused, orphaned, corrupt,
  missing `RunStarted`, missing/corrupt workflow snapshot, torn journal tail,
  malformed transcript lines, unknown events, and no usable evidence —
  demonstrates safe eligibility and non-authoritative partial state.
- Test: `go test ./internal/runexport -run 'TestCompletenessManifest' -v`
  parses README/manifest and verifies `complete` versus `partial`,
  exact fixed gap codes/source categories/aliases/line counts, nullable unknown
  state and totals, and separation of privacy omissions from damaged evidence —
  demonstrates FR-14 without copying source diagnostics.
- Test: `go test ./internal/runexport -run 'TestExportContention|TestSourceChangeAbort' -v`
  uses separate-process contention and source-change injection for a live or
  parked scheduler, concurrent Resume, append/replacement/deletion/new-step
  evidence, lock replacement, and run-root removal; every detected change aborts
  before publication and leaves source evidence unchanged — demonstrates the
  lease and consistency guarantees.
- Test: `go test ./internal/runexport -run 'TestConfinedSources' -v` covers
  symlink, traversal, FIFO/device/special-file, embedded-path, and
  permission-error fixtures prove only selected regular in-root evidence is
  opened and that confinement failures are fatal rather than partial.
- Test: `go test ./internal/runexport -run 'TestResourceBounds' -v` covers a
  4 MiB record edge, 256 MiB input/archive edges, 10,000-step edge, 64 KiB text
  edge, oversized journal/transcript distinctions, and cancellation; a large
  streamed transcript test records bounded memory growth — demonstrates all
  resource limits without an override path.

#### 4.0 Tasks

- [x] 4.1 Add a table-driven damaged-run matrix before implementation covering
  settled success/failure, interrupted worker, stopped/paused and gate parks,
  orphaned history, missing/corrupt workflow snapshot, missing `RunStarted`,
  torn journal tail, complete malformed journal line, unknown event kind,
  malformed transcript lines, missing expected transcript, and zero usable
  journal/transcript records.
- [x] 4.2 Make journal corruption handling preserve only the accepted prefix:
  omit and report a torn final record, stop and report at the first malformed
  complete/oversized record, retain unknown kinds as fixed `unknown` events,
  and prevent any later record from affecting aliases, state, or totals.
- [x] 4.3 Make transcript corruption handling skip malformed complete and torn
  lines independently and continue with later well-formed records, recording
  fixed step alias/line/count gaps. Structural mode may inspect record validity
  and counts but must never export transcript text or tool metadata.
- [x] 4.4 Derive expected transcript sources only from accepted `StepMessage`
  events or accepted running/terminal transitions for captured agent/command
  steps, excluding pending/skipped/review/fan-out-family barriers. Inspect a
  discovered direct transcript even when its step lacks captured metadata;
  classify absence-before-collection as a gap and disappearance-after-inventory
  as fatal concurrent change.
- [x] 4.5 Implement explicit fixed completeness reason codes for missing/corrupt
  expected sources, unsupported semantics, torn/oversized records, invalid
  structural fields, and export truncation. Include only source category,
  optional alias, and numeric line/count data; permission/read/confinement
  failures remain fatal rather than partial.
- [x] 4.6 Mark state/totals authoritative only when the accepted journal and
  required semantics support the claim. A missing/corrupt workflow leaves
  backend/transport/dependencies unknown; transcript-only evidence has unknown
  state/totals; no usable evidence returns operational failure without an
  archive; no recovery/display-only synthetic event enters output.
- [x] 4.7 Render partial evidence prominently in generated README and
  `manifest.json`, qualifying any prefix-derived state/totals. Add exact parsed
  assertions that intentional privacy omissions do not create partial gaps and
  optional-by-contract missing files are not reported.
- [x] 4.8 Complete the final source consistency recheck while the lease remains
  held: verify run-root and lock identity, selected-file presence/type/identity/
  size/mtime, direct-step inventory, and absence of newly selected evidence.
  Abort without publication on detectable append, replacement, deletion,
  new-step evidence, lock replacement, or root removal.
- [x] 4.9 Add helper-process and injected-hook tests that race export against a
  live scheduler, concurrent Resume, journal/transcript append, atomic file
  replacement, deletion, permission change, new step transcript, lock
  replacement, and run removal. Assert fixed diagnostics, cleanup, immediate
  lease reusability, and unchanged source payload bytes.
- [x] 4.10 Add confinement tests for symlinked selected files/directories,
  traversal-shaped direct entry names, FIFOs/devices/sockets where supported,
  paths embedded inside journal/transcript data, and unreadable selected files.
  Prove no recursive artifact/review/session/input/output/fan-out-manifest file
  is opened, including when it is a blocking FIFO or points outside the run.
- [x] 4.11 Enforce the 4 MiB input-record limit before allocating a full record,
  256 MiB cumulative selected-input/metadata-read limit, 10,000-step inventory
  limit, 64 KiB retained text limit after sanitization, and 256 MiB total
  uncompressed archive-member limit. Use checked integer arithmetic and expose
  no flag or environment override.
- [x] 4.12 Add exact below/at/above-bound tests for each limit. Verify an
  oversized journal record ends the prefix as partial, an oversized transcript
  line is skipped as a gap, while total input/archive or inventory overflow is
  fatal and publishes nothing. Include multibyte UTF-8 boundaries and a secret
  spanning the text truncation point.
- [x] 4.13 Add a large streamed-transcript regression test and benchmark with an
  instrumented reader/spool that records maximum read request and retained
  in-memory bytes. Capture `go test`/`go test -bench ... -benchmem` evidence that
  increasing on-disk transcript size does not cause proportional retained
  memory growth or whole-archive buffering.
- [x] 4.14 Check `context.Context` before/after each selected record, member
  spool, ZIP copy, consistency recheck, and publication. Add SIGINT/SIGTERM
  helper-process CLI tests asserting exits 130/143, no success stdout, fixed
  stderr, no destination, private-temp cleanup, and source preservation.
- [x] 4.15 Run targeted package and CLI tests with `-race -count=1` and capture
  the damaged-state matrix, parsed partial manifests/READMEs, contention/change
  refusals, confinement matrix, limit edges, cancellation cleanup, and streaming
  benchmark under the task 4.0 artifact directory.

### [ ] 5.0 Publish the export contract and close offline acceptance

Document the command and versioned archive as a stable recipient-facing
contract, link it from the README, and verify every parent slice together with
the repository's offline quality gates. Documentation must name selected and
excluded sources, exact limits, mode/privacy warnings, completeness semantics,
states, stream/exit behavior, cancellation, and the no-network/no-backend
boundary. Only after the implementation and acceptance evidence pass, mark A22
done in the backlog. No workflow schema, durable transcript migration, TUI,
viewer, hosted sharing, or backend behavior enters this task.

Covers spec FR: FR-17, and provides final regression evidence for FR-01 through
FR-16.

#### 5.0 Proof Artifact(s)

- Diff: `docs/operations.md` (or a linked export-contract document) defines
  every archive member/field, source-selection rule, bound, mode, state,
  completeness/gap behavior, privacy limitation, stream, exit, publication,
  cancellation, and local-only guarantee; `README.md` links it — demonstrates
  the complete FR-17 documentation contract.
- Test: `go test ./cmd/jig -run 'TestExportEndToEnd' -v` builds/drives the CLI,
  exports
  intact structural/text and damaged partial bundles, inspects them only with
  standard ZIP/JSON tools, scans all output surfaces for synthetic private
  values, and verifies source hashes are unchanged — demonstrates the three
  demoable units together.
- CLI: `go test ./internal/runexport ./cmd/jig -race -count=1` passes —
  demonstrates concurrency-sensitive export/lease behavior under the race
  detector with no model, credentials, or network.
- CLI: `go test ./... -count=1`, `go vet ./...`, and `gofmt -l .` all pass, and
  every `.agents/jig/*.toml` workflow validates — demonstrates repository-wide
  regression, format, vet, and executable-example compliance.
- Diff: `docs/plans/open-goals.md` changes A22 to done only after all preceding
  proof artifacts pass — demonstrates backlog closure is evidence-gated.

#### 5.0 Tasks

- [ ] 5.1 Write the recipient/operator export contract in
  `docs/operations.md` or a linked dedicated document: exact command grammar,
  mode warnings, eligible/refused states, selected and excluded sources,
  archive/member/field versions, aliases/times, state authority, gap codes,
  all limits, publication/cancellation, streams/exits, and local-only/no-backend
  behavior. State explicitly that neither mode guarantees anonymity.
- [ ] 5.2 Update `README.md` to make `jig export` discoverable and link the full
  contract; update `docs/TESTING.md` to replace stale package coverage claims
  with the current offline, helper-process, archive-parsing, disclosure-scan,
  and race-test conventions.
- [ ] 5.3 Add one deterministic end-to-end acceptance test that builds or drives
  the real CLI entry against temporary intact and damaged run stores, creates
  structural and text bundles, inspects them with standard ZIP/JSON readers,
  checks exact stdout/stderr/exits, and verifies before/after source hashes. It
  must use no live workflow validation, model, backend, credential, or network.
- [ ] 5.4 Add a requirements assertion or compact test-data matrix mapping every
  FR-01 through FR-17 to at least one executable test/proof name, so deleting a
  planned acceptance case cannot silently leave the documentation-only coverage
  table as the sole evidence.
- [ ] 5.5 Run and save sanitized outputs for `go test ./internal/runexport ./cmd/jig -race -count=1`,
  `go test ./... -count=1`, `go vet ./...`, and `gofmt -l .` (which must print
  nothing). Fix only regressions caused by this feature; do not alter unrelated
  user work.
- [ ] 5.6 Validate every `.agents/jig/*.toml` with `go run ./cmd/jig validate`
  and save the successful output, confirming the CLI addition did not change
  workflow schema/defaulting or require example migrations.
- [ ] 5.7 Re-run the three demo captures using only synthetic fixtures and save
  an artifact index that points to help/structural/text/partial outputs,
  disclosure scans, lock/confinement/bounds/cancellation results, source hashes,
  and repository quality gates. Verify no artifact is a raw run archive or
  contains a private fixture seed.
- [ ] 5.8 Only after 5.3–5.7 pass, update A22 in
  `docs/plans/open-goals.md` to **Done** with links to this spec, task list,
  proofs, and validation path. Leave all hosted sharing, TUI/viewer/import,
  raw backup, live snapshots, custom redaction, persistence-schema, and backend
  changes explicitly deferred.
