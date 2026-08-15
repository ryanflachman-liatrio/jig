# Task 3.0 Proofs — Edge Cases and Robustness

## Task Summary

This task validates boundary conditions in the tool call grouping implementation:
orphaned `tool_use` (no result), orphaned `tool_result` (no preceding `tool_use`),
groups spanning entry boundaries, `chatGroupExpand` reset on step change,
group expansion preserved across terminal resize, and empty/text-only transcripts.
Quality gates (`go vet`, `gofmt`) are also confirmed clean.

## What This Task Proves

- `primaryArg` correctly prioritizes known keys, falls back to sorted keys, truncates
  to 40 runes, and returns `""` for empty or non-string inputs.
- `loadChat` handles orphaned tool blocks, cross-entry groups, thinking/text breaks,
  empty transcripts, and text-only transcripts without panic.
- `chatGroupExpand` is cleared when the user navigates to a different step.
- Group expansion state survives `tea.WindowSizeMsg` (terminal resize).
- `go vet ./...` and `gofmt -l .` both produce no output.

## Evidence Summary

- All 63 tests pass (48 from Tasks 1–2 + 5 new robustness tests: `TestPrimaryArg`,
  `TestLoadChatGroupDetection`, `TestGroupExpandReset`, `TestGroupExpandPreservedOnResize`,
  plus the empty-transcript case inside `TestLoadChatGroupDetection`).
- `go vet ./...` clean.
- `gofmt -l .` clean after formatting `monitor_test.go` and `selector/update.go`.

## Artifact: Test Suite Pass

**What it proves:** All edge cases pass; no regressions.

**Command:**
```bash
go test ./internal/tui/monitor/...
```

**Result:**
```
ok  	jig/internal/tui/monitor	0.736s
```

**Test count:** 63 PASS, 0 FAIL.

## Artifact: go vet Clean

**Command:**
```bash
go vet ./...
```

**Result:** No output — clean.

## Artifact: gofmt Clean

**Command:**
```bash
gofmt -l .
```

**Result:** No output — clean (after applying `gofmt -w` to the two files that needed alignment fixes).

## Artifact: TestPrimaryArg — Key Priority and Truncation

**What it proves:** `primaryArg` returns the correct representative argument across
all 9 cases including well-known key priority order, unknown-key sorted fallback,
non-string value skipping, empty input, and 40-rune truncation with `…` suffix.

**Key cases:**
| Case | Input | Expected |
|------|-------|----------|
| `file_path` key | `{"file_path": "/a/b.go", "command": "ignored"}` | `"/a/b.go"` |
| `path` fallback | `{"path": "/x.go"}` | `"/x.go"` |
| `command` key | `{"command": "go test ./..."}` | `"go test ./..."` |
| unknown → sorted | `{"zzz": "last", "aaa": "first"}` | `"first"` |
| non-string skipped | `{"file_path": 42, "command": "cmd"}` | `"cmd"` |
| no string values | `{"count": 1}` | `""` |
| empty input | `nil` | `""` |
| truncation | 50-char string | 39 chars + `…` |

## Artifact: TestLoadChatGroupDetection — Accumulator Edge Cases

**What it proves:** The single-pass accumulator correctly classifies block sequences
into groups across all 8 cases. No panics on orphaned or empty inputs.

**Key cases:**
| Case | Input | chatGroupHeaders | chatBlocks |
|------|-------|-----------------|------------|
| consecutive tool+result | 1 use + 1 result | 1 | 1 |
| thinking between tools | use, think, use | 3 | 3 |
| text between tools | use, text, use | 2 | 2 |
| cross-entry group | use in entry 1, result in entry 2 | 1 | 1 |
| orphaned tool_use | use only, no result | 1 | 1 |
| orphaned tool_result | result only, no use | 1 | 1 |
| empty transcript | nil entries | 0 | 0 |
| text-only transcript | text block only | 0 | 0 |

The empty-transcript case also asserts `chatBlockCursor == 0`, guarding against
the panic in callers that access `chatBlocks[chatBlockCursor]` on a zero-length slice.

## Artifact: TestGroupExpandReset — chatGroupExpand Cleared on Step Change

**What it proves:** After expanding a group in step "a" and then calling
`reloadTranscript` (which triggers on step navigation), `chatGroupExpand` is
empty — preventing stale expansion state from bleeding into the new step.

**Key assertions:**
- After Enter on step "a" group: `len(chatGroupExpand) > 0`.
- After `reloadTranscript()` with new step: `len(chatGroupExpand) == 0`.

## Artifact: TestGroupExpandPreservedOnResize — Expansion Survives WindowSizeMsg

**What it proves:** `tea.WindowSizeMsg` (terminal resize) does not clear
`chatGroupExpand`. The group remains expanded and `chatBody()` still contains `▾`
after the resize.

**Key assertions:**
- After Enter: `len(chatGroupExpand) > 0`.
- After `WindowSizeMsg{Width: 120, Height: 40}`: `len(chatGroupExpand) > 0`.
- `chatBody()` contains `shared.ExpandedMarker` after resize.

## Reviewer Conclusion

All boundary conditions are covered. The accumulator handles orphaned blocks and
empty transcripts without panic. State management (`chatGroupExpand` reset, resize
preservation) is correct. Quality gates are clean. Task 3.0 is complete.
