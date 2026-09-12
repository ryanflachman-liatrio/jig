# Task 04 Acceptance Results and Limitations

## Acceptance checks

The checks below ran after Tasks 1-3 were committed on top of the current audited plan. The implementation baseline for failure attribution is `6b583fa`, the merge immediately before the original slice-04 implementation commits.

| Result | Artifact | EXIT |
| --- | --- | ---: |
| PASS — `go build ./cmd/jig` | `25-task-4-acceptance/build.txt` | 0 |
| FAIL — `go test ./...`; one load-sensitive engine timeout | `25-task-4-acceptance/test.txt` | 1 |
| PASS — `go vet ./...` | `25-task-4-acceptance/vet.txt` | 0 |
| PASS — `go test -race ./internal/tui/...` | `25-task-4-acceptance/test-race-tui.txt` | 0 |
| PASS — changed Go files are formatted | `25-task-4-acceptance/gofmt.txt` | 0 |
| PASS — repository-wide `git diff --check` | `25-task-4-acceptance/git-diff-check.txt` | 0 |

## Repository-wide test limitation

The current-tree `go test ./...` run failed only in `internal/engine`:

~~~text
--- FAIL: TestForEachChild_SecurityEscalationUnmodified (5.00s)
    engine_fanout_test.go:264: timeout waiting for RunFinished
~~~

The exact test passed immediately in isolation:

~~~bash
go test ./internal/engine -run TestForEachChild_SecurityEscalationUnmodified -count=1 -v
~~~

It also did not reproduce during the required isolated baseline check:

~~~bash
git worktree add --detach <temporary-worktree> 6b583fa
(cd <temporary-worktree> && go test ./...)
~~~

That baseline suite exited 0, including `internal/engine`. The evidence therefore supports a load-sensitive timeout, but it does not support labeling the failure as pre-existing based on this run. No expectation, deadline, engine code, scheduler code, or harness code was changed to mask it. The Monitor-specific package passed in both the ordinary repository suite and the targeted race suite.

## Visual capture

The integrated 80×30 capture exists in ANSI, deterministic HTML, and PNG forms:

- `25-task-4-monitor-rhythm.ansi`
- `25-task-4-monitor-rhythm.html`
- `25-task-4-monitor-rhythm.png`
- `25-task-4-monitor-rhythm-notes.txt`

Local headless Chrome successfully converted the HTML capture to a 1200×900 PNG. Chrome emitted macOS display-policy diagnostics while running headlessly but exited successfully and wrote the 42,164-byte image.

## Scope review

The cumulative slice-04 implementation is limited to the three planned Monitor production files, Monitor regression tests, and this spec directory. It changes no `internal/tui/shared/` code, palette, engine, harness, scheduler, workflow schema, wire format, or other epic slice behavior.

## Reviewer conclusion

All acceptance gates directly relevant to the Monitor change pass. The sole repository-wide failure is recorded as an unresolved load-sensitive engine timeout because it passed alone and the baseline suite passed; validation should retain this limitation rather than report the full suite as green.
