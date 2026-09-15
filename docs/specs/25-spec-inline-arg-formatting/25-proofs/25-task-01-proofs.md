# Task 01 Proofs - Fair-share inline argument formatter (core algorithm)

## Task Summary

This task built `formatArgsInline`, the pure, width-budgeted formatter at the
heart of epic slice 15: given a tool's arguments and an available width, it
renders as many `key=value` pairs as fit, reserving a minimal footprint for
every key still pending so one long value can never crowd out the keys after
it. It also handles scalar formatting (quoting, escaping, count summaries for
containers), secret-key redaction, and deterministic ordering despite Go's
randomized map iteration.

## What This Task Proves

- Width is a real budget the formatter respects, not a post-hoc clip: no
  output ever exceeds the supplied `maxWidth`, including at pathologically
  narrow widths (0-3 cells).
- A long argument value cannot starve the keys that follow it.
- Output is deterministic across repeated calls on the same unordered
  `map[string]json.RawMessage`.
- Secret-shaped keys never leak their value into the preview.
- Strings are escaped and containers are summarized by count, keeping the
  preview to a single line regardless of input shape.

## Evidence Summary

`go test ./internal/tui/monitor -run TestFormatArgsInline -v` passes all 8
subtests, each mapped to one of the spec's required behaviors (FR-15.1
through FR-15.6, FR-15.8, plus the security redaction requirement and the
audit's extreme-narrow-width flag).

## Artifact: TestFormatArgsInline full run

**What it proves:** Every planned behavior from the task-planning proof
artifact — long-value fairness, budget exhaustion, container counts,
escaping, empty/noise handling, secret redaction, width-never-exceeded (incl.
extreme narrow widths), and determinism — passes as an isolated unit test
against the formatter directly, with no TUI rendering involved.

**Why it matters:** This is the algorithm's correctness proof independent of
any caller; tasks 2.0 and 3.0 build on top of it and their own tests assume
this contract holds.

**Command:**

```bash
go test ./internal/tui/monitor -run TestFormatArgsInline -v
```

**Result summary:** All 8 subtests pass in 0.00s.

```
=== RUN   TestFormatArgsInline
=== RUN   TestFormatArgsInline/long_value_does_not_starve_trailing_keys
=== RUN   TestFormatArgsInline/budget_exhaustion_appends_an_ellipsis
=== RUN   TestFormatArgsInline/array_and_object_arguments_render_as_counts
=== RUN   TestFormatArgsInline/newline_and_tab_are_escaped_and_output_stays_one_line
=== RUN   TestFormatArgsInline/empty_and_noise-only_arguments_produce_empty_output
=== RUN   TestFormatArgsInline/secret-shaped_key_is_redacted
=== RUN   TestFormatArgsInline/width_budget_is_never_exceeded,_including_extreme_narrow_widths
=== RUN   TestFormatArgsInline/deterministic_across_repeated_calls_on_the_same_unordered_map
--- PASS: TestFormatArgsInline (0.00s)
    --- PASS: TestFormatArgsInline/long_value_does_not_starve_trailing_keys (0.00s)
    --- PASS: TestFormatArgsInline/budget_exhaustion_appends_an_ellipsis (0.00s)
    --- PASS: TestFormatArgsInline/array_and_object_arguments_render_as_counts (0.00s)
    --- PASS: TestFormatArgsInline/newline_and_tab_are_escaped_and_output_stays_one_line (0.00s)
    --- PASS: TestFormatArgsInline/empty_and_noise-only_arguments_produce_empty_output (0.00s)
    --- PASS: TestFormatArgsInline/secret-shaped_key_is_redacted (0.00s)
    --- PASS: TestFormatArgsInline/width_budget_is_never_exceeded,_including_extreme_narrow_widths (0.00s)
    --- PASS: TestFormatArgsInline/deterministic_across_repeated_calls_on_the_same_unordered_map (0.00s)
PASS
ok  	jig/internal/tui/monitor	0.467s
```

## Artifact: gofmt compliance

**What it proves:** The new code in `monitor_tool_summary.go` and
`monitor_tool_summary_test.go` follows repository formatting conventions.

**Why it matters:** `AGENTS.md` requires `gofmt -w` on changed Go files; this
confirms nothing is left unformatted.

**Command:**

```bash
gofmt -l internal/tui/monitor
```

**Result summary:** No output — no file in the package needs reformatting.

## Reviewer Conclusion

`formatArgsInline` and its scalar/ordering/redaction helpers are correct in
isolation: they respect the width budget under all tested conditions
(including extreme narrow widths flagged by the planning audit), never starve
trailing keys, redact secret-shaped keys, and produce deterministic output
despite Go's unordered maps.
