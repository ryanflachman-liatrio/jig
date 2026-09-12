# Task 01 Proofs - Raw-byte structural edge trimming

## Task Summary

This task establishes the raw-byte blank predicate and edge trimmer used by the Monitor transcript. It preserves ANSI-bearing padding rows while removing only plain ASCII-whitespace edge rows.

## What This Task Proves

- Empty and ASCII-whitespace-only rows are structural blanks.
- ANSI escape bytes, printable glyphs, and Unicode NBSP make a row content.
- Structural edge trimming preserves interior blanks and ANSI-bearing padding.
- The SGR-aware and raw-byte trimmers remain confined to their intended call sites.

## Evidence Summary

The Jig CLI builds, all predicate and trimming cases pass, and the call-site regression test passes. The implementation originally landed in commit `001dded`; this proof refresh verifies it against the current audited task contract.

## Artifact: Focused build and helper tests

**What it proves:** The helpers compile in the production binary and satisfy the complete raw-byte behavior table.

**Why it matters:** The Monitor must distinguish plain structural whitespace from intentionally styled padding without creating a second rendering policy.

**Commands:**

~~~bash
gofmt -w internal/tui/monitor/monitor_transcript.go internal/tui/monitor/monitor_transcript_test.go
go build ./cmd/jig
go test ./internal/tui/monitor -run 'TestIsStructuralBlank|TestTrimStructuralBlank|TestVerticalRhythmHelperCallSites' -v
~~~

**Result summary:** `go build` exited 0. All 12 structural-blank subcases, all 12 trim subcases, and the helper call-site test passed; package result was `ok jig/internal/tui/monitor`.

~~~text
--- PASS: TestIsStructuralBlankRawBytesSemantics
--- PASS: TestTrimStructuralBlankEdges
--- PASS: TestVerticalRhythmHelperCallSites
PASS
ok  jig/internal/tui/monitor
~~~

## Security Check

The test data is synthetic and contains no credentials, local run transcripts, or `.env` content.

## Reviewer Conclusion

The raw-byte structural blank contract is implemented, scoped to the Monitor, formatted, buildable, and covered by focused regression tests.
