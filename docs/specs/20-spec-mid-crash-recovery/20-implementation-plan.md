# Implementation Plan: Mid-execution crash recovery

**Status:** Plan proposed (open goal A3 — P0) — decisions open for lock
**Depends on:** Journal-before-fan-out (`docs/engine-design.md` §3); review-gate
`Manager.Resume` (Spec 05/07 path); early `EventSessionID` capture (Spec 07 B2);
TOML-only harness selection (Spec 14); headless `jig run` settle contract
(Spec 19)
**Complements:** A2 thin ops CLI (`jig resume` / `status`); A4/A5 Cursor + Claude
ACP `CapSessionResume` parity
**Breaks:** Display-only `reconcileInterruptedRun` semantics for ownership; the
review-gate-only `Manager.Resume` contract. Pre-v1: prefer the correct long-term
reopen model over keeping synthetic "failed forever" as the product answer.

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

| Durable truth | Display / ownership lie |
|---|---|
| `journal.jsonl` ends on `StepStatus{To: running}` (or `validating`) | `ReplayJournal` synthesizes `running→failed` + `RunFinished{Failed:true}` for the monitor |
| Partial `transcript.jsonl` may exist | Runs list marks *any* unfinished historical run `paused` |
| No `result.json` (terminal-only write) | Operator hits `R` → `"only quiescent review gates can resume"` |
| No durable `SessionID` while `running` | Session id lived only in memory / post-terminal `Result` |

Open-goals ranks this **#11** cross-cutting / **P0 product** (`docs/plans/open-goals.md` A3).
Engine design deferred it explicitly: crash-recovery/resume becomes "replay the
journal" with no redesign (`docs/engine-design.md` load-bearing #3; Deferred §).
Schema MVP-1 deferred "arbitrary in-flight agent checkpoint/resume" and notes
interrupted workers remain recovery cases (`docs/workflow-schema.md` MVP 1).

Without A3, long agent runs are **one kill -9 / OOM / laptop sleep away from a
permanently wedged or falsely-terminal run** that the operator cannot reopen.

---

## Goal

Make an unfinished run whose workers died with the process **reopenable** under
the same mental model as review-gate resume:

1. **Detect** interrupted in-flight steps from durable journal state (not from
   virtual display events).
2. **Restore** a live scheduler (`Manager.Resume` generalized, or a sibling
   entry point) under `scheduler.lock`.
3. **Park** each interrupted step for an operator decision — prefer the existing
   **recovery gate** (`awaiting_recovery` + `RecoveryRequest`) over inventing a
   second UX.
4. **Offer** `retry` (fresh) always; `resume` (continue agent session) when a
   durable SessionID exists **and** the harness advertises `CapSessionResume`;
   `skip` / `abort` unchanged.
5. **Preserve** file-is-truth: transcripts, worktrees, and journal remain the
   audit trail; recovery actions append new durable events rather than rewriting
   history.

Headless / ops CLI (`jig resume <run-id>`, Spec 19 D12 / A2) should be able to
call the same engine API; TUI `R` is the interactive client.

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
| Display crash reconcile | Virtual `running→failed` + `RunFinished` | `reconcileInterruptedRun` — **not durable** |

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
| API | `Run.Stop` / `Run.Resume` | Generalized `Manager.Resume` (or sibling) |
| Semantics | New message into session | Same — new message, not mid-turn rewind |

---

## Product contract

### Operator story (TUI)

1. Run dies mid-agent (kill, crash, power loss).
2. Operator restarts `jig`, opens Runs.
3. Interrupted run shows as **paused / interrupted** (not falsely finished).
4. `R` (Resume) acquires `scheduler.lock`, restores workflow snapshot + journal
   fold, parks each interrupted step on the **recovery gate**.
5. Gate offers:
   - **retry** — fresh agent/command (clear resume maps; bump Attempt)
   - **retry with guidance / resume** — only if durable SessionID +
     `CapSessionResume`; compose recovery message like live `Recover(resume)`
   - **skip** / **abort** — unchanged
6. Sibling steps that were already terminal stay terminal; review-parked steps
   remain review-parked (existing path). Mixed runs (review + interrupted
   workers) restore **both** park kinds.

### Operator story (headless / A2)

```bash
# After CI agent host OOM'd mid-step:
jig resume <run-id> --on-recovery retry   # or abort|skip; resume optional later
# Same engine API as TUI R; policy settles like Spec 19
```

