# 26-audit-acp-only-harness.md

## Executive Summary

- Overall Status: PASS
- Required Gate Failures: 0
- Flagged Risks: 0 (2 addressed since Run 1)

## Gateboard

| Gate | Status | Why it failed (<=10 words) | Exact fix target |
| --- | --- | --- | --- |
| Requirement-to-test traceability | PASS | — | — |
| Proof artifact verifiability | PASS | — | — |
| Repository standards consistency | PASS | — | — |
| Open question resolution | PASS | — | — |
| Regression-risk blind spots | PASS (was FLAG) | resolved, see Re-Audit Delta | — |
| Non-goal leakage | PASS | — | — |

## Standards Evidence Table (Required)

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `AGENTS.md` | yes | Pre-v1 correctness over compat shims; TOML-only backend selection through `harness.For`; `gofmt`/`go vet`/`go test` + nested `harness/acp` module completion gate | none |
| `README.md` | yes | "Claude SDK/ACP, Cursor ACP, Codex ACP are supported" status line requires updating once SDK is removed | none |
| `docs/TESTING.md` | yes | Test observable contract at smallest seam; synthetic data only; `-race` for `internal/tui/...`, `internal/helpchat`, `internal/engine`, `internal/runner`, `internal/harness` | none |
| `docs/CONVENTIONS.md` | yes | Consumer-defined interfaces at the boundary (`MonitorAdapter`/`helpchat` depend on `harness.Harness`, not ACP wire types); comments explain non-obvious "why" | none |

## User-Approved Remediation Plan

- Completed (user approved folding both Run 1 fixes directly into the task
  list)

## Re-Audit Delta (Runs 2+ only)

- Changed gate statuses since Run 1: `Regression-risk blind spots` FLAG →
  PASS; `Non-goal leakage` FLAG (none found) → PASS.
- Still-failing REQUIRED gates: none.
- Remediation applied:
  1. Added proof artifact + sub-task 4.8 to `26-tasks-acp-only-harness.md`:
     a test that kills `jig mcp-serve` mid-conversation during a live
     `AcpHarness`-backed help-chat session and asserts the TUI surfaces a
     clear, recoverable error rather than hanging — the live-integration
     counterpart to Unit 3's standalone crash-handling test.
  2. Added sub-tasks 2.8 and 4.9 confirming cost/usage stays nil/zero on the
     ACP-backed `MonitorAdapter` and help-chat paths, matching spec 12's
     existing convention — resolves Open Question 2 as an explicit,
     checkable step in both units that touch it.
- Newly introduced findings: none.
