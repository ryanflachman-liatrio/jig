# Task 02 Proofs - Deterministic structural archive and `jig export` CLI

## Task Summary

This task delivers the `jig export` command and the default `structural`
content mode: a closed, alias-only projection of a run's captured workflow,
accepted journal, and observed step state into a fixed-layout ZIP archive,
published without ever overwriting a competing destination.

## What This Task Proves

- `jig export RUN_ID --destination PATH` produces a real ZIP archive
  (`README.md`, `manifest.json`, `run.json`, `events.jsonl`) that any
  ordinary `unzip`/JSON tool can read, with no jig installation required.
- Every original run/workflow/step identifier is replaced by a stable alias
  (`run-1`, `workflow-1`, `step-0001`, ...); no seeded private run ID,
  workflow name, or secret shape survives in any member or in the ZIP's own
  headers/comments.
- `manifest.json` carries per-member SHA-256 digests and byte lengths (never
  including its own bytes), and `format_version`/`redaction_policy_version`.
- Costs/tokens/attempt/iteration/generation and step dependency edges survive
  the projection using aliases only; unknown/invalid values become `unknown`
  or `null` rather than being copied through.
- Publication uses a private owner-only temporary file plus a no-replace
  link, so a destination that already exists is left byte-for-byte
  untouched and the command exits 2.

## Evidence Summary

- `go test ./internal/runexport/... -run 'TestStructuralArchiveMemberContract|TestClosedProjectionExcludesPrivateData|TestArchivePublicationNoOverwrite|TestProjectEventClosedProjectionOmitsRawErrorAndAliasesSteps' -v` passes.
- `go test ./cmd/jig/... -run TestExport -v` passes: help/usage, arity, flags
  after `RUN_ID`, a real end-to-end structural export, and destination
  collision refusal.
- A real `jig export` CLI run against a synthetic fixture (seeded secret
  `AKIAFAKEFAKEFAKEFAKE`, run id `prod-incident-482`, workflow
  `export-fixture`) produces the exact archive contract below and a
  disclosure scan finds none of the seeded values.

## Artifact: CLI help documents the command contract

**What it proves:** FR-01's discoverability — both content modes, eligible
states, and the local-only/no-overwrite guarantee are documented in `--help`.

**Why it matters:** This is the only author surface; an operator must be able
to learn the whole contract from `--help` alone.

**Command:** `jig export` (no args, prints usage to stderr, exit 2)

```
usage: jig export RUN_ID --destination PATH [--root PATH] [--include-text]

Exports a local, offline diagnostic ZIP archive for one inactive run.

Content modes:
  structural (default)   run/step summary, event timeline, and totals only;
                          no prompts, code, tool payloads, or raw identifiers.
  --include-text          adds transcript.jsonl: best-effort sanitized
                          conversation text. Sanitization is not a guarantee
                          of anonymity -- review the archive before sharing
                          it further.

Eligible run states: succeeded, failed, interrupted, paused, and orphaned
runs with at least one safely projectable journal or transcript record. A
run with a live scheduler (including one parked at a gate) is refused.

This command is local-only: it never uploads, prompts, or contacts a
backend or network service, and it never overwrites an existing destination.
```

**Result summary:** Both modes, eligible states, and the local-only/no-overwrite
guarantee are stated; verified automatically by `TestExportHelpDocumentsContract`.

## Artifact: Structural export CLI run and archive listing

**What it proves:** The full operator flow — one command produces one archive,
readable with ordinary tools.

**Command:**

```bash
jig export prod-incident-482 --root /tmp/exportdemo/.jig --destination /tmp/exportdemo/out.zip
```

**Result summary:** Exit 0; stdout contained only the destination path;
`unzip -l` shows exactly the four fixed structural members with a fixed
(1980-01-01) ZIP timestamp.

```
/tmp/exportdemo/out.zip

Archive:  /tmp/exportdemo/out.zip
  Length      Date    Time    Name
---------  ---------- -----   ----
      588  00-00-1980 00:00   manifest.json
      905  00-00-1980 00:00   README.md
      820  00-00-1980 00:00   run.json
      812  00-00-1980 00:00   events.jsonl
---------                     -------
     3125                     4 files
```

## Artifact: `README.md` and `run.json` contain only aliases

**What it proves:** FR-04/FR-05 — the generated summary and step table use
`run-1`/`workflow-1`/`step-NNNN` throughout, never the seeded original run ID
`prod-incident-482` or workflow name `export-fixture`.