MVP may ship TUI-only reopen and leave `jig resume` to A2, but the **engine API
must be headless-callable** (no Bubble Tea).

### Status vocabulary

Do **not** add a new `StatusCrashed` unless forced. Prefer:

1. On reopen, transition durable `running` / selected non-terminal in-flight
   statuses → `awaiting_recovery` with a clear `Err` string
   (e.g. `"process exited while step was running"`).
2. Keep `reconcileInterruptedRun` for **read-only historical views that never
   reopen** — or retire it once reopen is the default path for unfinished
   interrupted runs (decision D7).

### What counts as "interrupted" on reopen

| Last durable status | Reopen action |
|---|---|
| `running` | Park recovery (A3 primary) |
| `validating` | Park recovery (validate interrupted; retry re-runs validate or full step — D9) |
| `awaiting_review` | Existing review restore |
| `needs_input` | Park recovery (block_on / AskUserQuestion lost with process; session may still resume) |
| `awaiting_recovery` / `awaiting_integration` / `stopped` | Restore live gate / stopped park (extend resume allow-list) |
| `pending` / `succeeded` / `skipped` / `failed` | Keep |
| Journal already has `RunFinished` | Reject resume (settled; A12) |

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

- Write as soon as `EventSessionID` arrives (and on each non-empty update).
- Clear / rewrite on fresh retry (new attempt) so stale ids cannot resume.
- Persistence-off: path empty → no-op (first-class).
- Prefer a **side file** over expanding `result.json` mid-flight so terminal
  result materialization stays terminal-only.
- Optionally also emit a journal event `StepSession` for fold/replay visibility
  (D3). Side file alone is enough for reopen; journal event keeps
  `state = fold(journal)` honest for SessionID.

Runner → engine signal: either

- engine already receives harness events only through the executor result today
  — so the **runner** should write `session.json` via datastore helpers when
  `TranscriptPath` / run dir is set, **or**
- introduce a lightweight `Reporter` / progress callback for SessionID.

Prefer **runner writes session.json** (mirrors transcript append: file is truth;
engine reads on reopen). Engine does not need a live event for crash durability.

### Generalized `Manager.Resume`

Expand `restoreReviewCheckpoint` into a broader **restore unfinished checkpoint**:

```
Manager.Resume(runID, legacyWorkflow)
  → load workflow snapshot
  → ReplayJournalRaw (durable only)
  → classify steps:
       review-parked → restore ReviewRequest + docs (existing)
       interrupted in-flight → transition to awaiting_recovery + RecoveryRequest
       awaiting_recovery / stopped / needs_input / awaiting_integration → restore park
       terminal → keep
  → restore run branch / worktree prune (existing)
  → acquire scheduler.lock
  → start scheduler loop from restored state (do not re-dispatch until Recover/Resolve)
```

Reject only when:

- run already has a live scheduler
- journal shows `RunFinished`
- workflow snapshot mismatch (existing)
- no reopenable park and no interrupted workers (nothing to do)

Flip `TestResumeRejectsInterruptedWorker` into
`TestResumeParksInterruptedWorkerOnRecovery`.

### Relationship to `reconcileInterruptedRun`

| Consumer | Today | After A3 |
|---|---|---|
| Ownership / Runs hydrate (`ReplayJournalRaw`) | Unfinished → `paused` | Unchanged raw; UI copy distinguishes review-paused vs crash-interrupted |
| Monitor historical (`ReplayJournal`) | Virtual fail + finish | Prefer: if reopenable, show unfinished + interrupted banner; virtual fail only for **orphaned** runs that cannot reopen (corrupt lock? missing snapshot?) |
| `Manager.Resume` | Rejects interrupted | Accepts + parks recovery |

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

1. Prune/recreate run worktree as today (`restoreRunBranch`).
2. For interrupted mutators: keep the existing step worktree if present; do not
   `git reset --hard` unless the operator chooses **retry** (fresh) — **resume**
   should continue against the same dirty tree when possible.
3. If the worktree is missing, degrade `CanResume` presentation: still allow
   session resume but warn; or force fresh retry (D10).

### Headless policy hook

Spec 19 `--on-recovery` already maps `RecoveryRequest` → native `Recover`.
Crash reopen that emits `RecoveryRequest` automatically plugs into headless
once A2/`jig resume` starts the scheduler. No new gate kind required.

