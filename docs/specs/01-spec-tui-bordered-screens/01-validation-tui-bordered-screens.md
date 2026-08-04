# 01-validation-tui-bordered-screens.md

## 1) Executive Summary

**Overall: PASS** — all mandatory gates clear.

**Implementation Ready: Yes.** All three Demoable Units are implemented, every required test passes (including under the race detector), the quality gates are clean, and all proof artifacts are present and demonstrate the specified functionality. One traceability note (MEDIUM) is recorded for `internal/tui/viewer.go`, which was changed but not listed in the planning-era Relevant Files; the change is directly linked to Task 3.5 via commit message and task notes.

**Key metrics:**
- Requirements Verified: 100% (all Unit 1, 2, and 3 FRs — plus Repository Standards)
- Proof Artifacts Working: 100% (10 screenshot/text artifacts present; all 10 required tests pass)
- Files Changed: 15 source files (14 in Relevant Files list + 1 unlisted supporting change with clear task linkage)

---

## 2) Coverage Matrix

### Functional Requirements

| Requirement | Status | Evidence |
|---|---|---|
| **Unit 1** | | |
| FR-1.1 Pure-presentation `panel()` helper in `panel.go` | Verified | `internal/tui/panel.go` exists; `TestPanel` passes |
| FR-1.2 Manual top-edge compositing (ADR 0001) | Verified | `panel.go` comments cross-ref ADR 0001; `docs/adr/0001-manual-border-title-compositing.md` exists |
| FR-1.3 Over-long title truncated with `…` | Verified | `TestPanel/overlong_title_truncates` passes |
| FR-1.4 Title left-aligned, 1-cell offset, 1-space padding | Verified | `TestPanel/focused_short_title` asserts corner runes + title text on top edge |
| FR-1.5 Focus flag → primary (Charple) / dim (Iron) border color | Verified | `TestPanel/focused_short_title` and `TestPanel/blurred_short_title` pass; styles derive from `theme.Panel` tokens in `styles.go` |
| FR-1.6 Frame helpers expose inner size without magic numbers | Verified | `TestPanelFrame` passes; `panelFrame()` used in all callers |
| FR-1.7 Single-panel screens always use focused border | Verified | `selector.go`, `detail.go`, `runs.go` all call `panel(…, focused=true)`; screenshots confirm primary border |
| FR-1.8 Selector in "Workflows" panel, list chrome stripped | Verified | `TestSelector` passes; `1.0-selector.txt` shows `╭─ Workflows` with no internal list title/help |
| FR-1.9 Detail panel titled with workflow name / file-path fallback | Verified | `TestDetail/named_workflow_titles_with_the_name` and `TestDetail/unnamed_workflow_falls_back_to_path` pass |
| FR-1.10 Runs panel titled "Runs" with framed scrolling viewport | Verified | `TestRuns` passes; `1.0-runs.txt` shows `╭─ Runs` panel with 8 run rows and footer below box |
| FR-1.11 Footer as plain line below each panel | Verified | All screenshots show footer below the box border |
| FR-1.12 Re-fit panels and inner content on `WindowSizeMsg` | Verified | Second `WindowSizeMsg` resize assertions in monitor and chat tests pass; all callers implement `resize()` |
| FR-1.13 All new styles in `styles.go` via theme tokens; no bare hex | Verified | `styles.go` diff: `Panel` sub-struct added; no hardcoded hex in any changed file |
| **Unit 2** | | |
| FR-2.1 Two-panel `JoinHorizontal` layout ("Steps" + transcript) | Verified | `TestMonitorTwoPanel` passes; `2.0-two-panel.txt` shows both panels side by side |
| FR-2.2 Both panels simultaneously visible | Verified | `2.0-two-panel.txt` screenshot; `TestMonitorTwoPanel` asserts both titles |
| FR-2.3 `focus` field (Steps/Transcript/Gate) replaces `mode` | Verified | `TestMonitorTwoPanel` tab-key asserts focus-color toggle; `mode`/`modeList`/`modeChat` removed per Task 2.1 |
| FR-2.4 Panel sizing: left `max(32, width/3)`, right remainder, inner ≥ ~40 | Verified | Task 2.2 checked off; code uses `max(32, m.width/3)` with clamp; `TestMonitorTwoPanel` second `WindowSizeMsg` asserts no overflow |
| FR-2.5 glamour wrap from transcript panel's inner width; cache invalidated on resize | Verified | Task 2.5 checked off; `rebuildRenderer` drives off transcript inner width |
| FR-2.6 Internal title/header rows removed from `listBody()`/`chatBody()` | Verified | Task 2.4 checked off; no leading title lines in either body function |
| FR-2.7 Focus-routed key dispatch (Steps: j/k select; Transcript: j/k/scroll/n/N/enter/space/o) | Verified | `TestMonitorTwoPanel` tests key routing per region |
| FR-2.8 `tab`/`shift+tab`/`left`/`right` cycle focus | Verified | `TestMonitorTwoPanel` tab-key focus cycle asserts; footer hint reflects keys; `2.0-two-panel.txt` footer shows `tab/←/→ focus` |
| FR-2.9 Eager transcript reload on every selection change; per-step state reset | Verified | `TestMonitorEagerReload` passes |
| FR-2.10 Pending gates rendered as non-blocking full-width strip beneath panels | Verified | `2.0-gate.txt` shows review gate strip below both panels; `TestMonitorGateNonBlocking` passes |
| FR-2.11 Gate auto-focuses on arrival; navigation stays live | Verified | `TestMonitorGateNonBlocking` asserts focus-switch key still moves focus while gate is pending |
| FR-2.12 Gate resolution semantics retained (digits, multi-select, `m`, textarea submit, cancellation) | Verified | `TestMonitorGateNonBlocking` asserts cancellation emits `agentQuestionResponseMsg{answer:"cancelled"}` |
| FR-2.13 Narrow-terminal fallback (<~76 cols): single focused panel shown | Verified | Task 2.9 checked off; `2.0-narrow.txt` shows single panel with intact border |
| FR-2.14 Footer/status hint line below panels and gate strip | Verified | `2.0-gate.txt` and `2.0-two-panel.txt` show footer at bottom |
| FR-2.15 Persistence-off (`runDir == ""`) path unaffected | Verified | Most monitor tests use `runDir = ""`; all pass |
| **Unit 3** | | |
| FR-3.1 "Conversation" + "Message" panels using `panel()` helper | Verified | `TestChatPanels` passes; `3.0-chat.txt` shows `╭─ Conversation · Turn 2 of 2` and `╭─ Message` |
| FR-3.2 Conversation title carries turn info when >1 turn | Verified | `TestChatStreamingTitle` passes; `3.0-chat.txt` shows `Conversation · Turn 2 of 2` |
| FR-3.3 "connecting…" only until connected, never persistent "Connected" | Verified | `TestChatStreamingTitle` asserts transient connecting state |
| FR-3.4 Fatal error as full-width line beneath panels | Verified | Task 3.4 checked off; `fatalLine()` renders beneath panels, separate from title |
| FR-3.5 Input panel owns frame; no double-box (textarea border removed) | Verified | `viewer.go` diff shows `m.viewport.Style = lipgloss.NewStyle()` (no own border); task 3.3 checked off |
| FR-3.6 "Message · responding…" while streaming, clears when idle | Verified | `TestChatStreamingTitle` passes; `3.0-chat-streaming.txt` shows `╭─ Message · responding… ` |
| FR-3.7 `headerView`/`turnIndicatorView`/`statusLineView` removed | Verified | Task 3.6 checked off; methods deleted in commit 597ee43 |
| FR-3.8 All styling from existing theme tokens | Verified | `styles.go` diff: no new hex; `Panel` sub-struct builds from existing `primary`/`hexIron`/`fgMuted` tokens |
| FR-3.9 `chatModel.View()` returns `tea.View` (standalone root model) | Verified | `chat.go` return type unchanged; task 3.5 preserves `tea.View` return |

