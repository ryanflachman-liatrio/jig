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

## Export a run

```bash
jig export RUN_ID --destination ./run-report.zip
jig export RUN_ID --root /path/to/.jig --destination ./run-with-text.zip --include-text
unzip -l ./run-report.zip
unzip -p ./run-report.zip README.md
```

`export` packages one inactive run into a self-contained ZIP a recipient can
read with ordinary `unzip`/JSON tools — no jig installation, backend login,
original checkout, or network access. `--destination` is required and always
names a ZIP file; `--root` defaults to `.jig`. There is no prompt, implicit
upload, overwrite flag, or raw-content mode, and export never contacts a
backend or the network.

**Content modes.** The default `structural` mode contains only aliased
identities, observed state, and the event timeline — no prose, code, tool
payloads, session IDs, workflow source, or Git identifiers. `--include-text`
additionally adds `transcript.jsonl`: best-effort sanitized conversation and
tool text. Text mode is **not** a guarantee of anonymity — it can still
identify people, projects, or contain sensitive content — and both the
archive's `README.md` and stderr say so; review the archive before sharing it
further.

**Selected and excluded sources.** Export reads only `journal.jsonl`,
`workflow.json`, `scheduler.lock` (coordination only), and each declared
step's direct `transcript.jsonl` — never artifacts, fan-out item manifests,
reviews, `input.md`, `session.json`, or any path referenced *inside*
persisted data. Reads use traversal-resistant (`os.Root`) opens; a symlink or
special file at a selected path is a fatal refusal, not a partial export.

**Identity and time.** Every original run/workflow/step/tool identifier is
replaced by a stable alias (`run-1`, `workflow-1`, `step-0001`, scoped
`tool-0001`, ...); the alias map exists only in memory and is never exported.
All times are signed integer milliseconds relative to the first observed
event — never an absolute date — so clock reversal is preserved rather than
clamped.

**Archive contract (`format_version: 1`).** Members are fixed literal names
in a fixed order, with a fixed ZIP timestamp and no source-derived extra
fields:

| Member | Contract |
|---|---|
| `README.md` | Generated summary and privacy/completeness notices; no copied prose. |
| `manifest.json` | Format/redaction-policy versions, content mode, completeness, gaps, fixed-category counters, and per-member byte length/SHA-256 (excluding its own bytes). |
| `run.json` | Alias identities, observed state and whether it is authoritative, relative times, nullable totals, and ordered step records. |
| `events.jsonl` | Journal-order records: sequence, relative time, a known kind or `unknown`, and a closed per-kind field projection — never raw error/reason text. |
| `transcript.jsonl` | Only with `--include-text`: sanitized role/block/tool-correlated conversation text, deterministically ordered. |

**Completeness and state authority.** `manifest.json` distinguishes
`complete` from `partial` with fixed gap reasons (`missing_source`,
`corrupt_source`, `torn_record`, `oversized_record`, `malformed_record`,
`unsupported_semantics`, `invalid_field`, `export_truncated`) plus a source
category and optional step alias/line count — never a raw path or parser
message. A missing/corrupt `workflow.json` does not block export (backend,
transport, and step type fall back to `unknown`); a torn or malformed journal
tail keeps its valid prefix and reports the cut. `run.json`'s
`state_authoritative` is `false` whenever the accepted journal is incomplete
or has no `RunStarted` record — in that case totals are `null`, not a
possibly-wrong number. A run with no safely projectable evidence at all fails
without producing an archive.

**Eligible states.** Settled succeeded/failed, interrupted, paused, and
orphaned runs are eligible once evidence exists. Before reading anything,
export acquires the same non-blocking `scheduler.lock` ownership lease Start
and Resume use; a live scheduler — including one parked at a gate with no
worker running — is refused immediately. Lock unavailability is never treated
as inactivity.

**Limits (no override flag in this version).** 4 MiB per journal/transcript
record, 64 KiB retained text after sanitization (truncated on a valid UTF-8
boundary), 256 MiB cumulative bytes read across all selected evidence, 256
MiB total uncompressed archive content, and 10,000 inspected step
directories. Exceeding a per-record limit ends/skips that record; exceeding a
cumulative limit aborts without publishing an archive.