---

## Decision map

| # | Topic | Proposed decision | Status |
|---|---|---|---|
| D1 | Product shape | Reopen interrupted runs via generalized `Manager.Resume`; park recovery gate — do not auto-finish as failed | proposed |
| D2 | UX | Reuse recovery gate actions; no new panel | proposed |
| D3 | Session durability | Side file `steps/<id>/session.json` written on first `EventSessionID`; optional journal `StepSession` in same change if fold honesty needs it | proposed |
| D4 | Who writes session.json | `internal/runner` (agent path) via datastore helper; engine reads on resume | proposed |
| D5 | Harness without CapSessionResume | Offer retry/skip/abort only (`CanResume=false`) | proposed |
| D6 | Command/check interrupt | Recovery with retry (= re-exec); no resume | proposed |
| D7 | `reconcileInterruptedRun` | Keep for non-reopenable orphans only; Runs/Monitor treat reopenable unfinished runs as paused/interrupted, not finished | proposed |
| D8 | Mixed parks | One Resume restores review + recovery + stopped + integration parks together | proposed |
| D9 | `validating` interrupt | Park recovery; **retry** re-enters validate using last attempt outputs if present, else full re-exec | open |
| D10 | Missing worktree on resume | Prefer warn + allow session resume; if tree required for mutator, force fresh retry | open |
| D11 | Auto policy on reopen | Never auto-Recover on TUI reopen; headless `jig resume --on-recovery` may act (A2) | proposed |
| D12 | CLI surface in this spec | Engine + TUI `R` in scope; `jig resume` CLI deferred to A2 (API ready) | proposed |
| D13 | A4/A5 ordering | A3 ships degrade-closed; A4/A5 unlock `CanResume` for those harnesses without A3 changes | proposed |
| D14 | Err string | Stable sentinel distinct from Spec 07 stop / live failure for tests + UI | proposed |
| D15 | Attempt counter | Crash reopen does not bump Attempt until operator picks retry/resume | proposed |
| D16 | Parallel interrupted siblings | Each interrupted step gets its own `RecoveryRequest`; run stays alive (ADR 0002 non-blocking gates) | proposed |
| D17 | Docs | Update workflow-schema MVP deferred sentence; engine-design Deferred; open-goals A3 link; CONTEXT.md resume note | proposed |

---

## Phased implementation

### Phase 0 — Spec lock (this document)

- [ ] Confirm D1–D17 (resolve D9, D10)
- [ ] Agree Err sentinel string + session.json schema
- [ ] Confirm mixed-park Resume is in MVP (D8) vs review-only+running-only

### Phase 1 — Durable SessionID (prerequisite)

**Packages:** `internal/datastore`, `internal/runner`, tests

- [ ] `datastore.SessionPath(runDir, stepID)` + write/read/clear helpers
- [ ] Agent runner: on first non-empty `EventSessionID`, write `session.json`
- [ ] Clear session file when dispatching a fresh attempt (no `ResumeSessionID`)
- [ ] Persistence-off no-op tests
- [ ] Unit test: kill-style cancel still leaves session.json on disk

**Exit criteria:** A live Stop/crash simulation leaves `session.json` readable
after process-equivalent teardown; no engine Resume changes yet.

### Phase 2 — Engine reopen for interrupted workers

**Packages:** `internal/engine`

- [ ] Generalize checkpoint restore allow-list
- [ ] On Resume: for each interrupted status, durable transition →
      `awaiting_recovery` + emit `RecoveryRequest` (with `CanResume` from
      session.json + harness capability lookup via step backend/transport)
- [ ] Load SessionID into `state.Result` / `resumeSessions` so `Recover(resume)`
      works unchanged
- [ ] Replace/extend `TestResumeRejectsInterruptedWorker`
- [ ] Tests: review-only still works; running-only works; mixed works;
      finished still rejected; lock contention still rejected
- [ ] Adjust `reconcileInterruptedRun` per D7 so ownership UI is coherent

**Exit criteria:** `go test ./internal/engine` — interrupted journal can Resume
and park recovery without a live worker.

### Phase 3 — TUI copy + Gate wiring

**Packages:** `internal/tui`

- [ ] Runs list: distinguish "paused (review)" vs "interrupted (crash)" using
      raw journal (not virtual finish)
- [ ] Resume key `R` already calls `Manager.Resume` — verify interrupted path
- [ ] Monitor: while historical+interrupted before resume, show banner; after
      resume, live recovery overlay
