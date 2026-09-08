# Implementation Plan: Unfinished park reopen after process death

**Status:** Implemented (2026-09-08)
**Depends on:** Spec 20 mid-execution crash recovery (generalized `Manager.Resume`,
`session.json`, D7 orphan rules, journal-before-reopen); Spec 06 integration
gates; Spec 07 live Stop/Resume; review-gate restore path
**Complements:** A2 thin ops CLI (`jig resume` / `status`); Spec 20 worker crash
reopen (ships first and hard-rejects these parks until this spec)
**Breaks:** Spec 20's intentional Resume reject for non-worker unfinished parks.
Pre-v1: prefer one Resume entry point that restores every durable park kind.

---

## Problem

Spec 20 makes process death mid-**worker** reopenable: durable `running` /
`validating` → recovery gate, plus existing review-gate restore. It **rejects**
Resume when the journal still holds other unfinished parks:

| Durable status after process death | Why Spec 20 deferred it |
|---|---|
| `needs_input` | Waiting agent / `block_on` process is gone; question payload may be in journal but UX is not a worker retry |
| `awaiting_recovery` | Already parked before crash; must re-emit `RecoveryRequest` + restore CanResume from `session.json` / Result without treating it as a fresh crash |
| `stopped` | Spec 07 same-process Stop park; after process death there is no live `Run.Resume` — need Manager-level restore of stopped park + session maps |
| `awaiting_integration` | Squash-merge conflict state lives in the **run** worktree; needs `IntegrationConflictRequest` re-emit + path list, not recovery |

Today (even after Spec 20): if jig dies while any of those parks are open —
alone or mixed with review / interrupted workers — the operator cannot reopen,
or Spec 20 refuses the whole Resume to avoid a half-restored scheduler.

`reconcileInterruptedRun` still ignores these statuses (it only virtual-fails
`running`). They already show as forever-nonterminal / paused in Runs without a
working `R` path.

This is the remaining half of open-goal A3's "restart what process death left
unfinished," scoped so Spec 20 can ship the P0 kill-mid-agent path first.

---

## Goal

Extend `Manager.Resume` so **every durable unfinished park** restores under one
entry point:

1. Keep Spec 20 worker reopen (`running` / `validating` → recovery) and review
   restore unchanged.
2. Restore pre-crash `awaiting_recovery` by re-emitting `RecoveryRequest` with
   honest `CanResume` (SessionID ∧ CapSessionResume).
3. Restore `stopped` as a live stopped park (operator uses existing Spec 07
   Resume-step / gate actions — not a fake crash recovery).
4. Restore `needs_input` by re-emitting the correct wait surface (`InputRequest`
   and/or `AgentQuestion`) from journal + on-disk artifacts — **not** by forcing
   the step through crash-recovery chrome unless no question payload exists.
5. Restore `awaiting_integration` by re-emitting `IntegrationConflictRequest`
   with conflict paths from the restored run worktree.
6. Mixed runs restore **all** park kinds together (lift Spec 20 D8 reject).
7. Preserve journal-before-fan-out: any synthesized gate events on reopen are
   durable before `runLoop`.

TUI `R` and future A2 `jig resume` keep calling the same `Manager.Resume`.

---

## Non-goals (this spec)

- Mid-turn LLM rewind (still Spec 07 / Spec 20 non-goal)
- Shipping `jig resume` CLI (A2) — consume the richer Resume API only
- A4 / A5 CapSessionResume harness work
- Reopening settled runs / reset (A12 / ADR 0008)
- Changing live (same-process) Stop/Resume/Recover/Resolve semantics except where
  shared helpers must serve both live and reopen paths
- Validate-only retry without full re-exec (still deferred)
- Remote workers (A19)
- Persistence-off reopen

---

## Research (post–Spec 20 expected truth)

| Park | Durable signals | Live restore API | Gap after process death |
|---|---|---|---|
| Review | `ReviewRequest` + docs on disk | `restoreReviewCheckpoint` | Works (Spec 05/20) |
| Interrupted worker | `running`/`validating` + optional `session.json` | Spec 20 → `awaiting_recovery` | Works after Spec 20 |
| Recovery (pre-crash) | `awaiting_recovery` + prior `RecoveryRequest` in journal | Live gate only | No Manager restore; Spec 20 rejects |
| Stopped | `stopped` + optional SessionID in Result / session.json | `Run.Resume` (live) | No Manager restore |
| Needs input | `needs_input` + `InputRequest` / questions in journal | `Run.AnswerQuestion` | No Manager restore; agent process gone |
| Integration | `awaiting_integration` + conflict in run worktree | `Run.ResolveIntegration` | No Manager restore; must rebuild request + paths |

