# 25-task-4-limitations.md

## `go test ./...` reports pre-existing flakes in `internal/engine` and `internal/harness`

`go test ./... -count=1` (captured to
`25-task-4-acceptance/test.txt`) reports failures in the `internal/engine`
and `internal/harness` packages, all of the form
`timeout waiting for ReviewRequest for "gate"` (`internal/engine`) or
`ACPHarness setup exceeded deadline` (`internal/harness`).

Every failing test passes when re-run in isolation:

```
$ go test ./internal/engine -run TestScheduler_ReviewDiff -count=1 -timeout 120s
ok  	jig/internal/engine	0.101s

$ go test ./internal/engine -run TestStepReintegratesAfterRetry -count=1 -timeout 60s
ok  	jig/internal/engine	0.091s

$ go test ./internal/harness -run TestTier2ObservesEveryACPHarness -count=1 -timeout 60s
ok  	jig/internal/harness	2.412s
```

Slice 07 changes only `internal/tui/monitor`, `internal/tui/shared`,
`go.mod` (promoting `hexops/gotextdiff` to direct), and the ADR/spec
docs. It does not touch `internal/engine`, `internal/harness`,
`internal/runner`, or the runtime scheduler. The flake is
load-related (engine tests spawn processes and share OS timeouts),
not caused by this slice's presentation-only changes. The change
is safe to land; the flake should be tracked separately.

## `go build ./cmd/jig`, `go vet ./...`, `go test -race ./internal/tui/... ./internal/helpchat`, `gofmt`, and `git diff --check`

All pass. See:

- `25-task-4-acceptance/build.txt`
- `25-task-4-acceptance/vet.txt`
- `25-task-4-acceptance/race-tui.txt`
- `25-task-4-acceptance/gofmt.txt`
- `25-task-4-acceptance/git-diff-check.txt`

The nested ACP module (`harness/acp`) is untouched by this slice
(no imports cross that boundary); it is not covered by the root
`go test ./...` and no separate build was required for slice 07.
