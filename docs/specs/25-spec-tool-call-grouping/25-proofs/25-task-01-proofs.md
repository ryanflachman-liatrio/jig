# Task 01 Proofs - Stable page-local read grouping

## Task Summary

This task adds an immutable grouped-read transcript item after ordinary tool
use/result correlation. Only uninterrupted runs of at least two canonical local
file reads at one generation, iteration, and attempt become a group.

## What This Task Proves

- FR-08.1–FR-08.4: adjacency, eligibility, interruption, and execution-coordinate
  boundaries are enforced by a pure linear pass.
- FR-08.5–FR-08.7: the first read anchors stable group identity and every original
  use/result reference remains available in member order.
- FR-08.8: singleton and page-edge evidence stays honest, while surviving group
  state is retained and stale state is pruned.

## Evidence Summary

Synthetic transcript fixtures cover adjacent and singleton reads, missing and
URI targets, canonical-kind precedence, text/thinking/system/unsupported/tool
interruptions, every execution coordinate, result-first ACP ordering, reload,
and state pruning. No persisted user run data is used.

## Artifact: Focused normalization suite

**What it proves:** The grouping pass and page-state integration satisfy the
normalization and stable-identity contract.

**Why it matters:** Rendering and interaction can safely consume one normalized
item without becoming a second correlation or scheduling layer.

**Command:**

```bash
go test ./internal/tui/monitor -run 'Test(GroupReadTranscriptItems|ReadGroupPageState|ReadGroupResultFirst)' -count=1
```

**Result summary:** The focused suite passed.

```text
ok  	jig/internal/tui/monitor	0.439s
```

## Reviewer Conclusion

The normalized model groups only eligible adjacent reads, retains all bounded
page evidence, and keeps stable item identity and lifecycle state.