Spec 20 invariant to preserve: crash **workers** still transition to
`awaiting_recovery` — they must **not** be routed through `StatusStopped` /
`Run.Resume`. Spec 21 adds restore for statuses that were **already** parks
before death.

---

## Product contract

### Operator stories

1. **Died on recovery gate** — Restart jig → `R` → same recovery overlay
   (retry / resume / skip / abort) with CanResume from `session.json`.
2. **Died while step stopped** — `R` → step still `stopped`; operator can
   Resume-step (Spec 07) or otherwise act via existing chrome.
3. **Died on block_on / AskUserQuestion** — `R` → question / input gate
   reappears; answering continues the workflow. If payload cannot be restored,
   degrade to recovery with clear Err (decision E4).
4. **Died on integration conflict** — `R` → integration gate with paths;
   Resolve / agent-prepare paths work as live Spec 06.
5. **Mixed** — review + interrupted worker + integration (etc.) all restore in
   one Resume; run stays alive (ADR 0002).

### Headless / A2

Once A2 calls `Manager.Resume`, Spec 19-style policies must settle **all**
restored gate kinds (`--on-recovery`, `--on-review`, `--on-conflict`, question
policy). Spec 21 documents the matrix; A2 implements CLI flags if missing.

---

## Architecture (proposed)

### Single allow-list

Replace Spec 20's reject-on-Spec-21-parks with a classifier:

```
for each step status after fold:
  pending|succeeded|skipped|failed → keep
  awaiting_review → restore review session (existing)
  running|validating → Spec 20 crash → awaiting_recovery (+ journal)
  awaiting_recovery → rehydrate RecoveryRequest (no status churn unless needed)
  stopped → restore stopped park + session maps from session.json/Result
  needs_input → re-emit InputRequest / AgentQuestion (E4)
  awaiting_integration → restore run worktree conflict + IntegrationConflictRequest
```

### Shared helpers

Prefer extracting restore helpers used by both live paths and Resume:

- `rehydrateRecovery(step)` — Err, CanResume, optional guidance empty
- `rehydrateStopped(step)` — SessionID into resume maps
- `rehydrateNeedsInput(step)` — last question events / block_on InputRequest
- `rehydrateIntegration(step)` — `mergeConflictPaths(runWorktree)` + request

Avoid duplicating gate chrome; TUI should see the same event types as live.

### Worktrees

- Always `restoreRunBranch` as today (required for integration conflicts).
- Step worktrees: keep if present; Spec 20 D10 rules still apply when a Spec 20
  crash-transitioned step later chooses resume vs retry.
- Integration reopen **fails closed** if run worktree cannot be restored or
  conflict markers are gone (decision E5).

### Journal

- Spec 20 crash transitions: still journal `→ awaiting_recovery` + new
  `RecoveryRequest`.
- Pre-crash parks: prefer **re-emit** gate request events if the live bus
  consumers need a fresh liveness signal; avoid rewriting history. Exact
  "re-emit vs fold-only" is decision E3.

---

## Decision map

| # | Topic | Proposed decision | Status |
|---|---|---|---|
| E1 | Product shape | One `Manager.Resume` restores all unfinished park kinds; lift Spec 20 D8 reject | locked |
| E2 | Stopped semantics | Restore as `stopped` (Spec 07), not crash→recovery | locked |
| E3 | Gate re-emit | Append fresh `RecoveryRequest` / `InputRequest` / `AgentQuestion` / `IntegrationConflictRequest` rows as one pre-loop batch, then fan out | locked |
| E4 | needs_input payload missing | Degrade to `awaiting_recovery` with Err explaining lost payload/session; prefer unresolved AgentQuestion, then InputRequest | locked |
| E5 | Integration conflict markers missing | Fail Resume for the run; preserve the historical worktree and do not invent empty conflict UI | locked |
| E6 | Pre-crash awaiting_recovery | Rehydrate in place; do not bump Attempt; CanResume from session.json ∧ cap | locked |
| E7 | Mixed with Spec 20 workers | Allowed; each park kind independent (ADR 0002) | locked |
| E8 | TUI | No new panels; existing Gate chrome per event type; deduplicate replay/live request boundaries | locked |
| E9 | A2 / headless | Document full settle matrix; CLI flags remain A2 | locked |
| E10 | Ordering | Implement only after Spec 20 Phases 1–2 land (needs generalized Resume skeleton) | satisfied |
| E11 | Docs | engine-design Resume classes; workflow-schema; CONTEXT; open-goals A3 residual | implemented |

