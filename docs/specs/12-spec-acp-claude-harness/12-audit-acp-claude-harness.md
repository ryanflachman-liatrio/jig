# 12-audit-acp-claude-harness.md

## Executive Summary

- Overall Status: PASS
- Required Gate Failures: 0
- Flagged Risks: 1

## Gateboard

| Gate | Status | Why it failed (<=10 words) | Exact fix target |
| --- | --- | --- | --- |
| Requirement-to-test traceability | PASS | — | — |
| Proof artifact verifiability | PASS | — | — |
| Repository standards consistency | PASS | — | — |
| Open question resolution | PASS | — | — |
| Regression-risk blind spots | FLAG | Unit 3 relies on manual diff, no automated regression test | `## Tasks > 3.0 Tasks` |
| Non-goal leakage | PASS | — | — |

## Standards Evidence Table (Required)

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `CLAUDE.md` | yes | Table-driven tests; consumer-defined interfaces; comments explain "why"; `gofmt`/`go vet` before commit | none |
| `README.md` | yes | Go 1.25; `go build ./cmd/jig`; `go run ./cmd/jig validate` smoke check | none |
| `go.mod` / `mise.toml` | yes | Module `jig`, Go 1.25.8 pinned, no ACP dep yet | none |

(`AGENTS.md`/`CONTRIBUTING.md`/PR template not found in repo — `CLAUDE.md` serves as the primary standards source, already read.)

## Findings

### FLAG Findings (max 2 in main report)

1. **Unit 3's "zero behavior change" proof relies on a one-time manual transcript diff (3.11), not an automated regression test.**
   - Risk: a future change could silently alter transcript shape without a test catching it, since the proof artifact is a manual diff at task-completion time rather than a repeatable assertion.
   - Suggested remediation: optional — add a golden-file comparison test if this becomes a recurring regression risk; not blocking for this slice since 3.9 (existing `internal/runner` tests passing unchanged) already provides ongoing regression coverage for the underlying logic.

## User-Approved Remediation Plan

- Approved | Completed — added 4.7 (`acp_test.go` translation coverage) and
  4.9 (`select_test.go` `FromEnv()` coverage) to
  `12-tasks-acp-claude-harness.md`, renumbering 4.7–4.11 to 4.8–4.13.

## Re-Audit Delta (Runs 2+ only)

- Changed gate statuses since previous run: Requirement-to-test traceability
  FAIL → PASS (both findings resolved by the two added sub-tasks).
- Still-failing REQUIRED gates: none.
- Newly introduced findings: none.
