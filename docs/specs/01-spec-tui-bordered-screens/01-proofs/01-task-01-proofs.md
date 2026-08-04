# Task 01 Proofs — Reusable titled-panel helper and single-box screens

## Task Summary

This task proves jig now has one shared, pure-presentation titled-panel primitive
(`panel()` in `internal/tui/panel.go`) and that the three single-view screens —
**selector**, **detail**, and **runs** — render inside it as a rounded box with the
title composited into the top border edge (lazygit style). The selector's bubbles-list
chrome is stripped, the detail title falls back from workflow name to file path, and
runs gained a scrolling viewport inside its frame. All styling flows from the existing
theme tokens in `styles.go`; no new hex, no bare package-level style vars.

> **Proof-location note:** the task file references a `proof/` directory with
> `1.0-*` screenshots. Per the SDD Phase-3 protocol the single markdown proof file
> and its screenshots are consolidated here under `01-proofs/`.

## What This Task Proves

- A pure-presentation `panel(title, body, width, height, focused)` helper draws the
  rounded corners, hand-composites the title into the top edge (ADR 0001), truncates
  over-long titles with `…`, and keeps total `Width`/`Height` equal to the requested
  outer dimensions for both focus states.
- The selector renders inside a "Workflows" panel with the bubbles-list internal
  title/help chrome disabled and its own footer line below the box.
- The detail screen titles its panel with the workflow name and falls back to the
  file path when the workflow is unnamed/unparseable.
- The runs screen renders inside a "Runs" panel, scrolling a long run list within
  the frame while keeping the selected row visible.
- No regression: the full test suite passes, `go vet` is clean, and `gofmt -l`
  reports no files.

## Evidence Summary

- `go test ./internal/tui -run 'TestPanel|TestPanelFrame|TestSelector|TestDetail|TestRuns'`
  passes — one test per Unit-1 functional requirement.
- `go test ./...` passes across every package; `go vet ./...` is clean; `gofmt -l .`
  prints nothing.
- Rendered text screenshots show the framed, titled selector and runs screens with
  chrome stripped and the footer below the box.

## Artifact: Panel helper unit tests

**What it proves:** The pure-presentation helper draws corners, composites and
truncates the title, and holds stable outer dimensions.

**Why it matters:** Every screen builds on this primitive; if the geometry or
truncation is wrong, every panel misaligns.

**Command:**

~~~bash
go test ./internal/tui -run 'TestPanel|TestPanelFrame' -v
~~~

**Result summary:** All cases pass, including a per-line width assertion that catches
a box body narrower than its top edge, and the over-long-title `…` truncation case.

~~~
--- PASS: TestPanel (0.00s)
    --- PASS: TestPanel/focused_short_title (0.00s)
    --- PASS: TestPanel/blurred_short_title (0.00s)
    --- PASS: TestPanel/overlong_title_truncates (0.00s)
--- PASS: TestPanelFrame (0.00s)
~~~

## Artifact: Screen tests (selector / detail / runs)

**What it proves:** Each single-view screen carries its title on the top edge, the
selector strips list chrome, detail falls back to the path, and runs keeps the
selection visible while scrolling.

**Why it matters:** These are the per-FR test artifacts required by the planning audit
(traceability gate), one assertion per functional requirement.

**Command:**

~~~bash
go test ./internal/tui -run 'TestSelector|TestDetail|TestRuns' -v
~~~

**Result summary:** All pass. `TestSelector` asserts `ShowTitle()/ShowHelp()` are off
and "Workflows" is on the top edge; `TestDetail` asserts name-title and path-fallback;
`TestRuns` drives the cursor to the last of 30 runs and asserts it stays within the
scrolled viewport.

~~~
--- PASS: TestDetail (0.00s)
    --- PASS: TestDetail/named_workflow_titles_with_the_name (0.00s)
    --- PASS: TestDetail/unnamed_workflow_falls_back_to_path (0.00s)
--- PASS: TestRuns (0.01s)
--- PASS: TestSelector (0.00s)
~~~

## Artifact: No-regression gates

**What it proves:** The change introduces no test, vet, or formatting regressions.

**Why it matters:** Repo standards require `go test ./...`, `go vet ./...`, and
`gofmt -l .` clean before completion.

**Command:**

~~~bash
go test ./... && go vet ./... && gofmt -l .
~~~

**Result summary:** Every package reports `ok`; vet exits 0; `gofmt -l .` prints
nothing (no unformatted files).

~~~
ok  	jig/internal/engine	1.251s
ok  	jig/internal/runner	0.418s
ok  	jig/internal/tui	0.445s
ok  	jig/internal/workflow	(cached)
# vet: clean (exit 0)
# gofmt -l .: (no output)
~~~

## Artifact: Selector screen screenshot

**What it proves:** The selector renders as a rounded box titled "Workflows" with the
workflow list inside and no list-internal title/help, and a plain footer below the box.

**Why it matters:** Demonstrates the titled frame on a chrome-stripped bubbles-list
screen — the primary Unit-1 visual outcome.

**Artifact path:** `docs/specs/01-spec-tui-bordered-screens/01-proofs/1.0-selector.txt`

**Result summary:** The "Workflows" title sits in the top border edge; the list shows
the selected row's Charple bar; the hint line renders below the box, unboxed.

~~~
╭─ Workflows ──────────────────────────────────────────────────────────╮
│                                                                      │
│ │ feature                                                            │
│ │ Kitchen-sink reference workflow                                    │
│                                                                      │
│   bugfix                                                             │
│   Reproduce, fix, and verify a bug                                   │
│                                                                      │
│   review                                                             │
│   Multi-agent code review with a human gate                          │
│                                                                      │
╰──────────────────────────────────────────────────────────────────────╯
  ↑/↓ navigate  •  / filter  •  enter open  •  ctrl+c quit
~~~

## Artifact: Runs screen screenshot

**What it proves:** The runs screen renders as a rounded box titled "Runs" with a
scrolling run list inside and the plain footer hint line below the box.

**Why it matters:** Demonstrates the new viewport-backed runs frame (runs previously
had no viewport) with the footer-below-box convention.

**Artifact path:** `docs/specs/01-spec-tui-bordered-screens/01-proofs/1.0-runs.txt`

**Result summary:** The "Runs" title sits in the top edge; rows render inside the
framed viewport with a selected-row cursor bar; the footer renders below the box.

~~~
╭─ Runs ───────────────────────────────────────────────────────────────╮
│ ▌ run-a1b2c3              feature               running  1/3 steps   │
│   run-d4e5f6              feature               done  0/3 steps      │
│   run-77aa88              feature               running  0/3 steps   │
│   run-90ffee              feature               running  0/3 steps   │
│   run-cc1122              feature               running  0/3 steps   │
│   run-334455              feature               running  0/3 steps   │
│   run-667788              feature               running  0/3 steps   │
│   run-99aabb              feature               running  0/3 steps   │
│                                                                      │
╰──────────────────────────────────────────────────────────────────────╯
  r new run  •  enter monitor  •  esc back  •  ctrl+c quit
~~~

## Reviewer Conclusion

The Unit-1 functional requirements are met and independently demonstrable: a single
tested panel helper defines the frame once, and the selector, detail, and runs screens
all render inside titled rounded boxes with their chrome folded into the border title
and a plain footer below. Every FR maps to a passing test, and the no-regression gates
(test/vet/gofmt) are clean.
