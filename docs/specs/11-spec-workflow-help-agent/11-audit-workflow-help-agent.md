# 11-audit-workflow-help-agent.md

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
| Regression-risk blind spots | FLAG | Happy-path only; no error/nil-run branch tests | See FLAG Findings |
| Non-goal leakage | PASS | — | — |

## Standards Evidence Table

| Source File | Read | Standards Extracted | Conflicts |
| --- | --- | --- | --- |
| `CLAUDE.md` | yes | All lipgloss styles in `styles.go`; `newInputTextarea` helper; value-receiver-with-shared-maps; `tea.KeyPressMsg` in tests | none |
| `internal/runner/agent.go` | yes | `claudecode.NewClient`, `WithSdkMcpServer`, `WithResume`, `WithContinueConversation`, `CreateSDKMcpServer` patterns | none |
| `internal/tui/shared/help.go` | yes | Compositor pattern: `NewCompositor` + `NewLayer` + `NewCanvas().Compose().Render()` | none |
| `internal/tui/chart/render.go` | yes | Same compositor pattern confirmed at second call site | none |
| `internal/tui/monitor/keys.go` | yes | `keybind.NewBinding(WithKeys(...), WithHelp(...))` binding format; ctrl+h is free | none |
| `internal/tui/monitor/monitor_model.go` | yes | Value-receiver pattern; map fields (chatExpand, chatRendered) persist across value copies | none |
| `AGENTS.md` | not found | — | — |
| `CONTRIBUTING.md` | not found | — | — |

## Findings (Only include when non-empty)

### FLAG Findings

1. **Regression-risk blind spots in task 3.7 and task 2.7 tests**
   - Risk: `TestToggleHelp_OpenClose` and `TestModelInit` cover the happy open/close path but do not cover the nil-run guard (journal-replayed run shows static message instead of connecting), nor the `ConnectErrMsg` path (API error renders inline, textarea stays open). Both are functional requirements in the spec (DU4 and the design considerations section). A regression in either branch would go undetected.
   - Suggested remediation: Add `TestToggleHelp_NilRun` to `monitor_test.go` asserting that when `m.run == nil`, opening the modal pre-populates a static error turn without firing `connectCmd`. Add `TestModel_ConnectErr` to `helpchat_test.go` asserting that `ConnectErrMsg` produces an error turn without panicking. Both are small additions to the existing test files in tasks 1.6 and 3.7.
