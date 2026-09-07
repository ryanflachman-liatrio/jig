# Implementation Plan: Mid-execution crash recovery

**Status:** Decisions locked (open goal A3 — P0) — ready for implementation
**Depends on:** Journal-before-fan-out (`docs/engine-design.md` §3); review-gate
`Manager.Resume` (review restore path); early `EventSessionID` capture (Spec 07 B2);
TOML-only harness selection (Spec 14); headless `jig run` settle contract
(Spec 19)
**Complements:** A2 thin ops CLI (`jig resume` / `status`); A4/A5 Cursor + Claude
ACP `CapSessionResume` parity; Spec 21 (full unfinished-park reopen)
**Breaks:** Display-only `reconcileInterruptedRun` semantics for reopenable runs;
the review-gate-only `Manager.Resume` contract. Pre-v1: prefer the correct
long-term reopen model over keeping synthetic "failed forever" as the product
answer.

**Follow-on:** [`../21-spec-unfinished-park-reopen/21-implementation-plan.md`](../21-spec-unfinished-park-reopen/21-implementation-plan.md)
restores non-worker parks (`needs_input`, pre-crash `awaiting_recovery`,
`stopped`, `awaiting_integration`) after process death. Out of Spec 20 MVP.

---

## Problem

jig already survives **process exit while parked on a document-review gate**.
`Manager.Resume` restores a live scheduler from `journal.jsonl` + `workflow.json`
+ on-disk review docs (`internal/engine/resume.go`). That path is intentional and
narrow:

> no worker or backend session is alive after the owning process exits, while
> review gates are fully durable.

What it does **not** cover is the common failure: **jig (or the host) dies while
a step is mid-execution**. Today that leaves:

| Durable truth | Consumer lie |
|---|---|
| `journal.jsonl` ends on `StepStatus{To: running}` (or other non-review unfinished) | **Runs** (`ReplayJournalRaw`): unfinished → `paused` |
| Partial `transcript.jsonl` may exist | **Monitor** (`ReplayJournal`): virtual `running→failed` + `RunFinished{Failed:true}` via `reconcileInterruptedRun` |
| No `result.json` (terminal-only write) | Operator hits `R` → `"only quiescent review gates can resume"` |
| No durable `SessionID` while `running` | Session id lived only in memory / post-terminal `Result` |

Note the **split UX**: the same interrupted run can look paused in Runs and
falsely finished in Monitor. Spec 20 must fix both consumers together (D7).

Also note: `reconcileInterruptedRun` only rewrites durable **`running`**. A crash
during `validating` / `needs_input` / other parks leaves forever-nonterminal
status in both Raw and display replay — those non-worker parks are Spec 21.

Open-goals ranks this **#11** cross-cutting / **P0 product** (`docs/plans/open-goals.md` A3).
Engine design deferred it explicitly: crash-recovery/resume becomes "replay the
journal" with no redesign (`docs/engine-design.md` load-bearing #3; Deferred §).
Schema MVP-1 deferred "arbitrary in-flight agent checkpoint/resume" and notes
interrupted workers remain recovery cases (`docs/workflow-schema.md` MVP 1).

Without A3, long agent runs are **one kill -9 / OOM / laptop sleep away from a
permanently wedged or falsely-terminal run** that the operator cannot reopen.

---

## Goal

Make an unfinished run whose **workers** died with the process **reopenable**
under the same mental model as review-gate resume:

1. **Detect** interrupted in-flight worker steps from durable journal state (not
   from virtual display events).
2. **Restore** a live scheduler via generalized `Manager.Resume` under
   `scheduler.lock`.
3. **Park** each interrupted worker on the existing **recovery gate**
   (`awaiting_recovery` + `RecoveryRequest`) — no second UX.
4. **Offer** `retry` (fresh) always; `resume` (continue agent session) when a
   durable SessionID exists **and** the harness advertises `CapSessionResume`;
   `skip` / `abort` unchanged.