- [ ] Footer / help text for interrupted runs
- [ ] Golden / model tests for list + gate

**Exit criteria:** Manual dogfood: start agent step, `kill -9` jig, reopen,
`R`, recover retry and resume (Claude SDK).

### Phase 4 — Docs + headless hook readiness

- [ ] `docs/workflow-schema.md` — rewrite MVP deferred sentence; document crash
      reopen + session.json
- [ ] `docs/engine-design.md` — move crash-recovery out of Deferred; describe
      Resume classes (review / interrupted / mixed)
- [ ] `CONTEXT.md` — crash reopen vs Stop/Resume
- [ ] `docs/headless.md` — note A2 `jig resume` will consume RecoveryRequest
- [ ] `docs/plans/open-goals.md` — A3 → planned (Spec 20)
- [ ] Example or script under `examples/` optional smoke (command step kill)

### Phase 5 (optional follow-on) — A2 CLI

Out of scope for Spec 20 implementation commits, but API-complete after Phase 2:

- `jig resume <run-id> [--on-recovery …] [--root …]`
- `jig status <run-id>` shows `interrupted` vs `paused_review`

---

## Testing plan

| Layer | Cases |
|---|---|
| datastore | session write/read/clear; empty runDir no-op |
| runner | SessionID event → file; fresh retry clears file; resume dispatch keeps file |
| engine | Resume interrupted agent; Resume command; mixed review+running; reject finished; CanResume true/false; Recover resume uses durable id; Recover retry clears maps + session file |
| replay | Raw unfinished; display reconcile only for orphans |
| tui | Runs interrupted badge; R succeeds; recovery overlay |
| harness | Fake without CapSessionResume → CanResume false |
| integration | Optional: start real Claude SDK step, kill, resume (manual / nightlies) |

Race: `go test ./internal/engine/... -race` remains the gate (Spec 13).

---

## Risks

| Risk | Mitigation |
|---|---|
| Backend session dead after long outage even with CapSessionResume | Recover(resume) fails → re-enter recovery with CanResume maybe still true; operator picks retry; document TTL unknowns |
| Stale session.json after partial write | Write atomically (temp + rename); ignore empty/corrupt on read |
| Operator expects exact mid-turn restore | Docs + gate copy: "continues with a new message" (Spec 07) |
| Virtual RunFinished taught users runs are dead | D7 UI + migration note in open-goals |
| Mutator dirty tree vs resume | D10; prefer keep tree on resume |
| A4/A5 lag | Degrade to retry; do not block A3 |
| Double scheduler | Existing `scheduler.lock` + Manager live-map check |

---

## Open questions (resolve in Phase 0)

1. **D9** — On `validating` interrupt, is retry "re-run validate only" or
   "re-exec the whole step"? (Lean: whole step for MVP simplicity.)
2. **D10** — Missing step worktree: force fresh retry, or allow session-only
   resume in original checkout? (Lean: force fresh retry for mutators;
   isolation=none may resume.)
3. **Journal `StepSession` event** — require in Phase 1, or side file only until
   a consumer needs fold? (Lean: side file in Phase 1; add journal event if
   Resume fold needs it without touching datastore.)
4. **Should `needs_input` crash reopen auto-re-emit `InputRequest` /
   `AgentQuestion` instead of recovery?** (Lean: recovery first — the waiting
   agent process is gone; resume session + re-ask is Recover(resume).)
5. **Naming** — keep API `Manager.Resume` vs `Manager.Reopen`? (Lean: keep
   `Resume` to match TUI `R` and Spec 19 D12.)

---

## Success criteria

- Kill jig during a running agent step → restart jig → `R` → recovery gate →
  **retry** completes the workflow.
- Same path with Claude SDK + durable session → **resume** continues the
  conversation (new message) and completes.
- Review-only unfinished runs still resume unchanged.
- Finished runs still refuse Resume.
- Cursor / Claude ACP interrupted steps offer retry without advertising false
  CanResume.
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
- [`docs/adr/0002-…`](../../adr/) — non-blocking gates (multi recovery)
- [`docs/adr/0008-manual-reset-rewind-and-replay.md`](../../adr/0008-manual-reset-rewind-and-replay.md) — crash-consistent ordering (reset, not reopen)
- [`docs/headless.md`](../../headless.md) — future `jig resume`
