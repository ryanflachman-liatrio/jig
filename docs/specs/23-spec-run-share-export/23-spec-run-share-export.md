# 23-spec-run-share-export.md

## Introduction/Overview

A22 in `docs/plans/open-goals.md` asks for run sharing through an anonymized export bundle. Operators currently inspect local `.jig/runs` records with `jig status` and `jig logs`, but those records contain original identifiers and potentially sensitive free text. This feature adds a local CLI export that packages selected diagnostic evidence into an ordinary ZIP archive, with structural information by default and sanitized conversation text only by explicit request.

The user accepted recommendations 1(A), 2(B), and 3(B) in `23-questions-1-run-share-export.md` on 2026-09-10. This specification resolves command spelling, archive layout, bounds, and synchronization as implementation choices within those accepted outcomes. It covers one feature with three demoable units. Clarification status: sufficient - no questions file required. No blocking questions remain.

## Goals

- Produce a self-contained archive a recipient can inspect without jig, a backend login, the original checkout, or a network connection.
- Make the default export's disclosure policy a closed, testable set of structural fields with consistently replaced identifiers.
- Offer useful conversation diagnostics through explicit opt-in, with documented best-effort sanitization and no complete-anonymity claim.
- Support inactive settled, paused, interrupted, and damaged runs while preventing concurrent scheduler changes during collection.
- Preserve source evidence and report missing, unsupported, malformed, or omitted data explicitly.

## User Stories

- **As an operator reporting a workflow failure**, I want to share statuses, transitions, retry history, and execution totals so that a maintainer can diagnose orchestration behavior without receiving my prompts or code by default.
- **As an operator investigating an agent interaction**, I want to explicitly include sanitized conversation and tool text so that a teammate can understand the interaction after I review the bundle for remaining sensitive information.
- **As a maintainer receiving a crash report**, I want a readable summary and structured evidence with clear gaps so that I can distinguish observed events from unavailable history.
- **As an operator preserving a run for further work**, I want export to leave its evidence unchanged so that sharing does not alter later inspection or resume behavior.

## Demoable Units of Work

### Unit 1: Export structural diagnostics from an inactive run

**Purpose:** Deliver the default end-to-end CLI and recipient experience for intact, inactive runs.

**Functional Requirements:**

- **FR-01 — CLI contract.** The system shall expose `jig export RUN_ID --destination PATH [--root PATH] [--include-text]`. `--root` defaults to `.jig`; `--destination` is required and names a ZIP file, regardless of filename suffix. Exactly one run identifier is accepted. Help documents both content modes, supported run states, and the local-only operation. Arguments follow existing ops conventions, including flags after the run identifier. There is no prompt, implicit upload, overwrite flag, or raw-content mode.
- **FR-02 — Target resolution.** The system shall resolve an existing run using the existing identifier rules: reject empty persistence roots, path-shaped run IDs, missing runs, non-directories, and symlink run targets. It shall not create a run directory. The destination's parent must already exist, and the destination must be outside the selected persistence root after resolving parent directories. Existing destination entries, including dangling symlinks, shall be rejected without alteration.
- **FR-03 — Structural archive.** The system shall produce the members and field projection described below, containing one run's summary, current observed step states, and ordered event timeline. It shall preserve attempts, iterations, generations, route/reset relationships, and dynamic fan-out parent/index/total relationships when present in accepted source records. Missing facts shall remain unknown; the export shall not invent terminal events or imply an interrupted run completed.
- **FR-04 — Closed disclosure policy.** The system shall construct dedicated export records from allowed fields. Default output shall exclude arbitrary prose, original names and identifiers, paths, session IDs, workflow source, prompts, tool payloads, code, artifact contents, Git identifiers, and hashes of original content. Unknown fields shall never be copied through. Enum-like source strings shall be validated against explicit known values and replaced with `unknown` when unsupported.
- **FR-05 — Identity and time.** The system shall use `run-1` and `workflow-1`, generated step aliases (`step-0001`, etc.), and scoped tool-call aliases throughout the archive. Step aliases shall be allocated in `RunStarted.Steps` order, then first journal occurrence for additional steps, then sorted direct step-directory discovery for remaining evidence. Tool aliases shall be scoped by step/generation/iteration/attempt and allocated by first transcript occurrence. Original-to-alias maps remain in memory and shall not be exported or hashed into identifiers. All evidence times shall be relative integer milliseconds from the first valid accepted journal timestamp, or the earliest valid retained transcript timestamp if the journal has none; absent times are null. Signed offsets preserve evidence of clock reversal. No original absolute run dates or current export date shall be added to content or archive headers; ZIP timestamps use a fixed format-neutral value.
- **FR-06 — Publication and exits.** The system shall write a private temporary archive in the destination directory and publish the completed file without overwriting a competing destination. On success stdout contains only the supplied destination path and a newline. Notices/errors use stderr; they shall use fixed messages, source categories, aliases, and line numbers rather than raw source values or parser errors. Exit codes are 0 for a complete or explicitly partial usable archive, 1 for operational failure, 2 for usage errors, and 130/143 for SIGINT/SIGTERM. Failed/cancelled exports shall not publish a destination and shall remove their own temporary files; abrupt process death may leave only a private temporary file. The destination and temporary file shall be owner-only on supported Unix platforms.