5. **Preserve** file-is-truth: transcripts, worktrees, and journal remain the
   audit trail; recovery actions append new durable events rather than rewriting
   history.
6. **Journal before fan-out on reopen:** durable `running`/`validating` →
   `awaiting_recovery` + `RecoveryRequest` are appended **before** the scheduler
   loop advertises gates (crash during reopen must not lose the decision surface).

Headless / ops CLI (`jig resume <run-id>`, Spec 19 D12 / A2) should call the same
engine API later; TUI `R` is the interactive client in this spec. Spec 20 does
**not** ship `jig resume` — only a non-Bubble-Tea engine API.

---

## Non-goals (this spec)

- Recovering the **exact interrupted LLM turn** mid-stream (Spec 07 non-goal;
  backends continue with a *new* message into the session)
- Making ACP→Claude or Cursor advertise `CapSessionResume` (A4 / A5) — crash
  recovery must **degrade to fresh retry** when the cap is absent
- Reopening **fully settled** runs (`RunFinished`) for reset (A12 / ADR 0008)
- Remote / distributed workers (A19)
- Checkpointing arbitrary agent filesystem state beyond worktree + transcript
- Auto-resume without operator (or headless policy) consent on crash reopen
- Changing `on_failure` automatic retry semantics for live failures
- Inventing a new TUI "crash" panel distinct from Gate recovery chrome
- Persistence-off crash recovery (no run dir ⇒ nothing to reopen — keep no-op)
- Restoring non-worker parks after process death (`needs_input`, pre-parked
  `awaiting_recovery`, `stopped`, `awaiting_integration`) — **Spec 21**
- Shipping `jig resume` / `jig status` CLI — **A2** (engine API ready after Phase 2)
- Validate-only retry (re-run `[step.validate]` without re-exec) — full re-exec
  only in MVP

---

## Research (current code truth)

### What already works

| Surface | Behavior | Where |
|---|---|---|
| Review-gate resume | Restore scheduler; only `pending` / `succeeded` / `skipped` / `awaiting_review` (+ matching `ReviewRequest` docs) | `Manager.Resume`, `restoreReviewCheckpoint` |
| Live Stop → Resume | Cancel worker; park `stopped`; re-dispatch with `ResumeSessionID` if known | Spec 07; `Run.Stop` / `Run.Resume` |
| Live recovery gate | `awaiting_recovery` + `Recover(retry\|resume\|skip\|abort)` | `enterRecovery`, `handleRecover` |
| Early SessionID (live) | First `EventSessionID` kept on cancel-before-Result | Spec 07 B2; `runner/agent.go` |
| Journal fold | `state = fold(journal)`; torn tail skipped | `ReplayJournal` / `ReplayJournalRaw` |
| Display crash reconcile | Virtual `running→failed` + `RunFinished` | `reconcileInterruptedRun` — **not durable**; **`running` only** |

### The durability hole

```
dispatch → StepStatus{running} ──journaled──▶
  EventSessionID ──in-memory only──▶   ← A3 must make this durable
  … tokens / tools / partial transcript …
  process death
  no stepDone, no result.json, no SessionID on disk
```

`result.json` is written only on terminal step statuses (`succeeded` / `failed` /
`skipped`). Mid-flight SessionID therefore cannot survive process death today,
even for Claude SDK / Codex which *can* resume sessions live.

### Live CanResume gap (fix in this spec)

Today `enterRecovery` sets `CanResume: state.Result.SessionID != ""` with **no**
`CapSessionResume` check. ACP→Claude / Cursor can surface session ids without
supporting resume → false affordance. Spec 20 tightens **live and crash** paths
to `SessionID ∧ CapSessionResume` (D5).

### Harness matrix (resume after crash)

