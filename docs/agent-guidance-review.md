# Agent guidance review

Reviewed against the working tree on 2026-09-11. This is a record of this
documentation review, not another source of ongoing agent instructions.
Start future work at [AGENTS.md](../AGENTS.md).

## Scope and structure

Rewrote `AGENTS.md`, `CLAUDE.md`, `CONTEXT.md`, and the architecture,
Go-conventions, and testing references. Added focused TUI and graph-engineering
guides. Updated the Rust entry point's scope, corrected adjacent README/schema
claims, and marked the old engine design as historical.

Workflow skill bodies under `.agents/skills` and `.claude/skills`, workflow
graphs, historical specs/proofs, dependencies, and production code were not
overhauled. The root guidance now points to shared references rather than
duplicating rules for individual coding tools.

## Main findings resolved

| Previous guidance problem | Current evidence and correction |
|---|---|
| Claimed only Claude was implemented and per-step backends were planned | [Harness selection](../internal/harness/select.go) supports Claude SDK/ACP, Cursor ACP, and Codex ACP. |
| Described one universal transport default | [Default resolution](../internal/workflow/load.go) chooses SDK for Claude and ACP for Cursor/Codex after explicit inheritance. |
| Listed only three step kinds | [Schema types](../internal/workflow/schema.go) include check and subworkflow; [module expansion](../internal/workflow/module.go) produces one graph. |
| Described default CLI startup as standalone chat/four screens | [Root model](../internal/tui/root.go) opens Home and Monitor; Detail is a Home overlay. |
| Pointed styling at removed root files | [Shared styles](../internal/tui/shared/styles.go) own the theme and semantic tokens. |
| Claimed engine contained no process execution | [Worktree code](../internal/engine/worktree.go) executes Git; agent/command execution remains behind the executor interface. |
| Treated manifest persistence as a background subscriber | [Manifest writer](../internal/manifest/manifest.go) is called synchronously before publication. |
| Promised termination and identical ordering too broadly | Route/retry bounds do not bound human waits or external calls without deadlines; independent workers can finish in different orders. |
| Said read-only steps receive no workspace | [Execution workspace selection](../internal/engine/execution.go) gives readers refreshed run-state views, separate from mutation worktrees. |
| Described reset closure ambiguously | Reset invalidates the target and downstream dependents, preserving independent survivor work. |
| Called shared map writes safe because receivers are values | Maps/slices still share storage; safety depends on ownership/synchronization and correct invalidation. |
| Recommended migration aliases and rigid model/file rules | Guidance now follows the pre-v1 removal policy and cohesive ownership rather than speculative wrappers or arbitrary line limits. |
| Claimed root tests covered everything | Root tests omit the nested ACP module and Rust crate; commands and verification scope now name those boundaries. |
| README example had a dangling input, missing skill, and misplaced verdict | Removed the dangling reference, used an existing skill, and moved `output_type` to the review step; verified decode. |
| Rust guidance implied parity with the complete Go schema | Clarified the separate crate and its current three-variant scope; parity requires feature-specific Rust evidence. |

## Practices added

- Go: small consumer interfaces, presence semantics, contextual errors,
  cancellation/cleanup, bounded I/O, and explicit ownership across goroutines.
- TUI: v2 APIs, command/message ownership, stale-result handling, terminal-cell
  sizing, focus/text capture, cache invalidation, and immutable source anchors.
- Graphs: explicit edge direction, stable presentation ordering, typed module
  boundaries, resource accounting, fan-in order, idempotency, and crash windows.
- Tests: behavior-specific verification, synchronization instead of sleeps,
  race/fuzz guidance, helper-process leases, synthetic export disclosure scans,
  opt-in live probes, and accurate pass/fail/skip reporting.

Primary-source links are attached to the relevant guidance in
[Go conventions](CONVENTIONS.md), [TUI engineering](TUI.md),
[Graph engineering](GRAPH_ENGINEERING.md), and [Testing](TESTING.md).
External practices are adapted to jig; they are not claims that jig implements
every capability of the referenced systems.

## Verification

- Root `go test ./...`: passed, including TUI, CLI, notification, and telemetry
  packages. Initial sandbox/dependency-download failures were resolved before
  the successful run.
- `go build -o /tmp/jig-doc-check ./cmd/jig` and root `go vet ./...`: passed.
- Nested `harness/acp` tests and vet: passed.
- All 15 root workflow/example TOMLs and the README TOML snippet: validated
  through the current Go loader. Module/profile files were not treated as
  standalone workflows.
- Relative links, source-path references in the new core guides, and
  `git diff --check`: checked.
- No live model probes, race suite, Rust checks, or terminal UI smoke were run;
  this change modifies documentation only.
