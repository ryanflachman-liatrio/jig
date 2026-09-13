# Task 03 Proofs - One-stop navigation, expansion, and copy

## Task Summary

This task connects grouped reads to the existing transcript cursor, local
toggle, expand-all, detail rendering, and selected-item clipboard paths without
adding member-level navigation or a parallel expansion model.

## What This Task Proves

- FR-08.17–FR-08.18: a group is one visible cursor stop and existing local/global
  expansion state remains authoritative.
- FR-08.19–FR-08.20: expansion reveals original member detail in order, while
  read groups remain excluded from structured-edit auto-expansion.
- FR-08.22: copy includes the visible header/tree and includes member evidence
  only when that captured group is expanded.

## Evidence Summary

Bubble Tea key-message tests traverse text → group → text with `n`/`N`, toggle
the group twice with `enter`, toggle global expansion twice with `o`, and verify
cursor identity, visible-item count, and per-item map stability. Clipboard tests
verify collapsed/expanded content and capture-before-reload behavior.

## Artifact: Focused interaction suite

**What it proves:** Navigation, local toggle, global override, ordered detail,
auto-expansion exclusion, and copy behavior work through production model paths.

**Why it matters:** Compact grouping must not change keyboard semantics or make
hidden output silently appear in a collapsed clipboard capture.

**Command:**

```bash
go test ./internal/tui/monitor -run 'TestReadGroup(Toggle|Navigation|ExpandAll|Copy|Expanded)' -count=1
```

**Result summary:** The focused interaction suite passed.

```text
ok  	jig/internal/tui/monitor	0.438s
```

## Artifact: Collapsed and expanded interaction capture

**What it proves:** The same synthetic group retains its header/tree while member
details appear only in the expanded scene.

**Why it matters:** A reviewer can inspect the complete disclosure cycle without
using real run data.

**Artifact path:** `25-task-03-read-group-interaction.txt`

**Exact interaction sequence:** `n` selects the group, `enter` expands, `enter`
collapses, `o` expands all without rewriting the local map, `o` collapses all,
and `N`/`n` traverse the adjacent items.

**Result summary:** The dedicated generated capture contains nine labeled model
states: the initial item before the group, group selection, local expansion and
collapse, global expansion and collapse, the item after the group, and reverse
navigation back through the group to the preceding item. Every state records the
cursor index, selected kind, visible-item count, local expansion value, and
expand-all override using fabricated paths and output.

## Reviewer Conclusion

Grouped reads behave as one ordinary transcript item for navigation, expansion,
and clipboard capture while retaining every member's detail evidence.
