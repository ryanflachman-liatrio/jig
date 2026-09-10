# 23 Questions Round 1 - Run Share / Anonymized Export Bundle

Please answer each question below (select one or more options, or add your own notes). Feel free to add additional context under any question.

**Resolved 2026-09-10:** The user instructed, “Continue with your recommendations for each question.” Accepted answers: 1(A), 2(B), and 3(B), including the recommended exclusion of thinking and attached files/artifacts. The selections below record that approval.

## Context from A22 and repository inspection

- Source: `docs/plans/open-goals.md`, A22, “Run share / anonymized export bundle.” The backlog calls this a never-fully-scoped outcome; its only constraint is today's local `.jig/runs` storage.
- Selected SDD phase: Phase 1 — Spec Generation. No existing spec matches A22. Spec 22 is the unrelated `jig init` scaffold; this feature uses sequence 23.
- Scope assessment: a local export with a defined content policy and recipient format fits one spec. Hosted sharing or executable run transfer would require reassessing scope.
- `cmd/jig/ops.go`, `internal/ops/status.go`, and `docs/operations.md` provide CLI and persisted status conventions, including active, interrupted, paused, succeeded, failed, orphaned, and corrupt runs.
- `internal/datastore/datastore.go` resolves existing run identifiers without creating directories. Run storage includes journals, prompts, results, transcripts, artifacts, and session records; export must select content deliberately.
- `internal/transcript/transcript.go` persists prose, thinking, and structured tool activity. `internal/sentinel/rules.go` provides pattern-based secret filters, but arbitrary text and identifiers cannot be assumed anonymous. `docs/operations.md` explicitly disclaims retrospective transcript redaction.
- Repository constraints: Go 1.25, focused internal packages, offline fixture tests, persistence-off support, and no backend selection through environment variables. Existing code takes precedence over stale coverage descriptions in `docs/TESTING.md`.

## 1. Sharing workflow and recipient experience

What should an operator produce, and how should the recipient inspect it in the first version?

- [x] (A) A local CLI export containing a readable Markdown summary and structured JSON/JSONL in one archive; the recipient extracts it with ordinary tools and needs no jig installation.
- [ ] (B) A local CLI export plus a dedicated read-only jig viewer for the received bundle.
- [ ] (C) Export from both the CLI and TUI, using the ordinary-tool archive experience in (A).
- [ ] (D) Hosted sharing with an uploaded bundle and shareable URL.
- [ ] (E) Other (describe).

**Recommended answer(s):** (A).

**Why these are recommended:**

- A gives operators a complete way to attach evidence to an issue or send it to a teammate and fits the existing ops CLI. Archive mechanics and command spelling can be resolved in the spec.
- B adds a reader and compatibility contract; C adds UI integration. Both are useful extensions if required for the initial outcome.
- D adds hosting, access control, retention, and external writes, substantially expanding this feature.

## 2. Exported content and meaning of “anonymized”

Which information must the recipient see, and what privacy promise should the export make?

- [ ] (A) Structural diagnostics only: statuses, event kinds, relative timing, attempts/iterations/generations, cost/token totals, backend/transport, and relationships under generated identifiers. Exclude arbitrary prose, original names/paths/session IDs, source code, prompts, tool payloads, and artifacts.
- [x] (B) The structural export in (A) by default, with an explicit option to include sanitized conversation/tool text. The optional text is best-effort redacted and must be reviewed before sharing; it carries no guarantee of complete anonymity.
- [ ] (C) Sanitized conversation/tool text by default because understanding what the agent did is the primary purpose; accept best-effort redaction and operator review, with no complete-anonymity guarantee.
- [ ] (D) A faithful private handoff including original transcript, workflow, and artifacts; anonymization is an optional mode.
- [ ] (E) Other (describe the must-have content and required exclusions).

**Current best-practice context:** The living [OWASP Logging Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html), consulted 2026-09-10, recommends excluding or transforming secrets, session identifiers, sensitive personal data, and source code, including during extraction. This supports deliberate field selection; it does not make pattern matching a proof of anonymity.

**Recommended answer(s):** (B).

**Why these are recommended:**

- B supports both structural bug reports and conversation debugging while making content inclusion an explicit operator choice. The default would have a testable field-exclusion contract; even structural metadata would not claim mathematical anonymity.
- A has the narrowest disclosure surface but cannot explain failures that depend on prompt or tool contents. C preserves that context immediately but makes every export require careful content review.
- D serves private transfer well but makes the default bundle contain the material the backlog's “anonymized” wording suggests removing.
- If choosing B or C, identify any additional text that must always be excluded, such as thinking, file diffs, or user messages. Recommended baseline: always omit thinking and attached files/artifacts; sanitize included messages and tool payloads, replace known local identifiers consistently, and explicitly document residual disclosure risk.

## 3. Run lifecycle and incomplete evidence

Which run states must the first version support?

- [ ] (A) Settled succeeded/failed runs only; reject unfinished or corrupt histories.
- [x] (B) Runs with no active scheduler, including settled, interrupted, and paused runs; export readable evidence from damaged histories with explicit incompleteness notices. Reject runs owned by a live scheduler.
- [ ] (C) All of (B), plus a bounded snapshot of a run while its scheduler is active, explicitly identifying the snapshot boundary and any cross-file inconsistency.
- [ ] (D) Only failed/interrupted runs, aimed specifically at support and crash diagnosis.
- [ ] (E) Other (describe).

**Recommended answer(s):** (B).

**Why these are recommended:**

- B includes the interrupted and damaged runs most useful for diagnosis while avoiding live-snapshot semantics in the first release. The spec must still define how concurrent resume/reset or file changes are detected and rejected during export.
- A simplifies validation but excludes important failure evidence. C supports immediate collaboration during a run but adds consistency behavior across append-only and rewritten files. D unnecessarily prevents sharing successful examples.
- “Readable evidence” means only data allowed by the selected content policy; malformed bytes must never be copied verbatim as a fallback.

## Research notes for subsequent specification

- **Log export/privacy:** OWASP guidance above is a living document, consulted 2026-09-10. Relevant practices: select necessary fields, transform sensitive values at the export boundary, and account for disclosure when records leave their original store. The material unresolved choice is question 2.
- **Go filesystem confinement:** [Traversal-resistant file APIs](https://go.dev/blog/osroot), official Go blog, published 2025-03-12 and consulted 2026-09-10. `os.Root` confines path resolution, including symlink escapes, and is available within this repository's Go 1.25 baseline. Export must avoid following artifact references outside the selected run, use controlled archive member names, and guard against filesystem races. This is implementation guidance, not a user decision or a requirement to change the toolchain.
- **Proof direction after answers:** use synthetic run fixtures to demonstrate the selected archive/reader experience, exact included and excluded content, source preservation, path confinement, and the chosen state/incompleteness contract. No real run data or credentials are needed for proofs.

## Continuation

Answers have been accepted and incorporated into `23-spec-run-share-export.md`. Continue with task planning after reviewing that specification.
