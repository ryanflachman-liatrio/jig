# Task 05 Proofs — Regression sweep (build, vet, test, validate)

## Task Summary

This task proves Success Metric 4: no regressions across the engine/runner/step
packages and the persistence-off paths, with the repo's standing quality gates
all green.

## What This Task Proves

- `gofmt` and `go vet` are clean.
- `go build ./cmd/jig` succeeds.
- `go test ./... -race -count=1` passes for every package (including the
  persistence-off engine tests and the TUI under `-race`).
- The kitchen-sink example `feature.toml` still validates — confirming no schema
  surface was added (Non-Goal 4).

## Evidence Summary

All gates pass. Note: `examples/research.toml` and `examples/review.toml` fail
validation both on this branch and on `main` (pre-existing: an invalid `when`
enum and a reserved schema field) — unrelated to this change. `feature.toml`, the
kitchen-sink reference named in CLAUDE.md, validates cleanly.

## Artifact: combined gate run

**Command:** `gofmt -l internal/ cmd/ && go vet ./... && go build ./cmd/jig && go test ./... -race -count=1 && go run ./cmd/jig validate examples/feature.toml`

**Artifact path:** `B.regression.txt`

**Result summary:** fmt clean, vet exit 0, build OK, all packages `ok`, and
`feature.toml` reports `ok: "feature" v1 — 15 step(s)`.

```
=== go test ./... -race -count=1 ===
ok  	jig/internal/datastore
ok  	jig/internal/engine
ok  	jig/internal/runner
ok  	jig/internal/step
ok  	jig/internal/transcript
ok  	jig/internal/tui
ok  	jig/internal/workflow

=== go run ./cmd/jig validate examples/feature.toml ===
ok: "feature" v1 — 15 step(s)
```

## Reviewer Conclusion

No regressions; every quality gate is green and the schema surface is unchanged.
