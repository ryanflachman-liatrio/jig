---
name: project-engine-phase1
description: Phase 1 of engine-design.md implemented — step/engine/runner/tui packages wired with FakeExecutor and run monitor TUI
metadata:
  type: project
---

Phase 1 of docs/engine-design.md is complete (committed 2026-07-30).

**Why:** Establishes the A+D skeleton — event-loop scheduler per run + event-sourced fan-out — needed before any real process/agent execution can be wired.

**What was built:**
- `internal/step` — Status, State, Result types
- `internal/engine` — Event vocabulary (9 types), journal envelope (JSONL encode/decode), Executor interface, Manager, Run, scheduler loop with ready-set + max_parallel + transitive-failure propagation
- `internal/runner` — FakeExecutor with configurable per-step delay/failure/deltas
- `internal/tui` — runsModel (run list), monitorModel (per-run step table), root wired with manager + event-drain cmd, `r` keybinding in detail screen
- `cmd/jig/main.go` — creates FakeExecutor + Manager, passes to tui.New()

**How to apply:** Phase 2 adds CommandExecutor + datastore + manifest.Writer. Phase 3 adds guards/loops/review. Phase 4 adds AgentExecutor.

**Key invariants:**
- scheduler is single-writer; workers and TUI communicate via inbox channel
- events fanned out after (Phase 2: before) journal append
- engine never imports runner; runner imports engine for the Executor interface
- tui.New() now takes *engine.Manager as third arg
