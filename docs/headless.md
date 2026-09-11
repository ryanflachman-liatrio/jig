# Headless `jig run`

Non-interactive / CI runner for a workflow TOML. The TUI (`jig` with no
subcommand) remains the interactive operator surface; `jig run` is the
automation surface. Both share the same engine, runners, harnesses, journal,
and `.jig/runs/<id>/` layout.

Plan: [`docs/specs/19-spec-headless-run/19-implementation-plan.md`](specs/19-spec-headless-run/19-implementation-plan.md).

## Quick start

```bash
# Recommended unattended / CI recipe
jig run examples/headless-smoke.toml --ci
echo $?   # 0 = RunFinished && !Failed

# Local script with progress (mutating run that should land on base)
jig run .agents/jig/research.toml --approve-merge --timeout 30m

# Machine consumers
jig run examples/headless-smoke.toml --output json --discard-merge --timeout 10m
jig run examples/headless-smoke.toml --output jsonl --discard-merge --timeout 10m \
  | jq -c 'select(.type=="step_status")'
```

Fail-closed is **inherent** to `jig run` — there is no `--fail-on-gate`. Human
gates (review, `from=user`, `block_on`, AskUserQuestion) Cancel the run and
exit **3**. Never blocks on stdin.

## Flags

```text
jig run <workflow.toml> [flags]

  --root DIR                 Persistence root (default: .jig)
  --output, -o text|json|jsonl
  --quiet, -q                Progress off (keep run id, errors, final output)
  --timeout DURATION         Wall-clock cancel (e.g. 45m); 0 = none
  --ci                       Unattended preset (prints effective flags once)
  --approve-merge            Answer FinalMergeRequest with approve
  --discard-merge            Answer FinalMergeRequest with discard
  --on-recovery abort|retry|skip   (default: abort; resume omitted)
  --on-conflict abort        (default: abort; abort→recovery cascade; no agent)
```

### `--ci` expansion

Printed once on stderr when `--ci` is set:

```text
--output json --discard-merge --on-recovery abort --on-conflict abort --timeout 45m
```

Operators may override individual flags after `--ci` (last explicit flag wins
for that option). Example: `jig run wf.toml --ci --timeout 10m` keeps json +
discard-merge + abort policies but uses a 10m timeout.

`--ci` only auto-**discard**s final merge — it never auto-approves human review.

### Precedence notes

| Concern | Rule |
|---|---|
| Merge | `--approve-merge` and `--discard-merge` are mutually exclusive; `--ci` sets discard unless either merge flag was explicit |
| Recovery | Default `abort`; `--on-recovery retry\|skip` overrides (including under `--ci`) |
| Conflict | Only `abort` is accepted; `agent` is TUI-only |
| Timeout | Default unbound (`0`); `--ci` supplies `45m` unless `--timeout` was explicit |

## Streams & `--output`

| Stream | Contents |
|---|---|
| **stdout** | `json` / `jsonl`: machine data only (including structured failures). `text`: single final summary line |
| **stderr** | Progress (`step_id status`), start-time warnings, early `run_id`, errors, `--ci` expansion, notification diagnostics |

`--quiet` / `-q` suppresses progress lines only. Early `run_id`, warnings/errors,
notification diagnostics, and the final stdout payload still emit.

### Notification diagnostics

