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

Strict reopen requires the version-1 authoring manifest and every captured
asset, including future routes and fan-out templates. Missing, corrupt,
unsupported, or incomplete captures refuse execution before workers start.
Start a new run from authoring TOML; edited checkout files cannot repair a
captured run. Older supported snapshots remain inspectable but cannot execute
without this capture contract. Persistence-off runs have no durable reopen.

Once reopened, the same supervisor, gate policy, output envelope, timeout, and
exit mapping as `jig run` apply.

### Authoring capture failures

`authoring capture: limit`, `forbidden_path`, `unsupported_file`,
`unsupported_platform`, `missing_binding`, `integrity`, and `storage` are
bounded diagnostic reasons. Step-owned file capture errors include the step
and asset kind without source contents or private source paths. Startup
errors appear in Home's notice or CLI stderr. Malformed skill, agent-file,
and producer-schema contents produce bounded `integrity` errors rather than
raw parser lines or private file references. Required storage failures never
silently switch a configured run to persistence-off.

Capture damage detected before dispatch, result acceptance, integration,
completion, or final landing fails the run, cancels active workers, and fences
later contributions. Earlier integrated work remains available on the run
branch; ordinary retry, continue, and recovery cannot bypass the failure.
Do not repair a run from edited checkout assets: start a new run. Capture
verification occurs at these boundaries, not continuously while waiting at a
human gate. Approval of final landing performs another verification.

Persistence-off (`jig run WORKFLOW --root ''`) retains captures in memory,
creates no authoring helper copies, and has no durable reopen. Explicit `run`
commands consume their execution workspace, including upstream integrated
files. The [frozen-authoring example](../examples/frozen-authoring-assets.toml)
demonstrates the distinction. The capture implementation has Linux and macOS
paths; capture qualification covers darwin/arm64 and linux/arm64. Linux was
exercised in an isolated container with networking disabled; this is not
qualification of jig's planned Docker execution runtime.

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
message. A missing/corrupt `workflow.json` does not block export (backend
and step type fall back to `unknown`); a torn or malformed journal
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

## Helper backends

The in-monitor help chat and the Tier-2 security monitors run on Claude unless
`config.toml` selects another backend. Both files are read: the user file
(`$XDG_CONFIG_HOME/jig/config.toml`, else `~/.config/jig/config.toml`), then
the project file (`<root>/config.toml`), with each non-empty project key
overriding the user key. `jig config show` prints the merged result.

```toml
[helpchat]
backend = "codex"        # claude | cursor | codex; default claude
model   = "gpt-5-codex"  # optional

[security]
monitor_backend = "cursor"  # claude | cursor | codex; default claude
monitor_model   = ""        # optional
```

When a model is unset, Claude uses `claude-haiku-4-5-20251001` for both
helpers; Codex and Cursor send no model, so the backend's own default applies.
The chosen backend needs its own working login, just like a workflow agent on
that backend.

Any other backend value makes config loading fail. The error names the key
(`helpchat.backend` or `security.monitor_backend`) and lists the accepted
values; it never echoes the configured value.