| Backend / transport | CapSessionResume today | Crash-recovery offer |
|---|---|---|
| Claude + `sdk` | Yes | `resume` when SessionID durable |
| Claude + `acp` | No (A5) | `retry` only until A5 |
| Cursor + `acp` | No (A4) | `retry` only until A4 |
| Codex + `acp` | Yes | `resume` when SessionID durable |
| `command` / `check` | N/A | `retry` (re-exec); no session |
| `review` | N/A | Already covered by review-gate resume |

### Spec 07 vs A3 (do not conflate)

| | Spec 07 Stop/Resume | A3 process crash |
|---|---|---|
| Process | Same live scheduler | Process gone; journal on disk |
| Worker exit | `stepDone` parks `stopped` | No `stepDone`; status stays `running` |
| SessionID | In-memory `Result` after cancel | Must be read from **new durable store** |
| API | `Run.Stop` / `Run.Resume` | Generalized `Manager.Resume` |
| Semantics | New message into session | Same — new message, not mid-turn rewind |
| Invariant | — | **Never** route crash reopen through `Run.Resume` / `StatusStopped`. Crash path is `running`/`validating` → `awaiting_recovery`. |

---

## Product contract

### Operator story (TUI)

1. Run dies mid-agent (kill, crash, power loss).
2. Operator restarts `jig`, opens Runs.
3. Interrupted run shows as **interrupted** (not falsely finished; distinct from
   review-paused copy).
4. `R` (Resume) acquires `scheduler.lock`, restores workflow snapshot + journal
   fold, appends durable recovery transitions, parks each interrupted worker on
   the **recovery gate**.
5. Gate offers:
   - **retry** — fresh agent/command (clear resume maps + `session.json`; bump Attempt)
   - **retry with guidance / resume** — only if durable SessionID +
     `CapSessionResume`; compose crash-specific recovery message
   - **skip** / **abort** — unchanged
6. Sibling steps that were already terminal stay terminal; review-parked steps
   remain review-parked. Mixed **review + interrupted worker** restores both
   (MVP). Other unfinished parks → Spec 21 (Resume may reject or leave for 21
   if present — see D8).

### Operator story (headless / A2) — out of Spec 20 CLI

```bash
# After CI agent host OOM'd mid-step (requires A2):
jig resume <run-id> --on-recovery retry   # or abort|skip; resume optional later
# Same engine API as TUI R; policy settles like Spec 19
```

Spec 19 `--on-recovery` settles gates **during** an active `jig run`. It does
**not** reopen a dead process. Crash reopen needs A2 (or TUI `R`) to call
`Manager.Resume` first; then the same `Recover` API applies. No new gate kind.

### Status vocabulary

Do **not** add a new `StatusCrashed`. On reopen, transition durable
`running` / `validating` → `awaiting_recovery` with Err sentinel
`"process exited while step was running"` (D14).

### What counts as "interrupted" in Spec 20 MVP

| Last durable status | Spec 20 Resume action |
|---|---|
| `running` | Park recovery (primary) |
| `validating` | Park recovery; **retry** = full step re-exec (D9) |
| `awaiting_review` | Existing review restore |
| `pending` / `succeeded` / `skipped` / `failed` | Keep |
| Journal already has `RunFinished` | Reject resume (settled; A12) |
| `needs_input` / `awaiting_recovery` / `awaiting_integration` / `stopped` | **Out of MVP** — Spec 21. If present alongside interrupted workers, Resume **rejects** with a clear error naming the unsupported park (D8), unless only review + running/validating. |

### Orphans (D7)

Virtual `RunFinished` / display-fail applies **only** when the run cannot reopen:

- missing / unreadable `workflow.json`
- journal unreadable or missing `RunStarted`
- (not) presence of `scheduler.lock` — flock is released on process death; lock
  file on disk is **not** an orphan signal

Everything else unfinished with only review and/or `running`/`validating` parks
is reopenable and must **not** get a virtual finish in Monitor.

---

## Architecture

### Durable mid-flight session identity

Add a **crash-consistent early write** when the runner first observes a
non-empty session id:

```
.jig/runs/<id>/steps/<step-id>/session.json
```

