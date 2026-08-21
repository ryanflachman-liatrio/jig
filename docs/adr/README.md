# Architecture Decision Records

This directory records the load-bearing design decisions made in jig. Each ADR
captures the *why* behind a choice — the constraints, trade-offs, and rejected
alternatives — so the rationale is one click away when revisiting or extending
the system.

ADRs 0007 and 0008 document the run-integration-branch model and the reset
algorithm respectively; they are the foundation for understanding how jig
manages code across steps.

| ADR | Title | Status | Decision summary |
|-----|-------|--------|-----------------|
| [0001](0001-manual-border-title-compositing.md) | Manual border-title compositing in the panel helper | accepted | lipgloss v2 exposes no border-title API; compositing border titles by hand is the only portable option. |
| [0002](0002-gates-are-nonblocking-focus-regions.md) | Monitor gates are non-blocking focus regions, not modal dialogs | accepted | A compact bar advertises queued gates; focusing one opens an overlay without resizing the monitor or blocking in-flight siblings. |
| [0003](0003-extensibility-lives-in-engine-and-schema.md) | Extensibility lives in the engine and schema, not the TUI | accepted | New step types are added via `engine.Executor` and `runner.Mux`; the TUI is a consumer, not the extension seam. |
| [0004](0004-tui-root-composes-screens-via-screen-interface.md) | The TUI root composes screens via a compile-time Screen interface | accepted | `rootModel` holds a `Screen` interface value; each screen is a self-contained Elm component, swapped by the root. |
| *(0005 absent)* | — | — | — |
| [0006](0006-engine-assembles-step-context-preamble.md) | The engine assembles a deterministic step-context preamble | accepted | The engine — not the agent — assembles the per-step context preamble from provenance data, keeping preamble content deterministic. |
| [0007](0007-run-integration-branch-model.md) | jig integrates step changes on a per-run branch, one squash commit per step | proposed | Each step worktree branches off the run-branch HEAD; on completion jig squash-merges it back as one tagged commit, enabling downstream steps to build on upstream code and commit-addressable reset. |
| [0008](0008-manual-reset-rewind-and-replay.md) | Manual reset rewinds the run branch and replays survivors, scoped to the dependency closure | proposed | Reset computes the target's `depends_on` closure, rewinds the run branch to before the earliest reset-set commit, cherry-picks independent survivors, and returns the reset set to `pending` with a bumped `Generation` counter. |
| [0009](0009-agent-security-monitoring.md) | Out-of-band two-tier agent security monitoring | accepted | A deterministic tool firewall plus an async Haiku monitor fleet provides defence-in-depth without blocking the hot path. |
| [0010](0010-nested-go-module-for-harness-acp.md) | Nested Go module for the ACP dependency | accepted | `harness/acp` is a nested module with a `replace` directive so `coder/acp-go-sdk`'s blast radius stays confined to the ACP-backend files. |
| [0011](0011-acp-via-zed-npx-adapter.md) | Drive Claude over ACP via Zed's npx adapter | accepted | Spawn `npx -y @zed-industries/claude-code-acp@latest` instead of a custom Go bridge — Zed's adapter is the proven ACP↔Claude reference implementation. |