**Proof Artifacts:**

- CLI capture of help, structural export, archive listing, and extracted `README.md`, using a synthetic run with a retry, reset, route, and fan-out children, demonstrates the operator and recipient flows.
- Parsed archive assertions demonstrate exact fields, consistent aliases/references, relative times, totals, and exclusion of synthetic private strings from every member and ZIP header.
- Before/after source hashes and negative CLI cases demonstrate source preservation, destination collision behavior, identifier validation, and exit/stream contracts.

### Unit 2: Include sanitized conversation text explicitly

**Purpose:** Add conversation evidence for cases where structural diagnostics do not explain agent behavior.

**Functional Requirements:**

- **FR-07 — Explicit content mode.** `--include-text` shall add the transcript member described below and set `content_mode` to `sanitized_text`; default mode is `structural`. Both the archive README and stderr shall state that text is best-effort redacted, may still identify people/projects or contain sensitive content, and must be reviewed before sharing. The flag itself is sufficient opt-in; no interactive confirmation is added.
- **FR-08 — Included and excluded text.** Text mode shall include sanitized user, assistant, system/command, and result prose, plus normalized tool title, input/output text, text content, and inline diff text already present in a transcript. It shall retain role, block type, sequence, coordinates, and tool-use/result correlation. Thinking blocks shall become content-free omission markers. Attachments, image/binary content, unknown raw content variants, external locations, review document snapshots, `input.md`, `session.json`, and artifact files shall never be copied or dereferenced. Inline code in opted-in tool payloads is text and may remain after sanitization; this is explicitly distinguished from attachment export in help and README.
- **FR-09 — Sanitization.** Every included free-text value shall pass through the same deterministic export sanitizer before it reaches any file or diagnostic. It shall fully replace recognized secret patterns and high-entropy tokens supported by the existing sentinel detectors, including the retained suffix of existing sentinel redaction markers. It shall replace known original run/workflow/step/tool identifiers, captured source/base/run-root paths, and current home-directory prefixes with consistent aliases or fixed markers. Matching shall process longer overlapping values first. Identifier replacements shall use token boundaries where appropriate to avoid replacing common substrings throughout prose; path prefixes shall use path boundaries. Escaped JSON string content shall be decoded before matching, including nested object keys and string values. Invalid payloads shall be omitted with a fixed marker rather than passed through as raw JSON.
- **FR-10 — Omission and safe representation.** Tool input/output shall be represented as sanitized plain text derived from parsed JSON; the exported field shall be a JSON string, not a raw JSON insertion. Inline diff paths and old/new text receive the same sanitizer. Unsupported blocks retain only a fixed `unknown` type, coordinates, and omission reason. All structural exclusions continue to apply outside the explicit text fields. C0/C1 terminal controls and escape sequences shall be removed from included text except newline/tab; generated Markdown shall contain no interpolated source text. Sanitization shall precede output truncation so a cut-off credential is never emitted as a previously unrecognized prefix.
- **FR-11 — Privacy accounting.** The manifest shall report the content mode, redaction policy version, aggregate replacement counts by fixed category, omitted thinking/unsupported/malformed content counts, and truncation counts. No report shall include matched text, retained secret suffixes, original identifier mappings, or hashes of private values. Policy exclusions are expected and distinct from damaged or incomplete evidence.