Suggested shape:

```json
{
  "session_id": "…",
  "backend": "claude",
  "transport": "sdk",
  "attempt": 1,
  "iteration": 0,
  "generation": 0,
  "updated_at": "…"
}
```

Rules:

- Write on first non-empty `EventSessionID` (match live `noteSession`: keep
  first id; harnesses do not rotate mid-step today).
- Also flush SessionID from `stepDone` Result into `session.json` before/at
  validate entry so a crash in the narrow `validating` window still has a
  durable id when one existed (D9).
- Write atomically (temp + rename); ignore empty/corrupt on read.
- Clear on fresh retry / fresh dispatch (no `ResumeSessionID`) so stale ids
  cannot resume — **both** crash reopen and live `Recover(retry)`.
- Persistence-off: path empty → no-op (first-class).
- Prefer a **side file** over expanding `result.json` mid-flight so terminal
  result materialization stays terminal-only.
- Journal `StepSession` event: **not** required in Phase 1. Add in Phase 2 only
  if Resume fold needs SessionID without touching datastore (D3).

Runner writes `session.json` via datastore helpers when run dir is set (mirrors
transcript append: file is truth; engine reads on reopen). Engine does not need
a live SessionID bus event for crash durability.

### Generalized `Manager.Resume` (MVP allow-list)

Expand `restoreReviewCheckpoint` into a broader **restore unfinished checkpoint**
with a **narrow** MVP allow-list:

```
Manager.Resume(runID, legacyWorkflow)
  → load workflow snapshot
  → ReplayJournalRaw (durable only)
  → classify steps:
       review-parked → restore ReviewRequest + docs (existing)
       running / validating → durable transition → awaiting_recovery
                            + RecoveryRequest (journal before loop)
       other unfinished parks → reject (Spec 21)
       terminal / pending → keep
  → restore run branch / worktree prune (existing)
  → acquire scheduler.lock
  → start scheduler loop from restored state (do not re-dispatch until Recover/Resolve)
```

Reject when:

- run already has a live scheduler (`m.runs` + `scheduler.lock`)
- journal shows `RunFinished`
- workflow snapshot mismatch (existing)
- unsupported unfinished park present (Spec 21 statuses)
- no reopenable park and no interrupted workers (nothing to do)

Flip `TestResumeRejectsInterruptedWorker` into
`TestResumeParksInterruptedWorkerOnRecovery`.

Keep API name **`Manager.Resume`** (D17 naming / open Q5) — match TUI `R` and
Spec 19 D12; broaden the docstring only.

### Relationship to `reconcileInterruptedRun`

| Consumer | Today | After Spec 20 |
|---|---|---|
| Ownership / Runs hydrate (`ReplayJournalRaw`) | Unfinished → `paused` | Unchanged raw; UI copy distinguishes review-paused vs crash-interrupted |
| Monitor historical (`ReplayJournal`) | Virtual fail + finish for `running` | If reopenable: show unfinished + interrupted banner; virtual fail **only for orphans** (D7) |
| `Manager.Resume` | Rejects interrupted | Accepts `running`/`validating` (+ review) + parks recovery |

Do not append virtual `RunFinished` into the durable journal. Reopen must keep
the run unfinished until a real operator/policy decision finishes it.

### Recovery message on crash-resume

Reuse `composeRecoveryMessage` with a crash-specific preamble, e.g.:

> The jig process exited while this step was running. Partial transcript may
> exist on disk. Continue from the prior agent session; do not redo completed
> work unless necessary.

Guidance text from the operator still folds in (live recovery UX).

### Worktrees & git

Interrupted mutating steps may leave dirty step worktrees. On reopen:

1. Prune/recreate **run** worktree as today (`restoreRunBranch`).
2. For interrupted mutators: keep the existing step worktree if present; do not
   `git reset --hard` unless the operator chooses **retry** (fresh) — **resume**
   continues against the same dirty tree when possible.
