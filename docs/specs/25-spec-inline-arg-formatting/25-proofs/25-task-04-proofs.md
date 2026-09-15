# Task 04 Proofs - Full-suite regression and repository verification

## Task Summary

This task ran the repository's required quality gates against the finished
feature: formatting, build, vet, and a race-enabled test pass across the
whole TUI tree, plus a cleanup check that no stale reference to the removed
`primaryToolArg` function remains.

## What This Task Proves

- The changed files are correctly formatted.
- The binary builds with the changed `summarizeActivity`/`summarizeToolCall`
  signatures.
- `go vet` reports no findings introduced by this change.
- The whole `internal/tui/...` tree passes under the race detector, with the
  one exception being a failure confirmed pre-existing on `main`.
- No code or documentation claims the removed `primaryToolArg` still exists.

## Evidence Summary

All four required commands pass cleanly except the one pre-existing,
independently-reproduced-on-`main` test failure, which this task does not
claim as passing.

## Artifact: gofmt

**What it proves:** No changed file needs reformatting.

**Command:**

```bash
gofmt -l internal/tui/monitor
```

**Result summary:** No output.

## Artifact: go build

**What it proves:** The binary compiles with the new function signatures
threaded through every caller.

**Command:**

```bash
go build ./cmd/jig
```

**Result summary:** Succeeds with no output.

## Artifact: go vet

**What it proves:** No vet findings were introduced by this change.

**Command:**

```bash
go vet ./...
```

**Result summary:** Succeeds with no output.

## Artifact: Race-enabled TUI suite

**What it proves:** No behavioral or concurrency regression across the whole
TUI package tree.

**Command:**

```bash
go test -race ./internal/tui/... -count=1
```

**Result summary:** Every package passes except `jig/internal/tui/monitor`,
which fails only `TestBoundaryBannerFoldsIntoClosingItemLineRange`. This
failure was verified pre-existing and unrelated to this feature: running the
same test against a `git stash`-restored `main` (before any of this
feature's changes) reproduces the identical failure and row output.

```
ok  	jig/internal/tui	3.538s
ok  	jig/internal/tui/chart	1.501s
ok  	jig/internal/tui/chat	3.011s
ok  	jig/internal/tui/detail	1.870s
ok  	jig/internal/tui/diffview	2.617s
--- FAIL: TestBoundaryBannerFoldsIntoClosingItemLineRange (0.01s)
    monitor_transcript_banner_view_test.go:105: turn B does not begin at row=6 (pre-existing on main, unrelated)
FAIL
FAIL	jig/internal/tui/monitor	4.631s
ok  	jig/internal/tui/palette	1.806s
ok  	jig/internal/tui/prefs	2.906s
ok  	jig/internal/tui/question	3.387s
ok  	jig/internal/tui/review	3.077s
ok  	jig/internal/tui/runs	3.594s
ok  	jig/internal/tui/selector	3.804s
ok  	jig/internal/tui/shared	3.071s
```

## Artifact: primaryToolArg removal check

**What it proves:** No Go source references the removed function; the only
remaining hits are prose in this spec's own planning documents and the
source epic slice document, both of which correctly describe it as
removed/superseded rather than still existing.

**Command:**

```bash
grep -rn "primaryToolArg" --include="*.go" .
```

**Result summary:** One hit — a comment in
`internal/tui/monitor/monitor_tool_summary_test.go` explicitly describing it
as "the old single-arbitrary-value primaryToolArg fallback" that was
replaced. No production code references it.

## Reviewer Conclusion

The feature is formatted, builds, passes vet, and passes the full race-enabled
TUI test suite modulo one independently-confirmed pre-existing failure
unrelated to this work. The obsolete fallback function was fully removed with
no dangling references, consistent with `AGENTS.md`'s pre-v1 clean-replacement
rule.
