# Task 05 Proofs - SDK dependency removed; repo-wide grep-clean closeout

## Task Summary

This task closes out spec 26 by removing `github.com/severity1/claude-agent-sdk-go`
from `go.mod` now that Units 1 (`ClaudeHarness`), 2 (`MonitorAdapter`), and 4
(`helpchat`) no longer import it, and by updating the docs that described
per-backend transport choice so they now describe ACP as jig's only
transport.

## What This Task Proves

- Zero remaining `.go` references to `claudecode`/`claude-agent-sdk-go`
  anywhere in the tree.
- The dependency itself is gone from `go.mod`/`go.sum`, and `go mod tidy`
  confirms nothing transitively still needs it.
- `go build/vet/test` pass at the repo root and in the nested `harness/acp`
  module with the dependency removed.
- `docs/ARCHITECTURE.md`, `CONTEXT.md`, `AGENTS.md`, and `README.md` describe
  ACP as the only transport; `Transport` is gone as a vocabulary term.
- The two records that named this migration as future work (spec 12's
  Non-Goal 5, `open-goals.md`'s A6 entry) are marked resolved.

## Evidence Summary

- A repo-wide `.go` grep for `claudecode`/`claude-agent-sdk-go`, excluding
  this spec's own directory, returns nothing.
- `go.mod`/`go.sum` no longer list the dependency; `go mod tidy` produces no
  diff beyond the two-line removal itself.
- Root `go build ./... && go vet ./... && go test ./...` passes, with only
  the same pre-existing, already-documented `TestBoundaryBannerFoldsIntoClosingItemLineRange`
  failure in `internal/tui/monitor` (unrelated to this spec, reproduced
  identically on `main` before Unit 1's changes — see Unit 2/3's proof
  artifacts for the original observation).
- The nested `harness/acp` module's own `go build/vet/test` pass.

## Artifact: Repo-wide grep-clean for the SDK import

**What it proves:** No Go source anywhere in the tree still imports the
Claude Agent SDK or its `claudecode` alias.

**Why it matters:** This is the spec's core success metric — "one transport,
not three" — verified mechanically, not by inspection.

**Command:**

```bash
grep -rn "claudecode\|claude-agent-sdk-go" --include="*.go" . | grep -v docs/specs/26-spec-acp-only-harness
```

**Result summary:** Zero matches.

## Artifact: `go.mod`/`go.sum` dependency removal

**What it proves:** The dependency is gone, and `go mod tidy` confirms
nothing else in the module graph needs it.

**Why it matters:** A grep-clean source tree with a lingering `go.mod` entry
would leave dead weight in the build graph; this closes that gap too.

**Command:**

```bash
grep -n "severity1/claude-agent-sdk-go" go.mod go.sum   # before: 3 matches
go mod tidy
git diff --stat go.mod go.sum
```

**Result summary:** Before removal, the module and its two `go.sum` hash
lines were present. `go mod tidy` removed all three lines and produced no
further diff — nothing transitively depended on it.

```
go.mod | 1 -
go.sum | 2 --
2 files changed, 3 deletions(-)
```

## Artifact: Full build/vet/test pass, root + nested module

**What it proves:** Removing the dependency does not break compilation or
any test suite, at either module root.

**Why it matters:** This is the spec's final safety gate before closeout —
proof that the whole migration (Units 1-5) leaves the tree in a fully
working state.

**Command:**

```bash
gofmt -l .                                   # no output
go build ./... && go vet ./... && echo OK    # root module
go test ./...                                # root module
(cd harness/acp && go build ./... && go vet ./... && go test ./...)
```

**Result summary:** `gofmt -l` reported no unformatted files. Root build/vet
pass cleanly. Root test run passes every package except the one
already-documented pre-existing failure below. The nested `harness/acp`
module builds, vets, and tests cleanly.

```
--- FAIL: TestBoundaryBannerFoldsIntoClosingItemLineRange (0.00s)
    monitor_transcript_banner_view_test.go:105: turn B does not begin at row=6
FAIL
FAIL	jig/internal/tui/monitor	1.893s
```

This is the same failure already logged as pre-existing and unrelated in
this spec's Unit 2 (`26-task-2-proofs.md`) and Unit 3 proof artifacts,
reproduced identically here with no change in cause.

## Artifact: Doc updates dropping the transport-choice framing

**What it proves:** `CONTEXT.md` no longer defines `Transport` as a
vocabulary term, and `README.md` no longer implies a Claude SDK/ACP split.
`docs/ARCHITECTURE.md` and `AGENTS.md` were already updated to ACP-only
language in Unit 1 and needed no further change here (verified by grep,
not re-edited).

**Why it matters:** Docs that still described a transport choice per
backend would misrepresent the post-migration architecture to the next
reader.

**Diff (CONTEXT.md, "Harness and backend" section):**

```diff
-**Backend:** the vendor/CLI being driven: Claude, Cursor, or Codex today.
-**Transport:** the protocol used to reach it: SDK or ACP as supported.
-**Harness:** jig's Go implementation of the transport/lifecycle seam:
-`AcpHarness`, `CursorHarness`, or `CodexHarness`.
-Supported pairs and defaulting are in [AGENTS.md](AGENTS.md).
+**Backend:** the vendor/CLI being driven: Claude, Cursor, or Codex today.
+**Harness:** jig's Go implementation of the ACP lifecycle/capability seam:
+`AcpHarness`, `CursorHarness`, or `CodexHarness`. ACP is the only transport;
+there is no per-backend transport choice.
+Backend selection and defaulting are in [AGENTS.md](AGENTS.md).
```

**Diff (README.md, "Status" section):**

```diff
-  Claude SDK/ACP, Cursor ACP, and Codex ACP are supported.
+  Claude ACP, Cursor ACP, and Codex ACP are supported.
```

**Confirmation `docs/ARCHITECTURE.md`/`AGENTS.md` needed no change:**

```bash
grep -n "SDK\|Transport\|transport" AGENTS.md docs/ARCHITECTURE.md
```

Only unrelated matches remain: `AGENTS.md`'s reference to the `JIG_HARNESS`
env var name, `docs/ARCHITECTURE.md`'s `harness/acp` row describing
platform-specific *process* transport support (OS pipes, not agent
backends), and an OTel/Prometheus "exporter SDK" mention in
`internal/telemetry`'s row — none imply a per-backend transport choice.

## Artifact: Historical records marked resolved

**What it proves:** The two places that named this migration as deferred
future work now point at this spec as the record of resolution.

**Why it matters:** Leaving those non-goals/open-goal entries unresolved
would make the codebase's planning docs contradict its actual state.

**Diff (spec 12's Non-Goal 5):**

```diff
 5. **Migrating the Tier-2 `MonitorAdapter`** (`internal/runner/monitor.go`)
    or the dead `tui` chat off the SDK. Both stay on their direct-SDK path.
+   **Resolved:** `docs/specs/26-spec-acp-only-harness/` migrated
+   `MonitorAdapter` to ACP (Unit 2), deleted the dead `tui/chat` (Unit 1), and
+   removed the SDK dependency entirely (Unit 5); this non-goal is closed.
```

**Diff (`docs/plans/open-goals.md`'s A6 entry):**

```diff
-| A6 | ... | R | **Done.** ... isolated direct-SDK classifiers ... |
+| A6 | ... | R | **Done.** ... (classifier isolation language dropped from
+  the "done" summary) ... The classifiers were isolated direct-SDK callers
+  at the time this line was written; `docs/specs/26-spec-acp-only-harness/`
+  migrated `MonitorAdapter` to ACP, closing that gap too. |
```

Other repo-wide non-`.go` matches for `claude-agent-sdk-go` (found via a
broader, non-`.go`-scoped grep) are all historical/dated documents from
specs 07, 10, 11, 12, and `docs/plan-agent-pause-and-questions.md`/
`docs/help-agent-plan.md`/`docs/plans/a6-restore-tier2-security-monitors.md`
/`docs/specs/harness-abstraction/` — left untouched, matching Unit 1's
established precedent of not rewriting dated historical records.

## Reviewer Conclusion

The Claude Agent SDK is gone from every Go source file and from `go.mod`;
`go mod tidy` confirms no dependency remains. `go build/vet/test` pass
cleanly at both module roots, with only the pre-existing, already-explained
`TestBoundaryBannerFoldsIntoClosingItemLineRange` failure unrelated to this
spec. Live documentation (`CONTEXT.md`, `README.md`, and previously
`ARCHITECTURE.md`/`AGENTS.md` in Unit 1) now describes ACP as jig's only
transport, and the two planning records that flagged this migration as
future work are marked resolved. Spec 26's five units are complete.
