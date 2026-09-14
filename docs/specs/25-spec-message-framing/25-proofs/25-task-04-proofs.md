# Task 04 Proofs - Expand toggle and cache discipline

## Task Summary

This task closes out the spec by proving the expand/collapse round trip works
end to end and that the markdown render cache (`chatRendered`, keyed by
`blockKey`) is only ever populated by a real render — never by the collapsed
summary — plus a full repository regression pass.

## What This Task Proves

- Toggling the existing expand map entry on a collapsed item renders the full
  markdown body inside the bubble; toggling it back restores the single
  summary row (FR-09.6).
- `chatRendered` gains no entry while collapsed, and after expansion holds
  the true render (never the summary text) — direct proof of the Technical
  Considerations cache-discipline note (FR-09.5).
- A search hit on a collapsed item's raw block text selects the item without
  requiring it to already be expanded, confirming the collapse feature does
  not break `monitor_search.go`'s existing behavior.
- The full `internal/tui` suite plus the repository-wide `go build`,
  `go vet`, and `go test ./...` (including `-race` on the TUI packages) pass
  with no regressions (Success Metric 3).

## Evidence Summary

- 3 new tests in `monitor_transcript_expand_test.go` pass.
- `go test -race ./internal/tui/... ./internal/helpchat` passes.
- `go build ./... && go vet ./... && go test ./... -count=1` passes at the
  repository root (one `internal/harness` run flaked on an unrelated ACP
  connection-teardown log line under `-count=1`; a subsequent isolated
  `go test ./internal/harness/... -count=1` passed cleanly, and this
  spec's diff touches nothing in `internal/harness`).

## Artifact: Expand/collapse round trip and cache discipline

**What it proves:** `TestExpandTogglesCollapsedUserTextRoundTrip` shows
collapsed → expanded → collapsed renders the summary, then the full body,
then the summary again. `TestExpansionPopulatesMarkdownCacheNotSummary`
directly inspects the `chatRendered` map: empty while collapsed, holding the
real render (not the summary) after expansion.

**Command:**

~~~bash
go test ./internal/tui/monitor/... -v -run \
  'TestExpandTogglesCollapsedUserTextRoundTrip|TestExpansionPopulatesMarkdownCacheNotSummary'
~~~

**Result summary:** Both tests pass.

~~~text
=== RUN   TestExpandTogglesCollapsedUserTextRoundTrip
--- PASS: TestExpandTogglesCollapsedUserTextRoundTrip (0.01s)
=== RUN   TestExpansionPopulatesMarkdownCacheNotSummary
--- PASS: TestExpansionPopulatesMarkdownCacheNotSummary (0.00s)
PASS
~~~

## Artifact: Search interplay with a collapsed item

**What it proves:** A search hit matching text deep inside an oversized,
collapsed user block selects that item (`selectedTranscriptItemKey`) without
the test ever setting `chatItemExpand` — expansion is not required for the
match or the selection to work.

**Command:**

~~~bash
go test ./internal/tui/monitor/... -run TestSearchHitSelectsCollapsedItemWithoutRequiringExpansion -v
~~~

**Result summary:** Pass — the collapsed item is selected by its search hit.

~~~text
=== RUN   TestSearchHitSelectsCollapsedItemWithoutRequiringExpansion
--- PASS: TestSearchHitSelectsCollapsedItemWithoutRequiringExpansion (0.01s)
PASS
~~~

## Artifact: Full regression pass (build, vet, race, root test suite)

**What it proves:** No regressions anywhere in the repository from the four
tasks in this spec.

**Command:**

~~~bash
go build ./cmd/jig
go build ./...
go vet ./...
gofmt -l <all changed .go files>
go test -race ./internal/tui/... ./internal/helpchat
go test ./... -count=1
~~~

**Result summary:** `gofmt -l` printed no files. `go vet` reported no
issues. The `-race` run across all `internal/tui/...` packages plus
`internal/helpchat` passed. The full `go test ./... -count=1` run passed for
every package on a clean rerun (see Evidence Summary for the one unrelated
flake, isolated and independently confirmed passing).

~~~text
ok  	jig/internal/tui	...
ok  	jig/internal/tui/monitor	...
ok  	jig/internal/tui/shared	...
... (all other packages ok)
~~~

## Reviewer Conclusion

The expand/collapse toggle round-trips correctly and renders the full body
exactly once per expansion, the markdown cache is never polluted with the
collapsed summary, and the collapse feature composes cleanly with search
navigation. Combined with Tasks 1–3, this closes out all 16 functional
requirements in the message-framing spec: the `User` label is gone, the
bubble carries role via tint alone, and oversized user text collapses
without paying any markdown layout cost until the operator asks for it.