**Proof Artifacts:**

- Paired structural/text exports from the same synthetic fixture demonstrate opt-in behavior, useful prose and tool correlation, and invariant thinking/attachment exclusions.
- Adversarial archive assertions demonstrate sanitization of nested/escaped payloads, object keys, titles, code/diffs, local paths, original identifiers, recognized credentials, and prior suffix-bearing redaction markers. Fixtures include secrets crossing output truncation boundaries and control sequences.
- Captured CLI and README notices plus parsed manifest counters demonstrate the privacy promise and omission accounting. All secrets used in proofs are synthetic.

### Unit 3: Export damaged history safely and consistently

**Purpose:** Support crash diagnostics without treating partial evidence as a complete run or allowing live scheduler changes during collection.

**Functional Requirements:**

- **FR-12 — Ownership lease.** Before reading evidence, the exporter shall acquire and hold a non-blocking exclusive lease on the same `scheduler.lock` inode/protocol used by Start/Resume, through publication or abort. A live owner, including a scheduler parked at a gate with no worker running, causes immediate failure. Lock unavailability must never be treated as inactivity. An absent lock file may be created solely for coordination; it is never truncated, removed, copied, or treated as evidence. No run payload file is modified. Export shall not instantiate a scheduler, resume a session, append events, run workflow validation commands, or invoke Git/backends.
- **FR-13 — Inactive and damaged states.** Settled succeeded/failed, interrupted, paused, orphaned, and corrupt runs are eligible if ownership is free and at least one well-formed journal or transcript record can be safely projected. Journal decoding shall preserve only the valid prefix before the first malformed complete record; a torn final record is omitted and reported. Transcript decoding may skip malformed lines and continue, recording the gaps. Unknown event kinds produce a fixed `unknown` event with sequence/time only. Unknown semantics, missing `RunStarted`, and corrupt history shall prevent presenting derived state as authoritative. A run with no usable evidence fails without an archive.
- **FR-14 — Completeness.** `manifest.json` shall distinguish `complete` from `partial` evidence, with fixed reason codes, source category, optional step alias, and numeric line/count information. Missing/corrupt expected files, unknown semantics, torn records, and export truncation produce `partial`; intentional privacy omissions do not. Optional files absent by contract do not cause a gap. `README.md` prominently identifies partial evidence and totals/state derived from a journal prefix as partial. A missing/corrupt workflow snapshot shall not prevent export of usable journal/transcript records; backend/transport and dependency data then remain unknown. No recovery/display-only synthetic failure is exported.
- **FR-15 — Confinement and concurrent changes.** The exporter shall read only explicitly selected regular files beneath the resolved run root using traversal-resistant opens. It shall reject symlinks and special files encountered at selected evidence paths, avoid recursive artifact walks, and never follow paths embedded in persisted data. During collection it shall record and recheck source inventory, file identity, size, and modification time, including lock identity and root existence. Detectable replacement, append, deletion, new step evidence, or lock replacement shall abort without publication. The lease is the consistency guarantee against cooperating jig schedulers; hostile processes ignoring locks and restoring file metadata are outside that guarantee.
- **FR-16 — Resource bounds.** Collection shall stream JSONL with a maximum 4 MiB input record and 256 MiB total bytes read across evidence and metadata discovery. An oversized journal record ends the usable prefix; an oversized transcript record is skipped with a gap. Crossing the total input budget aborts with an operational error. Retained text values are capped at 64 KiB UTF-8 after sanitization, with explicit truncation markers/counters. Total uncompressed archive content is capped at 256 MiB and inventory at 10,000 steps; exceeding either aborts. The implementation shall not load the whole archive or all transcript contents into memory. There are no unbounded override flags in this version.
- **FR-17 — Documentation and verification.** The implementation shall document the CLI, versioned archive fields, source selection, limits, state/completeness behavior, and sanitization limits in `docs/operations.md` or a linked export contract, with README discovery. Tests shall use temporary synthetic run stores and separate-process ownership checks, exercise each failure path above, and require no live model, credentials, or network access. The A22 backlog entry becomes done only after implementation and verification, not when this specification is written.