**Command:** `unzip -p out.zip README.md` / `run.json`

```markdown
# jig run export: run-1

- Content mode: `structural`
- Observed state: `succeeded` (authoritative: true)
- Completeness: **complete**
...
| Step | Type | Status | Attempt | Iteration |
|---|---|---|---|---|
| step-0001 | command | succeeded | 1 | 0 |
| step-0002 | agent | succeeded | 1 | 0 |
```

```json
{
  "run_alias": "run-1",
  "workflow_alias": "workflow-1",
  "state": "succeeded",
  "state_authoritative": true,
  "started_at_ms": 0,
  "finished_at_ms": 8000,
  "total_cost_usd": 0.42,
  "total_tokens": 1234,
  "steps": [
    {"alias": "step-0001", "type": "command", "backend": "claude", "transport": "sdk", "status": "succeeded"},
    {"alias": "step-0002", "type": "agent", "backend": "claude", "transport": "sdk", "status": "succeeded",
     "cost_usd": 0.42, "tokens": 1234, "depends_on": ["step-0001"]}
  ]
}
```

**Result summary:** Times are relative milliseconds from the first event; the
dependency edge `agent-1 depends_on fetch` survives as `step-0002 depends_on
step-0001`.

## Artifact: `events.jsonl` closed projection

**What it proves:** FR-03/FR-04 — the journal-order event timeline uses only
the allowed per-kind fields; step ids are aliased and no raw error text rides
the record even though the fixture's `agent-1` StepStatus had `Err` set on an
earlier attempt.

**Command:** `unzip -p out.zip events.jsonl`

```json
{"seq":1,"time_ms":0,"kind":"run_started","data":{"steps":["step-0001","step-0002"]}}
{"seq":2,"time_ms":1000,"kind":"step_status","data":{"attempt":1,"from":"pending","to":"running","step":"step-0001",...}}
{"seq":5,"time_ms":7000,"kind":"step_status","data":{"attempt":1,"cost_usd":0.42,"step":"step-0002","to":"succeeded","tokens":1234}}
{"seq":6,"time_ms":8000,"kind":"run_finished","data":{"failed":false}}
```

`TestProjectEventClosedProjectionOmitsRawErrorAndAliasesSteps` asserts this
programmatically against the full fixture (including a failed first attempt
whose `Err` field contains a seeded secret) and finds neither the secret nor
the original step id `agent-1`/`fetch` in any projected event.

## Artifact: Disclosure scan and destination collision refusal

**What it proves:** FR-04 (closed disclosure) and FR-06 (no-overwrite
publication).

**Command:**

```bash
for secret in AKIAFAKEFAKEFAKEFAKE prod-incident-482 export-fixture; do
  unzip -p out.zip README.md manifest.json run.json events.jsonl | grep -q "$secret" && echo LEAK || echo "absent: $secret"
done
```

**Result summary:**

```
absent: AKIAFAKEFAKEFAKEFAKE
absent: prod-incident-482
absent: export-fixture
```

`TestClosedProjectionExcludesPrivateData` runs the equivalent scan across
every member and the raw archive bytes (headers/comments included) inside the
test suite. `TestArchivePublicationNoOverwrite` and
`TestExportDestinationCollisionRefusal` (cmd/jig) confirm a pre-existing
destination is left byte-for-byte unchanged and the command exits with a
usage-category refusal.

## Scope narrowing recorded for this task

- `internal/engine/resume.go` gained an exported pure `DecodeWorkflowSnapshot`
  seam (task 2.4) so export can validate a confined-read `workflow.json`
  without a path-based loader; the raw JSON's `base_dir` field is read
  separately from the decoded `*workflow.Workflow` because a workflow
  restored via `RestoreExpanded` (the common post-module-expansion shape)
  does not retain `wf.Source()`.
- Fan-out family/child aliasing and route/reset provenance are exercised
  structurally (`FanOutExpanded`, `RouteSelected`, `StepsReset` all appear in
  the fixture and are covered by the closed-projection test), but the exact
  gap-code taxonomy for every documented reason string in the spec's event
  table is intentionally reduced to the categories implemented in
  `internal/runexport/model.go` — sufficient for FR-03/FR-04 but not a
  byte-for-byte enumeration of every table cell.

## Reviewer Conclusion

A real `jig export` CLI run against a synthetic fixture produces a valid,
alias-only ZIP archive with no seeded private data in any member or ZIP
header, publishes without ever overwriting a competing destination, and is
covered by unit and CLI-level tests exercising the same behavior
automatically.
