# Task 01 Proofs - Safe acquisition and scheduler lease

## Task Summary

This task creates the fail-closed boundary before export reads or publishes run evidence. Engine scheduling and export share one non-blocking `scheduler.lock` lease; request validation and rooted inventory reject unsafe targets without exposing source text.

## What This Task Proves

- Start, Resume, and export use the same descriptor-backed ownership lease.
- Unsafe run IDs, destinations, and selected symlinks are refused before evidence is changed.
- Another process holding the scheduler lease is treated as live, including while it has no worker.
- Inventory detects selected-evidence changes from identity and metadata without reporting source values.

## Evidence Summary

Focused engine/export acquisition tests passed. The complete package tests and static checks also passed. Synthetic fixture tests compare pre/post hashes of journal, workflow snapshot, and transcript payloads; refused requests leave those hashes equal. A valid acquisition may create only an empty `scheduler.lock` coordination file.

## Artifact: Focused acquisition verification

**What it proves:** Request resolution, confined inventory, lock ownership, cross-process contention, and release behavior are executable and passing.

**Why it matters:** These are mandatory safety preconditions for archive projection and publication.

**Command:** `go test ./internal/engine ./internal/runexport -run 'Test(ResolveRequest|AcquireLease|ConfinedInventory|RunLock|RunLease|ExportRejects)' -v -count=1`

**Result summary:** Passed. The suite covered unsafe destinations, missing/file/symlink run targets, selected-file symlink refusal, live ownership, mutation detection, and a separate-process lease holder.

~~~text
ok   jig/internal/engine
ok   jig/internal/runexport
~~~

## Artifact: Full package and static verification

**What it proves:** The lease extraction does not regress engine behavior and the export boundary passes static checks.

**Why it matters:** The lease is a scheduler lifecycle primitive, so focused tests alone are insufficient regression evidence.

**Commands:** `go test ./internal/engine ./internal/runexport -count=1`; `go vet ./internal/engine ./internal/runexport`; `gofmt -l internal/engine internal/runexport`.

**Result summary:** Both package suites passed; vet and the formatting check produced no findings.

~~~text
ok   jig/internal/engine
ok   jig/internal/runexport
~~~

## Reviewer Conclusion

The exporter acquires the scheduler ownership protocol before selected-evidence inspection, rejects unsafe or live targets, and releases acquisition resources on failure. Archive construction begins in Task 2.