**Sanitization (text mode only).** Every retained free-text value passes
through one deterministic full-replacement sanitizer: known secret patterns
and high-entropy tokens (the same detectors the live guard uses), original
run/workflow/step identifiers, captured workflow source directory and the
current home directory (boundary-aware, so a short id or path segment is
never rewritten inside a longer one), C0/C1 control and ANSI escape
sequences, then truncation last — so a credential crossing the truncation
boundary is never emitted as an unrecognized prefix. Thinking blocks,
attachments, images/binary content, and unsupported block types become
fixed content-free markers; tool input/output is rendered as sanitized text,
never inserted as raw JSON.

**Publication, streams, and exits.** Export writes a private owner-only
temporary archive in the destination's directory and publishes it with a
no-replace link — a destination that already exists (including a dangling
symlink) is refused untouched, never overwritten. On success, stdout
contains only the destination path; notices (e.g. the text-mode residual-risk
warning, a partial-archive notice) and errors use stderr with fixed
messages, source categories, aliases, and line/count numbers — never raw
source values or parser errors.

## Streams and exits

Machine output is stdout-only. Progress, run IDs, warnings, truncation notices,
and errors use stderr.

| Command | Success | Operational failure | Usage | Gate | Timeout | Signals |
|---|---:|---:|---:|---:|---:|---:|
| `status`, `logs`, `doctor` | 0 | 1 | 2 | — | — | logs follow: 130/143 |
| `resume`, applied `reset` | 0 | 1 | 2 | 3 | 4 | 130/143 |
| `export` | 0 (complete or partial) | 1 | 2 | — | — | 130/143 |
| `init` | 0 | 1 | 2 | — | — | — |

For interactive gates or a run rejected by unattended control, open bare
`jig` and use the run monitor.

## Deferred diff contract

There is intentionally no `jig diff` yet. Current persistence does not retain
an immutable base plus patch content strongly enough to reconstruct a reliable
historical diff. A later contract must add durable provenance or patch snapshots
instead of guessing from the current checkout.

## Notification readiness

`jig notifications check WORKFLOW.toml [--root PATH]` validates author policy and
inspects local notification bindings without sending network requests or
showing desktop notifications. Policy/check support is available; live delivery
and reopen integration remain pending in Spec 23.

The root defaults to `.jig`, like other operational commands. Bindings are read
from `<root>/notifications.toml`; `--root ''` disables file lookup. Missing config
is silently disabled. Global and per-destination enablement both default to
false. For example:

```toml
enabled = true

[[destination]]
id = "desktop"
type = "desktop"
enabled = true

[[destination]]
id = "team-alerts"
type = "slack"
enabled = true
url_secret = "slack-notifications"

[[destination]]
id = "ops"
type = "webhook"
enabled = true
url_secret = "ops-notifications"
bearer_secret = "ops-notifications-token"
```

Aliases must be unique, and this version permits at most one destination of each
`desktop`, `slack`, and `webhook` type. Only webhooks accept `bearer_secret`;
desktop destinations accept neither secret field. Unknown fields are errors.
The operator can disable destinations but cannot add policy events or routes.

Network URLs are complete values in named secrets, never inline config values.
For example, `slack-notifications` resolves `JIG_SECRET_SLACK_NOTIFICATIONS`:
uppercase the reference and replace hyphens with underscores. The notification
feature does not add secrets to child environments. Only requested, enabled
network destinations resolve secrets. HTTPS URLs require a host and cannot
contain userinfo or fragments. Disabled destinations need no secret values.

Exit codes: **0** means locally ready or explicitly disabled/empty, **1** means
invalid workflow/profile/config or failed readiness, and **2** means usage error.
Output lists events, aliases, enablement, and fixed status codes, without secret
values or raw external errors. Malformed/unreadable config disables the invocation;
a missing secret or unavailable desktop prerequisite affects that destination.

macOS checks for `/usr/bin/osascript`. Linux checks for `notify-send` and a
`DBUS_SESSION_BUS_ADDRESS`; libnotify tooling and a notification-capable local
desktop session are needed. Other platforms report unsupported desktop delivery.
These checks cannot verify a running notification service, OS permission,
banner visibility, or that a person read anything. Network readiness checks do
not verify TLS connectivity, receiver acceptance, or Slack channel access.
The check command sends nothing. Terminal bell is a separate feature.