**Proof Artifacts:**

- CLI captures and parsed archives for inactive failed, interrupted, paused, orphaned, and corrupt fixtures demonstrate usable partial evidence, unknown values, and clear gaps.
- Separate-process scheduler/export lock contention tests and source-change injection demonstrate refusal of live runs and concurrent Resume, cleanup on abort, and preservation of source evidence.
- Symlink/traversal/special-file, missing-source, collision, cancellation, oversized-record, and total-budget tests demonstrate confinement and bounded failure behavior. A large streamed transcript fixture demonstrates that memory use does not grow with transcript content retained on disk.

## Non-Goals (Out of Scope)

1. **Hosted sharing or notifications:** no upload, URL service, email, forge integration, or external messaging.
2. **TUI export or bundle viewer:** recipients use ordinary ZIP, Markdown, and JSON tools; no bundle import, execution, resume, replay UI, or original-store reconstruction.
3. **Raw or complete archive backup:** no attachments, repository files, artifacts, session credentials, thinking text, or private unfiltered mode.
4. **Live-run snapshots:** an active scheduler must release ownership before export, even when its workers are quiescent.
5. **Universal anonymization:** no claim to remove every secret, personal identifier, proprietary fact, or inference from structure/timing. No LLM sanitizer, external scanning service, encryption/key-management system, or custom redaction-rule UI.
6. **Persistence/schema overhaul:** no changes to workflow TOML or durable transcript format solely to support export; no broad retention rewrite or backend behavior change.

## Design Considerations

The CLI is the only author surface. Example commands:

```sh
jig export RUN_ID --destination ./run-report.zip
jig export RUN_ID --root /path/to/.jig --destination ./run-with-text.zip --include-text
unzip -l ./run-report.zip
unzip -p ./run-report.zip README.md
```

The caller chooses a destination name; jig does not derive it from the original run or project name. The README presents mode, completeness, run state, step table, totals, evidence layout, omission summary, and recipient instructions using generated labels only. Text-mode prose lives in JSONL so rendering README does not render untrusted model/tool Markdown or activate its links.

Archive layout is fixed:

| Member | Contract |
|---|---|
| `README.md` | Generated readable summary and privacy/completeness notices; no copied prose. |
| `manifest.json` | `format_version: 1`, `redaction_policy_version: 1`, mode, completeness, gaps, counters, and member byte lengths/SHA-256 digests. Digests cover exported members only and exclude the manifest itself. |
| `run.json` | Alias identities, observed state and whether it is authoritative, relative times, nullable totals, and ordered step records with aliases, type/backend/transport, status, coordinates, optional parent/index/total and dependency aliases. |
| `events.jsonl` | Journal-order records with sequence, relative time, known kind or `unknown`, and the structural event projection below. |
| `transcript.jsonl` | Present only in text mode: deterministic step order then per-file record order; alias step, sequence, relative time, coordinates, validated role, projected blocks, and omission/truncation markers. |

All members are regular files at these literal names, with fixed metadata dates, no comments or source-derived ZIP extra fields. Member order is the table order. Identical evidence, exporter version, and options produce the same extracted member bytes and names; byte-identical compressed archives across Go versions are not required. Original IDs never appear in member names.

The structural event projection is a closed mapping, not a recursive scrub of arbitrary events:

| Source event family | Allowed payload beyond sequence, time, and kind |
|---|---|
| Start/finish | Aliased declared steps; failed boolean. |
| Step status | Step alias; validated from/to status; attempt/iteration/generation; nullable finite cost and token count. Free-form error/subtype text is omitted. |
| Route/reset | Step/target/closure aliases; route index, iteration, and cap. Conditions and Git SHAs are omitted. |
| Fan-out expansion | Family/child aliases, generation/iteration, index, total. Source refs, item values, and digests are omitted. |
| Gate/review/question/input/recovery/integration/security events | Step alias when present; gate passed boolean or review comment count when present. Reasons, choices, labels, question IDs, findings, document descriptors, and details are omitted. |
| Step message | Step alias, referenced transcript sequence and iteration. |
| Legacy output/tool-call, run error, other known events | Kind and step alias when present; no source payload text. |
| Unknown kind | Sequence, relative time, fixed `unknown` kind only; mark unsupported semantics. |