### Repository Standards

| Standard Area | Status | Evidence & Compliance Notes |
|---|---|---|
| Styles in `styles.go` from theme tokens; no bare hex, no bare package vars | Verified | `styles.go` has `Panel` sub-struct set in `DefaultTheme()`; `grep -r 'lipgloss.NewStyle' internal/tui/*.go` finds only `viewer.go` viewport reset (justified: removing focus border to avoid double-box) and test helpers |
| Layout math uses lipgloss frame helpers (no magic numbers) | Verified | `panelFrame()` used in all callers; `GetHorizontalFrameSize`/`GetVerticalFrameSize` used in `resize()` paths |
| Comments explain non-obvious "why" | Verified | `panel.go` cross-refs ADR 0001; `viewer.go` diff comment: "No own border: the Conversation panel draws the frame and its color reflects focus. A bordered viewport here would double-box." |
| Table-driven tests; `tea.KeyPressMsg{Code:…, Text:…}` construction | Verified | All new test files follow the pattern; `chat_test.go`, `monitor_test.go`, `panel_test.go` all use `tea.KeyPressMsg` |
| v2 idioms: sub-models return `string`; only `chatModel`/`rootModel` return `tea.View` | Verified | `chat.go` returns `tea.View`; `monitor.go`, `selector.go`, `detail.go`, `runs.go` all return `string` |
| `gofmt -l -w .` and `go vet ./...` clean | Verified | `go vet ./...` → clean; `gofmt -l internal/tui` → no output |
| `go test ./... -race` passes | Verified | All packages pass; no data races reported |

