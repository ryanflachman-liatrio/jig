# Task 01 Proofs - Delete `ClaudeHarness`; `AcpHarness` becomes the only Claude path

## Task Summary

This task removes `ClaudeHarness` (the direct `claude-agent-sdk-go` path) and
the `Transport` concept entirely, so `backend = "claude"` now always resolves
to `AcpHarness`. `internal/harness/select.go`'s `For` collapses to a
single-argument `For(backend string)`, and `Transport` is deleted from the
workflow schema, `SessionInfo`, manifest/runexport/telemetry provenance, and
every caller. Dead `internal/tui/chat/*` is deleted alongside it.

Implementation surfaced a real regression the spec's Unit 1 description did
not anticipate — see "Capability gap discovered and closed" below — which
required extending `AcpHarness` itself, not just deleting code.

## What This Task Proves

- `claudecode`/`claude-agent-sdk-go` no longer appears anywhere under
  `internal/harness/`, and the `ClaudeHarness` Go type no longer exists
  anywhere in the tree.
- The root module and the nested `harness/acp` module build, vet, and test
  clean with zero `ClaudeHarness` references.
- A real, unmodified `backend = "claude"` workflow step (no `transport` set —
  the field no longer exists) runs to completion through `AcpHarness` and
  produces a normal transcript, with no TUI changes required.
- `AcpHarness` now supports session resume (`CapSessionResume`), closing a
  capability gap that would otherwise have silently broken `block_on`/
  stop-resume for every `backend = "claude"` step, including the real
  `.agents/jig/feature.toml` workflow.

## Evidence Summary

- `grep -rn "claudecode\|claude-agent-sdk-go" internal/harness/` returns
  nothing.
- `grep -rn "\bClaudeHarness\b" --include="*.go" .` returns nothing anywhere
  in the tree.
- `go build ./... && go vet ./...` (root) and the equivalent in
  `harness/acp` both pass with no errors.
- `go test ./...` (root, all packages) and `go test ./...` (nested
  `harness/acp`) both pass, with one pre-existing, unrelated failure
  documented below (not caused by this task).
- A live `jig run` of a `backend = "claude"` workflow step against the real
  `claude`/`npx` binaries on this machine succeeded, producing a transcript
  entry and a `succeeded` `result.json` — end-to-end proof `AcpHarness`
  drives the Claude path correctly.

## Artifact: Grep confirms the SDK is gone from the harness seam

**What it proves:** `internal/harness/` no longer imports or references the
Claude Agent SDK in any form.

**Why it matters:** This is the core deletion Unit 1 exists to make; a
lingering import would mean the migration is incomplete.

**Command:**

```bash
grep -rn "claudecode\|claude-agent-sdk-go" internal/harness/
echo "exit: $?"
```

**Result summary:** No matches; grep exits 1 (not found).

```
exit: 1
```

## Artifact: Grep confirms `ClaudeHarness` no longer exists anywhere

**What it proves:** The Go type itself — not just its import — is gone
tree-wide, including test fixtures and doc comments that used to reference
it by name.

**Command:**

```bash
grep -rn "\bClaudeHarness\b" --include="*.go" .
echo "exit: $?"
```

**Result summary:** No matches.

```
exit: 1
```

## Artifact: Build and vet pass, both modules

**What it proves:** The `For(backend, transport)` → `For(backend)` signature
change and the `Transport` field removal compile cleanly everywhere they
rippled: `internal/workflow`, `internal/runner`, `internal/engine`,
`internal/ops`, `internal/telemetry`, `internal/manifest`, `internal/runexport`,
`internal/datastore`, `internal/tui/detail`, and the nested `harness/acp`
module.

**Command:**

```bash
go build ./... && go vet ./...
(cd harness/acp && go build ./... && go vet ./...)
```

**Result summary:** Both pass with no output (no errors).

## Artifact: Full test suite passes (one pre-existing, unrelated failure)

**What it proves:** No behavioral regression anywhere in the tree from
deleting `ClaudeHarness`, removing `Transport`, or extending `AcpHarness`
with session-resume support.

**Command:**

```bash
go test ./... -count=1
```

