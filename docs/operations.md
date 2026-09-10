# Operations CLI

`jig` exposes persisted run state without starting the TUI. All operations use
the same `.jig/runs/<run-id>/` journal, captured workflow, scheduler lock, and
step transcripts as the engine.

## Scaffold a project

```bash
jig init
jig init --template starter --name feature-review --dir ../project
jig init --dry-run
```

`init` creates a workflow before any TUI or agent starts. The default
`minimal` template writes `.agents/jig/<name>.toml` and, when needed, a single
`.jig/` entry in the target's `.gitignore`. It contains only command and review
steps, so it requires no agent credential and spends no tokens. The `starter`
template additionally writes `.agents/skills/draft/SKILL.md`, which satisfies
its agent step's skill reference.

`--template NAME` selects `minimal` (the default) or `starter`; use
`--list-templates` to print their descriptions without planning or writing.
`--name NAME` sets the safe workflow identifier and filename, while `--dir
PATH` selects the target directory. `--force` replaces scaffold workflow or
skill files that already exist. Without it, `init` lists every collision on
stderr, writes nothing, and exits 1. `--dry-run` prints `would create` or
`would overwrite` lines and exits without modifying files; it takes precedence
over `--force`.

On success, stdout lists every created or overwritten path followed by `jig
validate`, `jig run`, and `jig doctor` next steps. The emitted workflow is
loaded through the normal validator before `init` reports success.

## Inspect runs

```bash
jig status
jig status 20260908-185703-m2u5u98s
jig status --output json --root .jig
```

List output is newest-first. Detail output includes workflow-order step state,
attempt/iteration/generation, latest error and subtype, cumulative cost/tokens,
and the current wait reason. State is derived from raw journal records and the
advisory lock: `active`, `interrupted`, `paused`, `succeeded`, `failed`,
`orphaned`, or `corrupt`. JSON list and detail use the same object shape.

Run IDs are identifiers, not paths. Absolute paths, separators, dot segments,
symlink run targets, missing runs, and non-directory targets are rejected
without creating anything.

## Read transcripts

```bash
jig logs RUN_ID
jig logs RUN_ID --step implement --tail 50
jig logs RUN_ID --follow --output jsonl
jig logs RUN_ID --all --include-thinking
```

The default is the newest 200 well-formed entries across all declared steps.
`--tail N` changes the bound; `--all` is the explicit unbounded option. Entries
from multiple steps sort by timestamp, workflow order, then per-file sequence.
Text is emitted verbatim without Markdown or ANSI interpretation. Thinking is
hidden behind a marker unless `--include-thinking` is set.

JSONL has one wrapper per entry:

```json
{"run_id":"…","step_id":"implement","entry":{"seq":7,"ts":"…","iter":0,"attempt":0,"role":"assistant","blocks":[]}}
```

Follow uses opaque byte cursors and polls append-only files. It discovers late
content, emits no duplicates, drains once after `RunFinished`, and otherwise
continues until SIGINT/SIGTERM. Transcript content may contain model/tool
secrets; this command does not claim retrospective redaction.

## Preflight a workflow

```bash
jig doctor
jig doctor workflow.toml
jig doctor workflow.toml --ci --output json
```

Doctor checks store access, existing run health, required backend executables,
Git worktree readiness, check-step tools, and named `JIG_SECRET_*` variables.
It does not invoke an agent, download an adapter, probe the network, or validate
a login. With `--ci`, review steps, user prompts, `block_on`, and interactive
question capability are failures. CI final merge is safe because the preset
discards it. Each result has stable `id`, `status`, `scope`, `message`, and
`remediation` fields; secret values are never included.

## Resume an unfinished run

```bash
jig resume RUN_ID --on-recovery abort
jig resume RUN_ID --on-recovery retry --discard-merge --timeout 30m
jig resume RUN_ID --on-recovery resume --ci
```

The recovery action is mandatory. Resume reads the immutable `workflow.json`
captured at run start and accepts only interrupted workers, recovery parks, and
integration-conflict parks. Review, prompt, input/question, stopped, and final
merge parks require the TUI and fail before `Manager.Resume` appends anything.
`resume` requires a durable session and backend resume capability. A competing
scheduler wins through `scheduler.lock`; the CLI never attaches to its inbox.

Once reopened, the same supervisor, gate policy, output envelope, timeout, and
exit mapping as `jig run` apply.

## Reset to a step

Preview is the default and is read-only:

```bash
jig reset RUN_ID --to plan
```

It prints the target plus all transitive dependents, their current statuses,
and parks outside that closure. It does not lock, append, create a worktree, or
delete files.

Apply requires two explicit signals and an automation-safe captured workflow:

```bash
jig reset RUN_ID --to plan --apply --ci
```

The CLI refuses settled, active, orphaned/corrupt histories, unknown targets,
unfinished work outside the closure, and failed CI preflight. After reopening,
the engine acknowledges the target, exact closure, and rewind SHA before the
CLI reports success; execution then continues under the shared headless
supervisor. Reset never means deleting a run. Resetting after `RunFinished` is
still deferred.

## Streams and exits

Machine output is stdout-only. Progress, run IDs, warnings, truncation notices,
and errors use stderr.

| Command | Success | Operational failure | Usage | Gate | Timeout | Signals |
|---|---:|---:|---:|---:|---:|---:|
| `status`, `logs`, `doctor` | 0 | 1 | 2 | — | — | logs follow: 130/143 |
| `resume`, applied `reset` | 0 | 1 | 2 | 3 | 4 | 130/143 |
| `init` | 0 | 1 | 2 | — | — | — |

For interactive gates or a run rejected by unattended control, open bare
`jig` and use the run monitor.

## Deferred diff contract

There is intentionally no `jig diff` yet. Current persistence does not retain
an immutable base plus patch content strongly enough to reconstruct a reliable
historical diff. A later contract must add durable provenance or patch snapshots
instead of guessing from the current checkout.