### Proof Artifacts

| Unit/Task | Proof Artifact | Status | Verification Result |
|---|---|---|---|
| Unit 1 / Task 1.3 | Test: `go test ./internal/tui -run TestPanel -v` | Verified | `TestPanel/focused_short_title`, `TestPanel/blurred_short_title`, `TestPanel/overlong_title_truncates` all PASS |
| Unit 1 / Task 1.3 | Test: `go test ./internal/tui -run TestPanelFrame -v` | Verified | `TestPanelFrame` PASS |
| Unit 1 / Task 1.5 | Test: `go test ./internal/tui -run TestSelector -v` | Verified | `TestSelector` PASS |
| Unit 1 / Task 1.9 | Test: `go test ./internal/tui -run TestDetail -v` | Verified | `TestDetail/named_workflow_titles_with_the_name`, `TestDetail/unnamed_workflow_falls_back_to_path` PASS |
| Unit 1 / Task 1.10 | Test: `go test ./internal/tui -run TestRuns -v` | Verified | `TestRuns` PASS |
| Unit 1 / Task 1.8 | Screenshot: `01-proofs/1.0-selector.txt` | Verified | File exists; shows `╭─ Workflows` with list items and footer below box; no list-internal chrome |
| Unit 1 / Task 1.8 | Screenshot: `01-proofs/1.0-runs.txt` | Verified | File exists; shows `╭─ Runs` panel with 8 scrollable run rows and footer hint line below |
| Unit 1 / Task 1.8 | CLI: `go test ./internal/tui && go vet ./... && gofmt -l internal/tui` | Verified | All pass; gofmt reports no files |
| Unit 2 / Task 2.11 | Test: `go test ./internal/tui -run TestMonitorTwoPanel -v` | Verified | PASS — both panel titles present; tab toggles focus-border region |
| Unit 2 / Task 2.11 | Test: `go test ./internal/tui -run TestMonitorGateNonBlocking -v` | Verified | PASS — focus-switch key moves focus while gate pending; gate resolution and cancellation deliver correct response |
| Unit 2 / Task 2.11 | Test: `go test ./internal/tui -run TestMonitorEagerReload -v` | Verified | PASS — cursor move reloads transcript panel to newly-selected step |
| Unit 2 / Task 2.12 | Screenshot: `01-proofs/2.0-two-panel.txt` | Verified | File exists; shows "Steps" panel (32-col wide) and step-id panel side by side; focused panel's border visibly distinct |
| Unit 2 / Task 2.12 | Screenshot: `01-proofs/2.0-gate.txt` | Verified | File exists; shows review gate strip beneath both panels with focus on transcript panel |
| Unit 2 / Task 2.12 | Screenshot: `01-proofs/2.0-narrow.txt` | Verified | File exists; shows single "Steps" panel full-width with intact rounded border |
| Unit 2 / Task 2.12 | CLI: `go test ./... -race && go vet ./... && gofmt -l .` | Verified | Full suite passes under race detector; vet and gofmt clean |
| Unit 3 / Task 3.7 | Test: `go test ./internal/tui -run TestChatPanels -v` | Verified | PASS — "Conversation" and "Message" titles present; focus toggle moves primary border |
| Unit 3 / Task 3.7 | Test: `go test ./internal/tui -run TestChatStreamingTitle -v` | Verified | PASS — streaming title "Message · responding…" set while streaming, cleared when idle; turn info in Conversation title when >1 turn |
| Unit 3 / Task 3.8 | Screenshot: `01-proofs/3.0-chat.txt` | Verified | File exists; shows `╭─ Conversation · Turn 2 of 2` and `╭─ Message` panels; focused border highlighted |
| Unit 3 / Task 3.8 | Screenshot: `01-proofs/3.0-chat-streaming.txt` | Verified | File exists; shows `╭─ Message · responding… ` in input panel title mid-stream |
| Unit 3 / Task 3.8 | CLI: `go test ./... && go vet ./...` | Verified | Full suite passes; vet clean |

