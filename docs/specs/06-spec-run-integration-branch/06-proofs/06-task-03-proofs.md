# Task 03 Proofs — Integration-conflict gate

## Task Summary

Task 3.0 makes squash-merge conflicts human-owned. When a step's integration
into the run branch conflicts with already-integrated work, the step parks in the
new `StatusAwaitingIntegration` state and an `IntegrationConflictRequest` naming
the conflicted paths is surfaced through the Gate — the run stays alive
(non-blocking, ADR 0002). The operator resolves in the run worktree and signals
completion via `Run.ResolveIntegration`, which finishes the squash-merge; an abort
fails the step and routes it to the existing recovery gate. Resolution is a
single-writer scheduler message + handler, mirroring `Run.Recover`/`handleRecover`.

## What This Task Proves

- An overlapping integration raises the gate with the conflicted paths, and
  resolving it lands a merged commit and completes the run.
- The abort path transitions the conflicted step to failed and routes it to the
  recovery gate.
- The `IntegrationConflictRequest` event round-trips through the journal.
- The TUI renders the integration gate with the conflicted paths and its
  resolve/abort actions emit `resolveIntegrationResponseMsg`.

## Evidence Summary

- `TestIntegrationConflictRaisesGate`, `TestIntegrationConflictAbortFailsStep`,
  `TestJournalRoundTrip` (now covering `IntegrationConflictRequest`),
  `TestMonitorIntegrationConflictGate` all PASS under `-race`.
- `go test ./... -race` green; `gofmt`/`vet` clean.
- The A2.0 log shows the conflict, the surfaced path, and both `jig-step`
  trailers after resolution.

## Artifact: Conflict raised, resolved, merged commit landed

**What it proves:** The second overlapping integration conflicts on `shared.txt`
(the paths the gate reports), and after the operator resolves in the run worktree
the merge is finished as a `jig-step: b`-tagged commit — both steps end integrated.

**Why it matters:** This is the observable evidence that conflicts are surfaced
(never auto-resolved) and that resolution lands a real merged commit.

**Artifact path:** `06-proofs/A2.0-integration-conflict-gate.txt`

**Result summary:** `git diff --diff-filter=U` reports `shared.txt` at conflict
time; after resolution the run branch carries `jig-step=b` over `jig-step=a`, and
`shared.txt` contains both contributions.

```
## b conflicts on shared.txt (gate fires: IntegrationConflictRequest{StepID:b, Paths:[shared.txt]})
CONFLICT (add/add): Merge conflict in shared.txt
$ git diff --name-only --diff-filter=U
shared.txt

## after Run.ResolveIntegration finishes the merge
01c204d...  jig-step=b
4f7735f...  jig-step=a
62af998...  jig-step=

## resolved shared.txt
from a
from b
```

## Artifact: Engine + journal + TUI tests under -race

**What it proves:** Gate-raise, resolve, abort→recovery, journal round-trip, and
the TUI surface all behave.

**Command:**

```bash
go test ./internal/engine -race -run 'TestIntegrationConflict|TestJournal' -v
go test ./internal/tui -run TestMonitorIntegrationConflictGate -v
go test ./... -race
```

**Result summary:** All targeted tests PASS; the whole suite is green under `-race`.

```
--- PASS: TestIntegrationConflictRaisesGate (0.38s)
--- PASS: TestIntegrationConflictAbortFailsStep (0.32s)
--- PASS: TestJournalRoundTrip (0.00s)
--- PASS: TestMonitorIntegrationConflictGate (0.00s)
```

## Reviewer Conclusion

Integration conflicts are surfaced to a human with the exact conflicted paths,
resolve into a real merged commit that lets the run complete, and abort cleanly
into the recovery gate — all through the single-writer scheduler and the existing
Gate/entry TUI pattern.
