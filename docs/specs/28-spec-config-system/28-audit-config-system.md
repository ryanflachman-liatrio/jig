# 28-audit-config-system.md

## Executive Summary

- Overall Status: PASS
- Required Gate Failures: 0
- Flagged Risks: 0 (both prior FLAG findings remediated; see delta)

## Gateboard

| Gate | Status |
| --- | --- |
| Requirement-to-test traceability | PASS |
| Proof artifact verifiability | PASS |
| Repository standards consistency | PASS |
| Open question resolution | PASS |

## Standards Evidence Table (Required)

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `AGENTS.md` | yes | Pre-v1 removal-not-shims policy; "persistence-off is supported... do not join an empty root into an unintended relative write"; backend selection is unrelated to this spec | none |
| `docs/CONVENTIONS.md` | yes | Typed structs over stringly-typed maps; resolve precedence once in the loader; wrap errors with `%w` except where a documented security exception applies; remove replaced paths and update consumers together | none |
| `docs/ARCHITECTURE.md` | yes | Package table format and boundary-description convention; engine/runner import direction rules (not directly touched by this spec) | none |
| `docs/TESTING.md` | yes | Test the observable contract at the smallest useful seam; root build/test/vet command set; focused package test invocation pattern | none |
| `README.md` | yes | Confirms current CLI surface (`init`, `validate`, `run`, `notifications`, etc.) matches `cmd/jig/main.go`'s dispatch table | none |

## Findings

No REQUIRED or FLAG findings remain open after remediation (see delta below).

## User-Approved Remediation Plan

- Completed

## Re-Audit Delta (Runs 2+ only)

- Changed gate statuses since previous run: Requirement-to-test traceability FAIL → PASS; Repository standards consistency FAIL → PASS.
- Still-failing REQUIRED gates: none.
- Remediation applied:
  1. `## Tasks > 1.0 > 1.2` now specifies `ProjectConfigPath` joins `root` directly (matching `notification.LoadLocalConfig`'s/`prefs.Path`'s existing convention) and returns `""` for `root == ""` without joining.
  2. `## Tasks > 1.0 > 1.3, 1.4` now state `loadFile`/`Load` never substitute a hardcoded root; `root` is always caller-supplied, including `""` for persistence-off.
  3. `## Tasks > 1.0 > 1.6, 1.7` now specify `loadEffectiveConfig(root string)` takes an explicit root and each of the four entry points (bare `jig`, `runRun`, `notificationsCheck`, the new `runConfig`, which also gains its own `--root` flag) passes the root it already owns, instead of a hardcoded `.jig` inside the helper.
  4. `## Tasks > 3.0 > 3.4, 3.5` now thread `Runtime`'s existing `root`/`notificationsCheck`'s existing `--root` flag into `loadEffectiveConfig`, instead of an implicit default.
  5. `## Tasks > 3.0 > 3.9a` (new) adds the previously-missing `glyph_preset` case-sensitivity/invalid-value test to `internal/config/load_test.go`.
  6. `## Tasks > 1.0 > 1.8` and `## Tasks > 3.0 > 3.10` now include an explicit persistence-off (`root == ""`) regression case (closes prior FLAG #1).
  7. `## Tasks > 4.0 > 4.3, 4.6` now name a single converter pair owned by `internal/config` (closes prior FLAG #2).
- Newly introduced findings: none.