3. Missing step worktree + mutator → force fresh retry (`CanResume` presentation
   false / degrade); `isolation=none` may still session-resume (D10).

### CanResume + dead sessions

- `CanResume = durable SessionID ≠ "" ∧ harness CapSessionResume` (live
  `enterRecovery` and crash reopen).
- If `Recover(resume)` fails because the backend session is dead: re-enter
  recovery with **`CanResume=false`** (or one retry then false). Do not loop
  "resume a dead session" forever. Document unknown backend TTLs.

---

## Decision map

| # | Topic | Decision | Status |
|---|---|---|---|
| D1 | Product shape | Reopen interrupted runs via generalized `Manager.Resume`; park recovery gate — do not auto-finish as failed | **locked** |
| D2 | UX | Reuse recovery gate actions; no new panel | **locked** |
| D3 | Session durability | Side file `steps/<id>/session.json` on first `EventSessionID`; journal `StepSession` only if Phase 2 fold needs it | **locked** |
| D4 | Who writes session.json | `internal/runner` (agent path) via datastore helper; engine reads on resume; flush again before validate | **locked** |
| D5 | CanResume | `SessionID ∧ CapSessionResume` for **live and crash** paths | **locked** |
| D6 | Command/check interrupt | Recovery with retry (= re-exec); no resume | **locked** |
| D7 | `reconcileInterruptedRun` | Virtual fail only for orphans (missing snapshot / unreadable journal / no RunStarted); reopenable unfinished runs stay unfinished in Monitor + Runs | **locked** |
| D8 | Mixed parks | MVP: review + `running`/`validating` only. Other unfinished parks → reject (Spec 21). | **locked** |
| D9 | `validating` interrupt | Park recovery; **retry** = full step re-exec; ensure SessionID flushed to `session.json` before/at validate | **locked** |
| D10 | Missing worktree on resume | Mutator missing tree → force fresh retry; `isolation=none` may session-resume; keep dirty tree when present | **locked** |
| D11 | Auto policy on reopen | Never auto-Recover on TUI reopen; headless `jig resume --on-recovery` may act (**A2**) | **locked** |
| D12 | CLI surface | Engine + TUI `R` in scope; `jig resume` deferred to **A2** (API ready) | **locked** |
| D13 | A4/A5 ordering | Spec 20 ships degrade-closed; A4/A5 unlock `CanResume` without Spec 20 changes | **locked** |
| D14 | Err string | Durable sentinel: `"process exited while step was running"` | **locked** |
| D15 | Attempt counter | Crash reopen does not bump Attempt until operator picks retry/resume | **locked** |
| D16 | Parallel interrupted siblings | Each interrupted worker gets its own `RecoveryRequest`; run stays alive (ADR 0002) | **locked** |
| D17 | Docs + naming | Keep API `Manager.Resume`; update schema / engine-design / CONTEXT / open-goals / headless notes | **locked** |
| D18 | Journal on reopen | Append `StepStatus` → `awaiting_recovery` + `RecoveryRequest` before starting scheduler loop | **locked** |
| D19 | Dead-session resume | Failed `Recover(resume)` → re-park with `CanResume=false` | **locked** |
| D20 | Retry clearing | `Recover(retry)` clears `session.json` + resume map entries (live and crash) | **locked** |

---

## Phased implementation

### Phase 0 — Spec lock (this document)

- [x] Confirm D1–D20 (D9, D10, D8 resolved; Spec 21 carved out)
- [x] Err sentinel: `"process exited while step was running"`
- [x] `session.json` schema (above)
- [x] MVP allow-list: review + `running` / `validating` only

### Phase 1 — Durable SessionID (prerequisite)

**Packages:** `internal/datastore`, `internal/runner`, tests