---

## 3) Validation Issues

| Severity | Issue | Impact | Recommendation |
|---|---|---|---|
| MEDIUM | **Supporting-file linkage gap: `internal/tui/viewer.go` not in Relevant Files.** `viewer.go` contains `chatModel.handleResize()` and was modified in commit `597ee43` to adopt `panelFrame()` for the chat's panel layout. Task 3.5 explicitly calls for "Recompute both panels' inner sizes on `WindowSizeMsg` (`handleResize`) from `panelFrame()`" and the commit message references the paneled chat restructure — the linkage is clear, but `viewer.go` was not added to the planning-era Relevant Files table in `01-tasks-tui-bordered-screens.md`. Evidence: `git diff fdfb278..HEAD -- internal/tui/viewer.go` shows only chat-panel resize logic. | Traceability gap only; requirement coverage is unaffected (FR-3.5 and FR-3.9 are fully verified by test and screenshot). | Add `internal/tui/viewer.go` to the Relevant Files table in the task list with the note "Modified by Task 3.5 — `handleResize` adopts `panelFrame()` for Conversation + Message inner sizing." |

---

## 4) Evidence Appendix

### Git Commits Analyzed

| Commit | Message | Relevant Files |
|---|---|---|
| `fdfb278` | `chore: plan out how to add borders to each panel` | `CONTEXT.md`, ADR 0001/0002, `01-spec-tui-bordered-screens.md` (spec creation baseline) |
| `e5448e7` | `feat: add titled-panel helper and frame selector/detail/runs` | `panel.go`, `panel_test.go`, `styles.go`, `selector.go`, `selector_test.go`, `detail.go`, `detail_test.go`, `runs.go`, `runs_test.go`, proof `1.0-*`, task list |
| `51bbcac` | `feat: two-panel run monitor with focus borders and non-blocking gates` | `monitor.go`, `monitor_test.go`, proof `2.0-*`, task list |
| `597ee43` | `feat: paneled streaming chat client with titled Conversation/Message panels` | `chat.go`, `chat_test.go`, `input.go`, `styles.go`, `viewer.go`, proof `3.0-*`, task list |

### Test Execution Results

```
$ go test ./internal/tui -run "TestPanel|TestPanelFrame|TestSelector|TestDetail|TestRuns|TestMonitorTwoPanel|TestMonitorGateNonBlocking|TestMonitorEagerReload|TestChatPanels|TestChatStreamingTitle" -v

=== RUN   TestChatPanels
--- PASS: TestChatPanels (0.00s)
=== RUN   TestChatStreamingTitle
--- PASS: TestChatStreamingTitle (0.00s)
=== RUN   TestDetail
=== RUN   TestDetail/named_workflow_titles_with_the_name
=== RUN   TestDetail/unnamed_workflow_falls_back_to_path
--- PASS: TestDetail (0.00s)
    --- PASS: TestDetail/named_workflow_titles_with_the_name (0.00s)
    --- PASS: TestDetail/unnamed_workflow_falls_back_to_path (0.00s)
=== RUN   TestMonitorTwoPanel
--- PASS: TestMonitorTwoPanel (0.00s)
=== RUN   TestMonitorGateNonBlocking
--- PASS: TestMonitorGateNonBlocking (0.00s)
=== RUN   TestMonitorEagerReload
--- PASS: TestMonitorEagerReload (0.01s)
=== RUN   TestPanel
=== RUN   TestPanel/focused_short_title
=== RUN   TestPanel/blurred_short_title
=== RUN   TestPanel/overlong_title_truncates
--- PASS: TestPanel (0.00s)
    --- PASS: TestPanel/focused_short_title (0.00s)
    --- PASS: TestPanel/blurred_short_title (0.00s)
    --- PASS: TestPanel/overlong_title_truncates (0.00s)
=== RUN   TestPanelFrame
--- PASS: TestPanelFrame (0.00s)
=== RUN   TestRuns
--- PASS: TestRuns (0.00s)
=== RUN   TestSelector
--- PASS: TestSelector (0.00s)
PASS
ok  	jig/internal/tui	0.492s
```