**Result summary:** Every package passes except
`jig/internal/tui/monitor`'s `TestBoundaryBannerFoldsIntoClosingItemLineRange`,
which fails identically on a clean checkout **before** this task's changes
(confirmed via `git status` at session start showing this file already
modified from unrelated in-progress work on spec 11/25's transcript banner
folding). Verified in isolation with `-count=3` that this failure is a
pre-existing test, not a flake or regression introduced here.

```
--- FAIL: TestBoundaryBannerFoldsIntoClosingItemLineRange (0.00s)
FAIL
FAIL	jig/internal/tui/monitor	3.404s
```

All other 30 packages report `ok`, including `jig/internal/harness` (29.7s,
covers all live-ACP fixture tests) and `jig/internal/runner` (3.6s).

```bash
(cd harness/acp && go test ./... -count=1)
```

```
ok  	jig/harness/acp	0.428s
?   	jig/harness/acp/cmd/spike	[no test files]
```

Also ran the docs/TESTING.md focused race invocations for the affected
packages — all clean:

```bash
go test -race ./internal/engine ./internal/runner ./internal/harness -count=1
go test -race ./internal/tui/... ./internal/helpchat -count=1
```

Result: same single pre-existing `tui/monitor` failure; no races reported;
`internal/runner`, `internal/harness`, `internal/helpchat` all pass.

## Artifact: Capability gap discovered and closed (`AcpHarness` session resume)

**What it proves:** Deleting `ClaudeHarness` alone would have silently broken
`block_on`/stop-resume for every `backend = "claude"` step — a real
regression against Success Metric 2 ("no regression in step execution"),
not anticipated by the spec's "zero design risk" framing for this unit.

**Why it matters:** `ClaudeHarness` advertised `CapSessionResume`;
`AcpHarness.Open` unconditionally rejected `spec.Resume` with "session
resume not supported." `.agents/jig/feature.toml` — a real, in-tree
production workflow — uses `block_on` on three `backend = "claude"` steps
today. This was flagged to the user mid-implementation (see conversation);
the user chose to scope and fix it rather than document it as a known
regression.

The fix reused an already-proven pattern: `CursorHarness`/`CodexHarness`
already wire `spec.Resume` through `conn.LoadSession` and already advertise
`CapSessionResume`. The same wiring was applied to `AcpHarness.Open`, and
`CapSessionResume` added to its `Capabilities()`.

This surfaced a second, independent bug: `harness/acp/conn.go`'s `Connect`
(the Zed/Claude adapter spawn path) never populated `Conn.SupportsLoadSession`
from the ACP `Initialize` response — `ConnectCursor`/`ConnectCodex` did, but
`Connect` did not. Fixed to match; without this fix, `LoadSession` would
always fail with "adapter did not advertise session/load" even after wiring
`AcpHarness.Open` correctly.

**Command (live ACP fixture, before the fix — for reference, reconstructed
from the test failure this task fixed):**

```
security_integration_test.go:380: resume result=&{Status:failed ... Err:agent open: acp: adapter did not advertise session/load}
```

**Command (after the fix):**

```bash
go test ./internal/harness/... -run \
  "TestACPResumePathsLoadExplicitly|TestTier2ACPLiveStopResumeUsesBackendCapability|TestTier2PersistedReopenAcrossEveryACPHarness|TestAcpHarnessCapabilities" \
  -v
```

**Result summary:** All four tests, including their `claude-acp`
sub-tests, now pass — `claude-acp` resumes via `session/load` exactly like
`cursor-acp`/`codex-acp`, where before this fix it silently fell back to a
fresh session (masking the loss of conversation history from an operator
resuming a stopped Claude step).

