# Phase 3 Proof — Review workspace core

## What this proves

The standalone `internal/tui/review` child model now supports source-line
navigation, document restoration, inclusive range anchors, draft projection,
comment lifecycle, reviewed-document acknowledgements, suggestion validation,
and structured submission construction. It has no dependency on the engine or
Monitor, so Phase 4 can own routing and persistence commands.

## Evidence

Command:

```bash
go test ./internal/tui/review -count=1
```

Result: PASS. The model tests cover navigation/restoration, range quoting,
suggestion replacement requirements, monotonic comment IDs, draft state, and
summary submission.

Additional TUI regression command:

```bash
go test ./internal/tui/... -count=1
```

Result: PASS.
