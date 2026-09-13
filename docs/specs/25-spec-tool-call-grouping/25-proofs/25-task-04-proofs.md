# Task 04 Proofs - Lifecycle integrity and acceptance

## Task Summary

This task proves grouped reads remain atomic through search and filters, own
exact collapsed and expanded line ranges, preserve or clear state at the correct
lifecycle boundaries, and introduce no repository-level regression.

## What This Task Proves

- FR-08.21: member input, output, location, content, error, role, retry, and tool
  matches retain the complete group.
- FR-08.23–FR-08.25: line ranges cover exactly the group output; same-step reload
  and resize preserve expansion while invalidating renders; step changes and
  removed pages clear state; persistence-off stays empty.
- The complete feature passes focused, race, build, root test, vet, formatting,
  diff-quality, credential, and synthetic terminal-smoke checks.

## Evidence Summary

All required commands passed. The root suite required a second run with local
loopback permission because its existing notification test uses `httptest`; that
rerun passed and is recorded accurately in `test.txt`. All fixtures and captures
use fabricated paths, content, and temporary run directories.

## Artifact: Focused integrity suite

**What it proves:** Search/filter atomicity, exact line ranges, reload/resize
semantics, state pruning, step reset, and persistence-off behavior.

**Why it matters:** Grouping must remain a presentation projection and cannot
distort selection, filtering, durable boundaries, or viewport bookkeeping.

**Command:**

```bash
go test ./internal/tui/monitor -run 'TestReadGroup(Search|Filter|LineRanges|Reload|Resize|PersistenceOff)' -count=1
```

**Result summary:** The focused integrity suite passed.

```text
ok  	jig/internal/tui/monitor	0.342s
```

## Artifact: Synthetic persisted-transcript terminal smoke

**What it proves:** Real Monitor update paths support `n`/`N`, local/global
toggle, third-member search, error filtering, and narrow/wide resize while a
failed member remains visible.

**Why it matters:** This complements model assertions with reviewer-readable
end-to-end terminal observations against file-backed synthetic truth.

**Artifact path:** `25-task-04-monitor-smoke.md`

**Result summary:** The smoke passed at 80×24, 56×20, and 120×30 terminal sizes.

## Artifact: Repository acceptance commands

**What it proves:** The implementation meets repository compilation, test,
static-analysis, race, formatting, and diff-quality requirements.

**Why it matters:** Focused behavior is insufficient if surrounding packages or
repository standards regress.

**Artifact paths:**

- `25-task-04-acceptance/build.txt`
- `25-task-04-acceptance/test.txt`
- `25-task-04-acceptance/vet.txt`
- `25-task-04-acceptance/race-tui.txt`
- `25-task-04-acceptance/gofmt.txt`
- `25-task-04-acceptance/git-diff-check.txt`
- `25-task-04-acceptance/credential-scan.txt`

**Result summary:** Every acceptance gate passed; the proof scan found zero
credential-pattern matches.

## Complete Requirement Evidence Map

| Requirement | Evidence |
| --- | --- |
| FR-08.1 | Task 01 adjacent/singleton normalizer tests |
| FR-08.2 | Task 01 canonical kind, path, missing-target, and URI tests |
| FR-08.3 | Task 01 interruption and coordinate boundary tests |
| FR-08.4 | Task 01 ordered-member and interruption tests |
| FR-08.5 | Task 01 first-anchor key and reload tests |
| FR-08.6 | Task 01 bounded page fixtures and use-only behavior |
| FR-08.7 | Task 01 recursive member enumeration test |
| FR-08.8 | Task 01 surviving/stale page-state tests |
| FR-08.9 | Task 02 header/count and no-card tests plus gallery |
| FR-08.10 | Task 02 distinct-target tree tests |
| FR-08.11 | Task 02 full-path merge, order, de-duplication, and elision tests |
| FR-08.12 | Task 02 numeric selector and location-fallback tests |
| FR-08.13 | Shared TreePrefix visible-width tests |
| FR-08.14 | Task 02 success suppression and exceptional row-state tests |
| FR-08.15 | Task 02 aggregate failure/state-precedence tests |
| FR-08.16 | Task 02 narrow/wide ANSI-width tests and gallery |
| FR-08.17 | Task 03 one-stop navigation and cursor-identity tests |
| FR-08.18 | Task 03 local/global toggle tests |
| FR-08.19 | Task 03 expanded ordered-detail capture and tests |
| FR-08.20 | Task 03 structured-edit auto-expansion exclusion test |
| FR-08.21 | Task 04 third-member search/filter tests |
| FR-08.22 | Task 03 collapsed/expanded capture-before-reload copy tests |
| FR-08.23 | Task 04 collapsed/expanded exact line-range tests |
| FR-08.24 | Task 04 reload, resize, pruning, and step-change tests |
| FR-08.25 | Task 04 persistence-off test |

## Non-Goal Integrity Review

| Non-goal | Final-diff finding |
| --- | --- |
| Other tool kinds | Eligibility is closed to canonical reads; other tools remain standalone. |
| Displacement | No item removal or durable transcript mutation was added. |
| Wire formats and harnesses | No `internal/transcript`, `internal/toolcall`, harness, or adapter file changed. |
| Cross-coordinate/interruption grouping | Pure normalizer tests enforce every declared boundary. |
| Bespoke read previews | Expansion calls the existing tool-activity detail renderer. |
| Member-level navigation | Groups alone enter `chatVisibleItems`; member rows never become cursor stops. |
| Removed render-plan design | No render plan, group-header map, or parallel expansion state was introduced. |

## Reviewer Conclusion

The feature satisfies FR-08.1 through FR-08.25 with file-backed, model,
renderer, interaction, lifecycle, and repository-level evidence while remaining
inside every declared non-goal.