After run settlement the shared dispatcher's bounded diagnostic ring is
drained to stderr (fixed-order lines, one per sanitized outcome, safe-field
allowlist only — alias, optional run ID, event, outcome, reason code,
attempt, aggregate count). This runs regardless of `--quiet` and never
touches stdout, so JSON/JSONL envelopes and exit codes stay byte-compatible
whether delivery is enabled, disabled, or failing. See
[Notification readiness](operations.md#notification-readiness) for
enablement, secrets, and troubleshooting.

Cancellation cause classification affects notification event selection:

- Ordinary engine/step failure and recovery-abort → `run_failed` fires.
- `--timeout` wall-clock expiry → `run_failed` fires (typed as a timeout
  cause in diagnostics).
- Fail-closed policy rejection (unexpected human gate, merge-without-flags)
  → `run_failed` fires (typed as a policy rejection cause).
- SIGINT/SIGTERM operator interruption → treated as deliberate
  cancellation and suppressed (no `run_failed` fires).

Successful completion requires explicit `run_succeeded` selection in the
workflow's notification policy.

### Flat JSON envelope (frozen)

```json
{
  "ok": true,
  "run_id": "20260906-221500-a1b2c3d4",
  "workflow": "headless-smoke",
  "failed": false,
  "total_cost_usd": 0.42,
  "total_tokens": 12000,
  "run_dir": ".jig/runs/20260906-221500-a1b2c3d4",
  "error": null
}
```

`jsonl` emits NDJSON progress/ctrl lines and always ends with a final
`{"type":"result","data":{…envelope…}}` line (including on failure).

## Exit codes (frozen)

| Code | Meaning |
|---|---|
| 0 | `RunFinished && !Failed` |
| 1 | Run finished failed (step/harness/auth, recovery-abort, …); **also** load/validate error before Start |
| 2 | Usage / flag parse / wrong arity (never started) |
| 3 | Unexpected human gate / merge-without-policy fail-closed |
| 4 | `--timeout` wall clock |
| 130 | SIGINT after Cancel + settle |
| 143 | SIGTERM after Cancel + settle |

## Gate settle table

| Event | Default headless behavior | Flags |
|---|---|---|
| Review / `from=user` prompt / `block_on` input / AskUserQuestion | Typed gate error → `Cancel()` → wait settle → exit **3** | (later: `--auto-review` / `--inputs-file`) |
| `RecoveryRequest` | Native `Recover` with configured action → exit 1 if run failed | `--on-recovery abort\|retry\|skip` |
| `IntegrationConflictRequest` | Native abort → then recovery cascade → exit 1 | `--on-conflict abort` only |
| `FinalMergeRequest` without merge flags / without `--ci` | Fail closed → exit **3** | — |
| `FinalMergeRequest` + flags / `--ci` | Native `FinalMerge(approve\|discard)` | `--approve-merge` / `--discard-merge` / `--ci` |
| Approve-merge hits conflict / `RunError` | Discard if `--discard-merge`/`--ci`, else fail-closed Cancel + exit 3 | — |

`Snapshot().Done` can be true while FinalMerge is still pending. Headless keys
off `RunFinished` / `Run.Wait`, never `Done` alone.

### Conflict cascade

`ResolveIntegration(…, abort=true)` fails the step into another
`RecoveryRequest`. Headless then applies `--on-recovery`. Do not claim
`--on-conflict abort` alone settles the run.

### Crash reopen vs `--on-recovery`

Spec 19 `--on-recovery` settles gates **during** an active `jig run`; it does
not itself reopen a dead process. `jig resume RUN_ID --on-recovery ACTION` calls
`Manager.Resume` and then hands the restored scheduler to the same headless
supervisor. It accepts automation-safe interrupted/recovery/integration parks;
review, input/question, prompt, stopped, and final-merge parks fail before
mutation and direct the operator to the TUI. See
[`docs/operations.md`](operations.md).

## Authoring CI-safe workflows

- Prefer `jig run … --ci` (or the expanded flag set)
- Prefer `profile = "@autonomous"` on agent steps — **only** disallows
  `AskUserQuestion` when `disallowed_tools` was left empty; it does **not**
  block review / `from=user` / `block_on` / recovery / merge
- Avoid `type = "review"`, `from = "user"`, and `block_on`
- Expect `FinalMergeRequest` when persistence is on, cwd is a git repo, and the
  run branch gained commits — pass `--discard-merge` / `--approve-merge` /
  `--ci`, or use persistence-off / non-git cwd for pure analysis tests
- Use [`examples/headless-smoke.toml`](../examples/headless-smoke.toml) (command +
  check only) as the zero-gate fixture — do not point CI recipes at
  `.agents/jig/bugfix.toml` (has `type=review`)

At start, `jig run` warns on stderr about hostile constructs and reminds about
merge flags when git persistence applies. `jig validate` stays structural;
`jig doctor WORKFLOW.toml --ci` uses the same hostile-gate inventory and also
checks deterministic local prerequisites before tokens are spent.

## Dual-mode with the TUI

1. `jig run wf.toml …` prints `run_id` immediately on stderr
2. Open bare `jig` → Runs screen to inspect the same journal / transcripts
3. Settled-failed runs are prune-eligible (`jig prune`); a wedged pre-finish
   hang is a bug — headless must always settle after Cancel

## Related

- Workflow schema: [`docs/workflow-schema.md`](workflow-schema.md)
- Engine design: [`docs/engine-design.md`](engine-design.md)
- Open goal A1: [`docs/plans/open-goals.md`](plans/open-goals.md)
- Operations CLI: [`docs/operations.md`](operations.md)