- [ ] `datastore.SessionPath(runDir, stepID)` + atomic write/read/clear helpers
- [ ] Agent runner: on first non-empty `EventSessionID`, write `session.json`
- [ ] Flush SessionID from Result into `session.json` when entering validate
- [ ] Clear session file when dispatching a fresh attempt (no `ResumeSessionID`)
- [ ] Persistence-off no-op tests
- [ ] Unit test: after successful session write, cancel/teardown still leaves
      `session.json` readable (do not assert mid-write kill -9)

**Exit criteria:** A live Stop/crash simulation leaves `session.json` readable
after process-equivalent teardown; no engine Resume changes yet.

### Phase 2 — Engine reopen for interrupted workers

**Packages:** `internal/engine`

- [ ] Generalize checkpoint restore allow-list (review + running/validating)
- [ ] On Resume: for each interrupted worker status, **journal** transition →
      `awaiting_recovery` + emit `RecoveryRequest` **before** `runLoop`
      (`CanResume` from session.json ∧ harness capability)
- [ ] Load SessionID into `state.Result` / resume maps so `Recover(resume)` works
- [ ] Tighten live `enterRecovery` CanResume to SessionID ∧ CapSessionResume (D5)
- [ ] `Recover(retry)` clears session.json + resume maps (D20)
- [ ] Dead-session resume → re-park `CanResume=false` (D19)
- [ ] Reject Resume when Spec 21 parks present (clear error)
- [ ] Replace/extend `TestResumeRejectsInterruptedWorker`
- [ ] Tests: review-only; running-only; validating; mixed review+running;
      finished rejected; Spec 21 park rejected; lock contention; CanResume
      true/false; Recover resume uses durable id; Recover retry clears file
- [ ] Adjust `reconcileInterruptedRun` / Monitor consumers per D7

**Exit criteria:** `go test ./internal/engine` — interrupted journal can Resume
and park recovery without a live worker; orphans alone still display-reconcile.

### Phase 3 — TUI copy + Gate wiring

**Packages:** `internal/tui`

- [ ] Runs list: distinguish "paused (review)" vs "interrupted (crash)" using
      raw journal (not virtual finish)
- [ ] Resume key `R` — verify interrupted path; surface Spec 21 reject errors
- [ ] Monitor: historical+interrupted before resume → banner (no fake finish);
      after resume → live recovery overlay
- [ ] Crash-specific recovery preamble in gate copy
- [ ] Footer / help text for interrupted runs
- [ ] Golden / model tests for list + gate

**Exit criteria:** Manual dogfood: start agent step, `kill -9` jig, reopen,
`R`, recover retry and resume (Claude SDK).

### Phase 4 — Docs + headless hook readiness

- [ ] `docs/workflow-schema.md` — rewrite MVP deferred sentence; document crash
      reopen + session.json
- [ ] `docs/engine-design.md` — move mid-execution crash recovery out of Deferred;
      describe Resume classes (review / interrupted-worker); point Spec 21 at
      remaining parks
- [ ] `CONTEXT.md` — crash reopen vs Stop/Resume
- [ ] `docs/headless.md` — note A2 `jig resume` will call Manager.Resume then
      consume RecoveryRequest (Spec 19 `--on-recovery` alone is not reopen)
- [ ] `docs/plans/open-goals.md` — A3 → planned/in-progress (Spec 20); link Spec 21
- [ ] Example or script under `examples/` optional smoke (command step kill)

### Out of scope here (tracked elsewhere)

| Item | Where |
|---|---|
| `jig resume` / `jig status` CLI | A2 |
| Restore `needs_input` / `stopped` / `awaiting_integration` / pre-parked `awaiting_recovery` | Spec 21 |
| CapSessionResume for Cursor / Claude ACP | A4 / A5 |
| Validate-only retry without full re-exec | Deferred (not scheduled) |

---

## Testing plan