Missing numeric values remain null when absence is distinguishable in storage; values already persisted as zero retain that limitation. Invalid/non-finite/negative counters or costs are omitted and reported as invalid fields. Status folds use the same semantics as ops on the accepted journal prefix. Complete total claims require sufficient journal evidence; transcript-only exports have unknown state and totals.

## Repository Standards

- Follow `AGENTS.md`, `CLAUDE.md`, and `CONTEXT.md`: deterministic orchestration, correct domain terms, focused internal packages, no SDK/harness imports in the engine, and TOML-only backend selection.
- Keep CLI parsing/stream formatting in `cmd/jig`; put collection, projections, sanitization, and archive construction behind a focused internal package. Preserve existing ops semantics and avoid adding export logic to the TUI.
- Use table-driven fixture tests and `t.TempDir`; compare observable archive contents, CLI behavior, and unchanged source bytes. Existing ops/engine tests are authoritative where `docs/TESTING.md` coverage descriptions are stale.
- Keep persistence-off writers no-op; explicit export with an empty root returns a clear error instead of enabling persistence.
- Use the pinned Go 1.25.12 toolchain, gofmt, targeted tests, `go test ./...`, and `go vet ./...`; run race checks for any shared lock/lifecycle changes and validate workflow examples as required by repository checks. No new vendor runtime or model invocation is needed.

## Technical Considerations

- Relevant seams are `internal/datastore.ResolveRunDir`, `internal/ops.FoldStatus`, `internal/engine` journal envelopes and ownership locking, `internal/transcript` normalization, and `internal/toolcall.Activity`. `ReplayJournal` is unsuitable because it synthesizes display events and hydrates review files. Existing replay helpers also omit some corruption/torn-tail detail and use path-based opens; reuse decoding/folding behavior behind confined readers rather than assuming existing high-level readers satisfy the export contract.
- Factor a narrow lock lease shared with Start/Resume if needed. `RunLockState` is a short-lived probe and does not satisfy FR-12. While holding its own lease, export derives status using the pre-export free-ownership fact instead of probing its own descriptor and misclassifying itself as active. Creating an absent empty scheduler lock is the sole allowed run-store mutation; byte contents and membership of payload files remain unchanged, although ordinary reads may update access times.
- Select the journal, captured `workflow.json`, and direct per-step transcripts. The captured workflow is read only for structural fields and known identifiers; never load current author files or resolve referenced modules/files from disk. Validate captured checksums before using its metadata; missing or invalid metadata becomes a reported gap. Backend/transport come from resolved captured values; unknown values remain unknown. Discover direct step directories without recursively reading artifacts, fan-out item manifests, reviews, or sessions. Journal fan-out descriptors provide child relationships without exposing item contents.
- Expected sources for gap reporting are the journal and workflow snapshot, plus transcripts advertised by `StepMessage` or by an accepted running/terminal transition for a captured agent/command step (excluding skipped steps and fan-out family barriers). Missing transcripts for pending, skipped, review, or undispatched steps are not gaps. A discovered transcript is inspected even if its step is absent from captured metadata. Read errors on selected evidence are fatal; missing files present before collection begins can produce the partial outcome above, while disappearance during collection is a concurrent-change failure. Never suppress permission errors as ordinary missing evidence.
- Structural mode may inspect transcript records for evidence availability and damage counts, but exports no transcript text or tool metadata. Additional metadata reads for identifier replacement in text mode must stay within the selected source set. Omitted session records are never opened merely to improve filtering; unrecognized session-like text remains part of the stated residual risk.
- Export serialization uses dedicated records with explicit fields; do not marshal `workflow.Workflow`, `step.Result`, `engine.Event`, or `toolcall.Activity` directly. Reuse secret detectors through an export-specific full-replacement function; the existing sentinel helper retains four characters and is insufficient as-is. Keep the existing live-monitor policy unchanged unless a shared pure detector extraction is needed.
- Enforce bounds before allocation. Archive construction can stream sanitized members into private temporary storage to obtain lengths/digests before manifest creation. No unredacted source copy, mapping file, or raw staging archive may be written. Unknown fields and parser errors are data, not trusted diagnostics.
- The archive has its own explicit version; it does not promise compatibility for jig's currently unversioned local transcript store or require compatibility shims. The first implementation documents each exported field and tests the closed projection.