```
$ go test ./... -race
ok  	jig/internal/datastore	(cached)
ok  	jig/internal/engine	(cached)
ok  	jig/internal/runner	(cached)
ok  	jig/internal/transcript	(cached)
ok  	jig/internal/tui	(cached)
ok  	jig/internal/workflow	(cached)
```

```
$ go vet ./...
(no output — clean)

$ gofmt -l internal/tui
(no output — clean)
```

### Key Proof Artifact Content

**`1.0-selector.txt`** — selector as titled rounded panel, no list chrome:
```
╭─ Workflows ──────────────────────────────────────────────────────────╮
│ │ feature                                                            │
│ │ Kitchen-sink reference workflow                                    │
│   bugfix                                                             │
│   review                                                             │
╰──────────────────────────────────────────────────────────────────────╯
  ↑/↓ navigate  •  / filter  •  enter open  •  ctrl+c quit
```

**`2.0-two-panel.txt`** — side-by-side Steps + transcript panels, focused border:
```
╭─ Steps ──────────────────────╮╭─ generate ───────────────────────────...╮
│   ✓  plan      succeeded     ││   ●  running                            │
│ ▌ ●  generate  running       ││   #1 assistant                          │
│   ○  review    pending       ││   ▌ ▸ ◇ reasoning  Planning the change  │
╰──────────────────────────────╯╰─────────────────────────────────────────╯
  running  ·  tab/←/→ focus  •  j/k scroll  •  n/N block  •  enter expand
```

**`2.0-gate.txt`** — non-blocking review gate strip beneath both panels:
```
╭─ Steps ──────────────────────╮╭─ plan ───...╮
│ ▌ ○  plan      pending       ││ ○ pending  │
╰──────────────────────────────╯╰────────────╯
╭─ Review — review ──────────────────────────────────────────────╮
│   [1] approve   [2] reject   [m] message                       │
╰────────────────────────────────────────────────────────────────╯
  awaiting review  ·  tab to gate  •  tab/←/→ focus  ...
```

**`3.0-chat-streaming.txt`** — streaming indicator in Message panel title:
```
╭─ Conversation · Turn 2 of 2 ─...╮
│ Wiring the loader now —          │
╰──────────────────────────────────╯
╭─ Message · responding… ──────────╮
│ ┃ Ask Claude...                   │
╰──────────────────────────────────╯
```

### File Integrity Summary

| File | Classification | In Relevant Files? | Requirement/Task Linkage |
|---|---|---|---|
| `internal/tui/panel.go` | Core (new) | Yes | Tasks 1.2, all Unit-1 FRs |
| `internal/tui/panel_test.go` | Supporting (new) | Yes | Task 1.3 |
| `internal/tui/styles.go` | Core | Yes | Task 1.1 |
| `internal/tui/selector.go` | Core | Yes | Task 1.4 |
| `internal/tui/selector_test.go` | Supporting | Yes | Task 1.5 |
| `internal/tui/detail.go` | Core | Yes | Task 1.6 |
| `internal/tui/detail_test.go` | Supporting (new) | Yes | Task 1.9 |
| `internal/tui/runs.go` | Core | Yes | Task 1.7 |
| `internal/tui/runs_test.go` | Supporting (new) | Yes | Task 1.10 |
| `internal/tui/monitor.go` | Core | Yes | Tasks 2.1–2.12 |
| `internal/tui/monitor_test.go` | Supporting | Yes | Task 2.11 |
| `internal/tui/chat.go` | Core | Yes | Tasks 3.1–3.6 |
| `internal/tui/chat_test.go` | Supporting (new) | Yes | Task 3.7 |
| `internal/tui/input.go` | Core | Yes | Task 3.3 (textarea border suppression) |
| `internal/tui/viewer.go` | Core | **No** | Task 3.5 (`handleResize` adopts `panelFrame()`); commit `597ee43` |

---

**Validation Completed:** 2026-08-04  
**Validation Performed By:** Claude Opus 4.8