The help chat requires partial streaming, the permission callback, and session
resume from its backend. If the backend lacks one, the help panel shows an
"unavailable" message naming the backend and the missing capability, and no
agent session or tool server starts. The monitor requirements are described in
[security monitoring](security-monitoring.md#classifier-isolation-and-prerequisites).

## Notification readiness

`jig notifications check WORKFLOW.toml [--root PATH]` validates author policy and
inspects local notification bindings without sending network requests or
showing desktop notifications. Live delivery, snapshot integrity, and reopen
restoration are implemented (Spec 23); the readiness command remains a pure
inspection surface and never contacts a receiver.

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
The check command sends nothing. The terminal bell is separate: it is a
monitor-only `[tui] bell` setting (see [TUI.md](TUI.md)), not a notification
route.

### Live delivery

The process runtime owns one dispatcher shared by the TUI and every `jig run`,
`jig resume`, and applied `jig reset`. Each attention burst is coalesced for
500 ms per (run, epoch, destination); the resulting summary carries at most
ten sorted descriptors plus a total/omitted count. Send attempts are capped
per attempt (three seconds), per destination (one at a time, plus a
one-second Slack pacing floor), and per message lifetime (thirty seconds
from enqueue). Retries fire up to three times against transient transport
failures and HTTP 408/429/5xx, honoring valid `Retry-After` values; permanent
4xx, TLS validation errors, and non-HTTPS URLs abandon without retry.

Under queue pressure (256 pending destination expansions) an incoming
`run_failed` evicts the oldest non-failure; when only failures remain the
new failure is dropped and diagnostics record the loss with a stable reason
code. Ordinary shutdown (SIGINT/SIGTERM or TUI quit) stops run producers
first, then drains the dispatcher for one total five-second window and
cancels the remainder. Retries and destinations may duplicate an already-
accepted external message when a receiver's confirmation is lost.

### Notification diagnostics

Delivery outcomes are captured in a bounded per-process ring (100 sanitized
entries with identical-failure aggregation). Fields include alias, optional
run ID, event, outcome, reason code, attempt, and aggregate count only;
response bodies, URLs, headers, and secret values are never retained.

- Headless (`jig run`/`jig resume`) drains the ring to stderr after run
  settlement; stdout envelopes and exit codes are unchanged.
- The TUI exposes the same ring behind the `Ctrl+G` chord as a modal
  overlay. The overlay never opens on its own — an arriving failure does
  not steal Gate focus. `Esc` or `Ctrl+G` closes it.
- Diagnostics accumulate across every run started in the same jig process;
  on process exit the ring is not persisted.

### Reopen behavior

At run start jig freezes the resolved policy in the run's immutable
workflow snapshot alongside its own SHA-256 field. Reopen (`jig resume`,
`jig reset`, TUI resume) verifies the digest and restores the policy
verbatim; a mismatch fails reopen. The source profile files are never
re-read, so a changed or deleted profile does not alter a resumed run's
policy. Operator bindings and secrets ARE resolved fresh per reopen — the
same alias can point at a new URL between epochs, but the frozen event
selection stays the same. On active reopen the observer emits at most one
filtered restored summary per (run, epoch, destination). Historical
inspection (`jig status`, journal replay, TUI Runs hydration) creates no
notification producer.

## Complete worker attempts

A worker's `timeout` starts after scheduling admission, before preparation,
and continues through main work, validation, and final diff collection. It does
not restart when the step enters `validating`. Queue waits and human parks are
excluded; live questions count. The scheduler remains available for Stop and
snapshots while the runner executes the validator. Stop parks the step only
after its worker returns; timeout follows the existing failure/retry policy.

ACP initialization, authentication, session open/load, and configuration calls
have finite 30-second sub-deadlines even without a worker `timeout`. The main
agent conversation keeps the original attempt context.

A zero-exit validator must also satisfy every declared output predicate.
`output_schema` enforces captured JSON Schema Draft 2020-12 with document-local
references only. Output predicate reads are bounded to regular files at most
8 MiB. The final isolated contribution includes validator edits and must pass
`mutation_paths`; a rejected contribution is never partially integrated.
Persistence-off validation uses the configured host CWD and cannot roll back
edits. Readers do not integrate their changes.

Managed host command groups are terminated and checked for remaining live group
members before validation. ACP adapter Close uses the same bounded group check;
confirmed killed exits are normal cleanup. Reported uncertain termination keeps
capacity and run-owned storage fenced; retry, resume, skip, and reset cannot
replace or accept it. Abort ends orchestration while retaining the uncertain
storage. These checks do not certify arbitrary detached host descendants or
provide Docker containment. Linux and macOS are the qualified host platforms;
other platforms fail closed when group confirmation is unsupported.

## Trusted sandbox policy

`--sandbox-policy PATH` selects a separate operator-owned policy for bare TUI
startup, `run`, `resume`, applied `reset`, and `doctor`. Separate and `=` values
are accepted; an empty value or a repeated flag is a usage error (exit 2).
`validate` remains structural and rejects operator policy flags.

Without the flag, discovery uses `$XDG_CONFIG_HOME/jig/sandbox-policy.toml`,
then `~/.config/jig/sandbox-policy.toml` when XDG is unset. XDG must be absolute.
There is no project search or `--config` alias. Absent implicit policy permits
host requests and denies Docker. A selected missing, unreadable, unsafe, or
malformed policy fails closed. Policy files must be regular, at most 1 MiB,
owned by the invoking user or root, and not group/world writable. File symlinks
and unsafe ancestors are rejected; root-owned sticky temporary directories
are permitted. Automatic sources inside the project/source tree are rejected;
an explicit path is a deliberate operator designation. Ordinary project-over-user
preference merging cannot change policy authority.

A minimal host denial is:

```toml
version = 1
allow_host = false
```

The optional Docker section requires every list, authentication, and both
complete resource tables shown below. These are synthetic paths and explicit
operator choices, not installed defaults:

```toml
version = 1
allow_host = false
require_all_agents = false

[docker]
allowed_endpoints = ["unix:///operator/docker.sock"]
allowed_repositories = ["docker.io/library/alpine"]
allowed_images = []
require_digest = false
allowed_networks = ["none"]
allow_emulation = false
workspace_roots = ["/operator/projects"]
storage_roots = ["/operator/storage", "/operator/tmp"]
authentication = "none"

[docker.defaults]
cpus = 1
memory = "512MiB"
pids = 64

[docker.ceilings]
cpus = 2
memory = "2GiB"
pids = 256
```

Unknown/duplicate keys fail. Optional booleans default to false. All lists
allow at most 256 unique entries; empty lists deny their category. Image rules
match normalized exact references or exact repository names, with no wildcard
or prefix matching. `require_digest` also applies to repository permissions.
Resources use the workflow grammar, defaults must fit ceilings, and explicit
requests above a ceiling are denied without clamping. Only omitted resources
receive defaults; authored requests remain unchanged.

Docker workspace/source and storage paths must fall beneath canonical allowed
roots, including resolution of existing ancestors of missing child paths.
Empty persistence paths remain absent. These rules do not constrain arbitrary
host filesystem or network access. `authentication = "none"` is the only
supported mode. Docker secrets and agent authentication are unsupported; jig
rejects them before secret resolution or login-store access. A true
`require_all_agents` or positive `hard_storage_bytes` requirement is recognized
but unsupported; zero/negative hard storage is invalid.

Admission checks the complete expanded graph, including skipped steps and
explicit child overrides, before launching work. The selected policy is frozen
for the runtime epoch. Workloads, executable validators, and operator-requested
integration resolution pass the same authorization boundary. Host help and
Tier-2 services remain outside that boundary. Policy permission does not enable
Docker execution: the permanent `sandbox_runtime_unsupported` check still
rejects every Docker workload. Doctor can inspect metadata for an authorized local deployment; this does not
certify resource enforcement.

Doctor reports `sandbox.policy`, bounded reason/field/source identifiers, and
`sandbox.scope` auxiliary-host disclosure. Policy denial returns exit 1 and
skips executable/secret readiness checks. Host tool availability is never used
as evidence of tools inside a Docker image.

### Policy on reopen and recheck

Reopen verifies the captured workflow and authoring assets, then reloads current
policy using the saved source selector. A previously selected policy file that
is missing, unsafe or malformed blocks the run; it never falls back to another
file. A saved built-in policy origin rediscovers the current operator default.
An explicit `--sandbox-policy FILE` on resume reselects a trusted source for that
epoch. Permission is evaluated against the captured request and effective
resources; changed defaults do not resize work, and fingerprint changes alone
do not deny permission. The successful policy is frozen until the next epoch.

Denial journals `sandbox_policy_blocked` before publishing it. This run-level
block overlays recovery, review, input and integration state. It fences worker,
validator, secret and integration-resolver calls, plus reset, skip and session
resume. Monitor shows **recheck policy** and **abort run**. Rechecks coalesce
while pending, run outside the scheduler with a ten-second deadline, and carry
a block epoch so stale results cannot release a newer block. Abort remains
responsive. A grant is journaled before workspace restoration and leaves
underlying human gates pending. A journal failure keeps execution fenced.

Headless resume with `--on-recovery=retry` attempts one explicit policy recheck.
Persistent denial aborts and exits 1; abort, skip and resume policies also exit 1
without bypassing it. Ordinary human gates still exit 3 after policy permits
the run. Status and historical Monitor replay stored evidence without loading
policy or contacting Docker. Export includes only aliased sandbox profile,
runtime, recognized authored platform, and typed policy reason summaries.

Docker execution remains unsupported, including after a successful policy
check. Synthetic complete Docker captures exercise binding/identity checks;
real Docker runs cannot be started by this release. A bound inspection uses
the original canonical socket and daemon identity rather than current context
selection. No metadata report certifies container enforcement.

### Docker endpoint selection

`--docker-context NAME`, `--docker-host URI`, and `--docker-config DIR` are
operator flags for TUI startup, run/resume/applied reset, and doctor. They accept
separate or `=` values, reject empty/repeated values, and context/host flags are
mutually exclusive. `validate` rejects them. Host-only workflows without these
flags skip Docker configuration and endpoint selection completely.

Endpoint precedence is explicit context/host, `DOCKER_CONTEXT`, `DOCKER_HOST`,
config `currentContext`, then virtual `default`. A non-default context ignores
ambient `DOCKER_HOST`; virtual `default` uses it when present, otherwise
`unix:///var/run/docker.sock`. An explicit host wins over ambient context.
Config-root precedence is explicit `--docker-config`, `DOCKER_CONFIG`, then
`~/.docker`. CLI relative directories resolve against process-entry CWD;
environment roots must be absolute. Automatic config sources inside the project
are rejected. Only needed config projections are read; a missing default home
`config.json` means no active context. Invalid winning sources never fall back.

Config and selected context metadata are bounded regular JSON files (1 MiB,
64 nesting levels). Duplicate keys, trailing JSON, and wrong selected types
fail. Only current context, context name, Docker host, and SkipTLSVerify are
projected; credential/helper/TLS stores are not initialized or enumerated.
Only local Unix sockets on Linux/macOS are supported. Selected and permitted
socket symlinks resolve canonically before comparison; missing or forbidden
sockets fail. Selection records endpoint/context/config provenance and pins
socket identity; replacement is an error, never a new automatic selection.

Doctor may select a policy-permitted Docker deployment even while execution is
unsupported. Doctor dials only the authorized local Unix socket through the
read-only inspection boundary below. Runtime rejects Docker
before deployment selection; explicit flags on a host run still require an
allowed endpoint. TLS, headers, API/platform overrides, and credential
configuration cannot alter the selected transport.

### Read-only doctor inspection

Default doctor storage inspection reads metadata and directory access; it never
creates/removes a probe file and reports writability as unverified. Add
`--probe-store-write` to explicitly create, close and remove a temporary file in
the nearest existing storage ancestor. The flag never creates a missing root or
grants any Docker mutation permission. Creation, close and cleanup failures
remain readiness failures.

Authorized deployment inspection uses the pinned Moby SDK with explicit
transport settings and disabled telemetry export. It permits only HEAD/GET
ping, GET version/info, and GET exact selected image-inspect paths. Budgets are
10 seconds total, 2 seconds per dial, 4 MiB response bodies (including errors),
64 KiB response headers, 36 wire requests and 32 distinct images. Requests use
fresh connections to avoid hidden transport retries. Only a method-unsupported
HEAD ping can fall back to GET; redirects and other failed ping retries are
blocked. Context cancellation applies to every request.

The version-1 `sandbox` JSON summary separates structure, policy, endpoint,
capabilities and execution support. A completed binding records the selected
socket, Linux daemon ID, negotiated API (1.40–1.54) and known native platform.
Missing capability fields remain unknown; false remains false. Rootless compute
readiness requires reported cgroup v2/systemd support. Reported capabilities
never prove enforcement. Non-native platforms require emulation permission and
remain unverified. Image absence with `pull="never"` fails; otherwise acquisition
is reported as required, without pulling. Remaining images beyond the cap stay
uninspected. Environment, labels, entrypoints and raw daemon warnings are omitted.
Docker workflows still report `sandbox_runtime_unsupported` and exit 1 even
when individual metadata checks pass.