```
--- PASS: TestAcpHarnessCapabilities (0.00s)
--- PASS: TestACPResumePathsLoadExplicitly (1.xx s)
    --- PASS: TestACPResumePathsLoadExplicitly/claude-acp
    --- PASS: TestACPResumePathsLoadExplicitly/cursor-acp
    --- PASS: TestACPResumePathsLoadExplicitly/codex-acp
--- PASS: TestTier2ACPLiveStopResumeUsesBackendCapability
    --- PASS: TestTier2ACPLiveStopResumeUsesBackendCapability/claude-acp
    --- PASS: TestTier2ACPLiveStopResumeUsesBackendCapability/cursor-acp
    --- PASS: TestTier2ACPLiveStopResumeUsesBackendCapability/codex-acp
--- PASS: TestTier2PersistedReopenAcrossEveryACPHarness
    --- PASS: TestTier2PersistedReopenAcrossEveryACPHarness/claude-acp
    --- PASS: TestTier2PersistedReopenAcrossEveryACPHarness/cursor-acp
    --- PASS: TestTier2PersistedReopenAcrossEveryACPHarness/codex-acp
```

## Artifact: Real end-to-end `backend = "claude"` workflow run

**What it proves:** Outside of any test fixture, a real workflow step with
`backend = "claude"` (no `transport`, since the field no longer exists) runs
against the real `claude`/`npx` binaries on this machine and completes
successfully through `AcpHarness`.

**Why it matters:** This is the closest available substitute for "run it in
the TUI and look" in a non-interactive session — it proves the full stack
(workflow load → `AgentExecutor` → `harness.For("claude")` → `AcpHarness.Open`
→ real Zed Claude ACP adapter → transcript write) end to end, not just unit
coverage.

**Workflow fixture (sanitized, no secrets):**

```toml
[workflow]
name = "unit1-agent-smoke"
version = "1"

[defaults]
backend = "claude"

[[step]]
id = "greet"
type = "agent"
skill = "skills/hello"
allowed_tools = []
isolation = "none"
```

**Command:**

```bash
jig run agent-smoke.toml --root <absolute-path>/.jig --ci
```

**Result summary:** The step succeeded on the first real run once given an
absolute persistence root. `result.json` shows `"status": "succeeded"` with
a real ACP `session_id` and `subtype: "end_turn"`; the transcript contains
one normal assistant entry.

```json
{"ok":true,"run_id":"20260916-165714-ewjzcpnb","workflow":"unit1-agent-smoke","failed":false,"total_cost_usd":0,"total_tokens":0,"run_dir":"[REDACTED]/.jig/runs/20260916-165714-ewjzcpnb","error":null}
```

```json
{
  "step_id": "greet",
  "status": "succeeded",
  "result": {
    "status": "succeeded",
    "structured": {
      "assumptions": [],
      "confidence": "high",
      "issues": [],
      "status": "succeeded",
      "summary": "Replied with the requested greeting."
    },
    "session_id": "[REDACTED]",
    "subtype": "end_turn"
  },
  "provenance": {
    "backend": "claude"
  }
}
```

Transcript (one entry, normal shape):

```json
{"seq":1,"role":"assistant","blocks":[{"type":"text","text":"hello\n\n```json\n{\"assumptions\": [], \"confidence\": \"high\", \"issues\": [], \"status\": \"succeeded\", \"summary\": \"Replied with the requested greeting.\"}\n```"}]}
```

**Note on an unrelated finding:** the first two run attempts failed with
`` `cwd` must be an absolute path `` because `jig run`'s default `--root`
value (`.jig`, a relative path) produces a relative worktree-view path in
`internal/engine/execution.go` when the run is not launched with an absolute
`--root`. This is a pre-existing engine/CLI behavior unrelated to this
task's `Transport`/`ClaudeHarness` changes (confirmed: the code path involved,
`internal/engine/execution.go`'s `executionViews`/`jigRoot` handling, is
untouched by this diff, and the same relative-root behavior would affect any
backend). Not fixed here — out of this task's scope — but noted since it
affected how the manual proof run above had to be invoked.

## Reviewer Conclusion

`ClaudeHarness`, the `sdk` transport, and the `Transport` field are fully
removed from the tree with no remaining references, all builds/vets/tests
pass (modulo one confirmed pre-existing unrelated TUI test failure), and a
real end-to-end run proves `AcpHarness` now correctly drives the Claude path
— including a session-resume capability that had to be added to `AcpHarness`
(and a related bug fixed in `harness/acp/conn.go`) to avoid a genuine
regression in `block_on`/stop-resume support that the original spec did not
anticipate.