---

## Phased implementation

### Phase 0 — Spec lock

- [x] Confirm E1–E11 (resolve E4, E5)
- [x] Inventory journal event payloads needed for question / integration rehydrate
- [x] Agree fail-closed vs degrade-to-recovery per park

### Phase 1 — Engine park restore

**Packages:** `internal/engine`

- [x] Lift Spec 20 reject for Spec 21 statuses
- [x] Rehydrate `awaiting_recovery`, `stopped`, `needs_input`, `awaiting_integration`
- [x] Mixed-park tests (review + recovery + integration + stopped + needs_input
      combinations worth covering; table-driven)
- [x] Preserve Spec 20 worker crash path tests

**Exit criteria:** `go test ./internal/engine` — Resume accepts each park kind
alone and in mixed fixtures; finished still rejected.

### Phase 2 — TUI + copy

- [x] Ensure Gate overlays bind to re-emitted events after Resume
- [x] Runs/Monitor copy for non-crash unfinished parks (not all "interrupted")
- [x] Replay/live boundary tests

### Phase 3 — Docs + A2 matrix note

- [x] engine-design / schema / CONTEXT / open-goals / headless settle matrix

---

## Testing plan

| Layer | Cases |
|---|---|
| engine | Each park alone; mixed with Spec 20 running→recovery; reject finished; integration missing markers (E5); needs_input missing payload (E4); CanResume on rehydrated recovery |
| tui | R on each park kind; gate chrome matches live |
| replay | No virtual finish for these statuses (already true); banners coherent |

---

## Risks

| Risk | Mitigation |
|---|---|
| Fake question UX without live worker | E4 degrade path; docs |
| Integration tree cleaned by `restoreRunBranch` | Order restore carefully; test conflict survival across prune/recreate |
| Scope collision with A2 | Engine-only in Spec 21; CLI stays A2 |
| Spec 20 still in flight | E10 hard dependency on Spec 20 Resume skeleton |

---

## Resolved questions

1. Missing `needs_input` payload or resumable session degrades that step to
   recovery; it does not reject unrelated parks in the run.
2. Missing integration conflict markers rejects Resume and leaves the historical
   worktree intact.
3. A stopped step without `session.json` remains stopped; Resume-step restarts it
   fresh, matching live Spec 07 behavior.
4. Resume appends fresh gate rows for durability/liveness. The journal fold and
   Monitor deduplicate them by gate identity.

---

## Success criteria

- Process death on each of `needs_input`, `awaiting_recovery`, `stopped`,
  `awaiting_integration` → restart → `R` → correct live gate/park → operator
  can finish the run.
- Mixed parks with Spec 20 interrupted workers restore in one Resume.
- Spec 20 kill-mid-agent path remains green.
- No new gate panel kinds.

---

## Related docs

- [`../20-spec-mid-crash-recovery/20-implementation-plan.md`](../20-spec-mid-crash-recovery/20-implementation-plan.md) — prerequisite; D8 carve-out
- [`docs/plans/open-goals.md`](../../plans/open-goals.md) — A3 residual
- [`docs/specs/06-spec-run-integration-branch/`](../06-spec-run-integration-branch/) — integration conflicts
- [`docs/specs/07-spec-stop-resume-step/`](../07-spec-stop-resume-step/) — stopped park
- [`docs/specs/19-spec-headless-run/19-implementation-plan.md`](../19-spec-headless-run/19-implementation-plan.md) — gate settle policies
- [`docs/adr/0002-gates-are-nonblocking-focus-regions.md`](../../adr/0002-gates-are-nonblocking-focus-regions.md)