| Layer | Cases |
|---|---|
| datastore | session write/read/clear; atomic rename; empty runDir no-op; corrupt ignore |
| runner | SessionID event → file; validate flush; fresh retry clears file; resume dispatch keeps file |
| engine | Resume interrupted agent; Resume command; mixed review+running; reject finished; reject Spec 21 parks; CanResume true/false (cap matrix); Recover resume uses durable id; Recover retry clears maps + session file; dead resume → CanResume false; journal-before-loop on reopen |
| replay | Raw unfinished; display reconcile only for orphans |
| tui | Runs interrupted badge; R succeeds; no fake Monitor finish; recovery overlay |
| harness | Fake without CapSessionResume → CanResume false even with SessionID |
| integration | Optional: start real Claude SDK step, kill, resume (manual / nightlies) |

Race: `go test ./internal/engine/... -race` remains the gate (Spec 13).

---

## Risks

| Risk | Mitigation |
|---|---|
| Backend session dead after long outage even with CapSessionResume | D19: re-park with CanResume=false; document TTL unknowns |
| Stale session.json after partial write | Atomic temp + rename; ignore empty/corrupt on read |
| Operator expects exact mid-turn restore | Docs + gate copy: "continues with a new message" (Spec 07) |
| Virtual RunFinished taught users runs are dead | D7 UI + open-goals note |
| Mutator dirty tree vs resume | D10; keep tree on resume; force fresh if missing |
| A4/A5 lag | Degrade to retry; do not block Spec 20 |
| Double scheduler | Existing `scheduler.lock` + Manager live-map check |
| Scope creep into park restore | Hard reject Spec 21 statuses; link Spec 21 |
| Crash during Resume before RecoveryRequest journaled | D18: journal transitions before `runLoop` |

---

## Open questions

None blocking Phase 1. Resolved in Phase 0:

1. ~~D9~~ — full step re-exec on retry.
2. ~~D10~~ — mutator missing tree → force fresh; `isolation=none` may resume.
3. ~~StepSession~~ — side file first; journal event only if fold needs it.
4. ~~needs_input~~ — Spec 21 (not recovery-first fake question UX in this MVP).
5. ~~Naming~~ — keep `Manager.Resume`.

---

## Success criteria

- Kill jig during a running agent step → restart jig → `R` → recovery gate →
  **retry** completes the workflow.
- Same path with Claude SDK + durable session → **resume** continues the
  conversation (new message) and completes.
- Review-only unfinished runs still resume unchanged.
- Mixed review + interrupted worker resumes both parks.
- Finished runs still refuse Resume.
- Runs with `needs_input` / `stopped` / `awaiting_integration` (alone or mixed)
  refuse Resume with a Spec 21-pointing error until Spec 21 ships.
- Cursor / Claude ACP interrupted steps offer retry without advertising false
  CanResume (live and crash).
- Monitor no longer shows virtual `RunFinished` for reopenable interrupted runs.
- Persistence-off tests remain green; no session files written.
- Schema + engine-design no longer list mid-execution crash recovery as
  unspoken "display fail forever."

---

## Related docs

- [`docs/plans/open-goals.md`](../../plans/open-goals.md) — A3
- [`docs/engine-design.md`](../../engine-design.md) — journal, Deferred
- [`docs/workflow-schema.md`](../../workflow-schema.md) — Failure recovery; MVP 1
- [`docs/specs/07-spec-stop-resume-step/`](../07-spec-stop-resume-step/) — live stop/resume
- [`docs/specs/19-spec-headless-run/19-implementation-plan.md`](../19-spec-headless-run/19-implementation-plan.md) — D12 resume deferral
- [`docs/specs/21-spec-unfinished-park-reopen/21-implementation-plan.md`](../21-spec-unfinished-park-reopen/21-implementation-plan.md) — full park reopen
- [`docs/adr/0002-gates-are-nonblocking-focus-regions.md`](../../adr/0002-gates-are-nonblocking-focus-regions.md) — multi recovery
- [`docs/adr/0008-manual-reset-rewind-and-replay.md`](../../adr/0008-manual-reset-rewind-and-replay.md) — crash-consistent ordering (reset, not reopen)
- [`docs/headless.md`](../../headless.md) — future `jig resume` (A2)
