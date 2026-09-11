# Task 04 Proofs - Documented and regression-verified mouse contract

## Task Summary

The TUI guide now documents the complete keyboard-primary mouse contract, and the focused, race, build, repository test, vet, diff, scope, and sanitization gates have been executed.

## What This Task Proves

- Documentation names every supported click/wheel surface, exact movement, focus behavior, exclusions, and no-configuration keyboard primacy.
- All TUI packages pass normally and under the race detector.
- The root binary builds; all repository packages test and vet successfully.
- The final scope contains no dependency/schema/backend/engine changes or mouse activation/configuration path.

## Evidence Summary

Every required command ultimately passed. The first repository-wide test attempt was accurately recorded as a sandbox network-bind blocker; its authorized rerun passed all packages.

## Artifact: Focused TUI tests

**What it proves:** Mouse behavior and existing TUI regression coverage pass together.

**Why it matters:** This is the primary automated verification for FR-14 and FR-15.

**Artifact path:** `docs/specs/25-spec-optional-mouse-navigation/artifacts/25-4-focused-tests.txt`

**Result summary:** All TUI packages passed on Go 1.25.12.

## Artifact: Repository regression checks

**What it proves:** Race, build, full test, and vet gates pass.

**Why it matters:** It demonstrates the pointer routing did not regress broader runtime behavior.

**Artifact path:** `docs/specs/25-spec-optional-mouse-navigation/artifacts/25-4-regression-checks.txt`

**Result summary:** All four required gates passed; the only initial blocker was sandbox denial of existing httptest listeners, resolved by the permitted rerun.

## Artifact: Scope and sanitization review

**What it proves:** The implementation matches FR-01 through FR-15 and excludes all declared non-goals and sensitive data.

**Why it matters:** It gives validation a concise inventory and security boundary.

**Artifact path:** `docs/specs/25-spec-optional-mouse-navigation/artifacts/25-4-scope-review.txt`

**Result summary:** Diff checks and credential-pattern scans passed; open goals remain unchanged pending validation.

## Reviewer Conclusion

The feature is documented, regression-verified, scoped to TUI ownership, and ready for independent Phase 4 validation.