Current external guidance reviewed for this spec:

| Technology/source | Recency | Applied guidance |
|---|---|---|
| [OWASP Logging Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html) | Living guidance consulted 2026-09-10 | Select necessary evidence and exclude/transform sensitive fields during extraction. The accepted text mode trades retained diagnostic context for an explicit best-effort guarantee. |
| [Go traversal-resistant file APIs](https://go.dev/blog/osroot) | Official article, 2025-03-12; consulted 2026-09-10 | Use rooted opens to prevent relative-path and symlink escapes; simple check-then-open path validation is insufficient against races. |
| [Go archive/zip documentation](https://pkg.go.dev/archive/zip) and [os.Root documentation](https://pkg.go.dev/os#Root) | Living reference consulted 2026-09-10 | Standard ZIP supports ordinary extraction; construct controlled headers/member names and close writers before publication. Use APIs available in the pinned toolchain, even when the living docs show newer additions. |

No dependency or toolchain upgrade is required. Existing local transcript retention and partial-redaction behavior remains valid for local inspection; export adds a distinct stricter boundary for records leaving that context.

## Security Considerations

The default policy excludes free text by construction. Structural aliases, relative timing, costs, step counts, and event patterns may still permit inference or correlation; the product must not call the result guaranteed anonymous. Text mode reduces recognizable secrets and identifiers but can retain names, proprietary code, domain facts, unfamiliar credentials, and transformed/split secrets that the detectors cannot recognize.

Every output surface is in the disclosure boundary: archive member names/headers, README, JSON keys and values, counters, gap descriptions, stderr, and temporary files. Only the caller-supplied destination path is echoed on stdout; no raw evidence is added to diagnostics. Source files and configured artifact references are untrusted. Rooted reads must be combined with regular-file and symlink policy; rooted APIs alone do not reject every special-file or in-root symlink case.

The lock prevents cooperating jig schedulers from mutating the run while evidence is read. File inventory/identity checks catch observable external changes, including retention deletion; they do not provide a filesystem snapshot against a malicious local process. Failure to obtain safe ownership or confinement is fatal and must not become a partial archive. Published content contains no resume authority, and no archive operation contacts a backend or remote service.

All proof artifacts use synthetic text and credentials. Tests scan extracted content and archive headers, verify exact retained fields and excluded categories, and exercise privacy failures; a successful regex scan alone is not proof of anonymity.

## Success Metrics

1. **Usable handoff:** synthetic intact and damaged-run exports can be listed/extracted with ordinary tools, and the README plus JSON identify observed state, transitions, and any evidence gaps without the source checkout.
2. **Default exclusion:** all seeded private identifiers, prose, paths, credentials, code, payloads, and private digests are absent from every default archive member/header; structural reference integrity and values pass assertions.
3. **Opt-in sanitization:** every supported detector/known-identifier fixture is fully replaced in text mode, including nested/escaped fields and prior redaction suffixes; thinking and attachments remain absent.
4. **Consistency and preservation:** active-scheduler, concurrent-resume, source-change, destination-collision, cancellation, and confinement tests publish no failed archive and preserve payload bytes. Lock creation is the documented coordination-only exception.
5. **Bounded operation:** oversized records and total/inventory limits produce the specified partial/failure outcomes without unbounded reads or allocation; required offline repository checks pass.

## Open Questions

No blocking or non-blocking questions remain. The accepted scope and the implementation choices above are the planning baseline. Future viewer/TUI support, configurable bounds, hosted sharing, and custom sanitization policies are separate features.
