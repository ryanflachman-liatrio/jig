# Testing

Test the observable contract at the smallest useful seam. Default tests should
run locally without authenticated agents or real external notification delivery.
Use synthetic data and temporary repositories/directories. See
[Architecture](ARCHITECTURE.md) for module boundaries and
[Graph engineering](GRAPH_ENGINEERING.md) for lifecycle invariants.

## Commands

Run from the repository root unless a subshell changes directory:

```bash
go version                           # must satisfy both go.mod files
go build ./cmd/jig
go test ./...
go vet ./...
(cd harness/acp && go test ./... && go vet ./...)
```

Root `./...` does not include tests in the nested ACP module or the Rust crate.
The root module tests do include `internal/harness`. For Go source changes,
format the changed files with `gofmt -w <files>`; use `gofmt -l <files>` to check
without rewriting unrelated files. Use `git diff --check` to catch whitespace
errors in any change.

Focused examples:

```bash
go test ./internal/workflow -run TestDecodeInvalid -v
go test ./internal/engine ./internal/runner -count=1
go test -race ./internal/engine ./internal/runner ./internal/harness
go test -race ./internal/tui/... ./internal/helpchat
go test -race ./internal/runexport ./cmd/jig -count=1
(cd harness/acp && go test -race ./...)
go test ./internal/workflow -coverprofile=/tmp/jig-workflow-coverage.out
go tool cover -func=/tmp/jig-workflow-coverage.out
```

Use `-count=1` when uncached execution matters. Race detection finds races only
in executed paths; a clean run is not proof of freedom from races.
[Go race detector](https://go.dev/doc/articles/race_detector)

Validate runnable examples and project workflow roots, not module/profile TOMLs:

```bash
go build -o /tmp/jig-doc-check ./cmd/jig
for workflow in examples/*.toml .agents/jig/*.toml; do
  /tmp/jig-doc-check validate "$workflow" || exit 1
done
```

For a local command-only end-to-end smoke, use a temporary project directory:
`jig run <absolute-path-to-examples/headless-smoke.toml> --ci` with the built
binary. Check [headless behavior](headless.md) and the fixture before execution.
Validation does not run workflow agents, checks, or notification senders.

## Select checks by change

| Change | Required evidence |
|---|---|
| Guidance/docs only | Review code claims and relative links, `git diff --check`; validate changed runnable examples. No new tests for prose. |
| Go implementation | Focused behavioral tests, root build/tests/vet; nested module checks if affected. |
| Schema/defaults/modules/conditions | Valid and invalid decode cases, precedence, filesystem-backed Load cases as needed; root workflow/example validation. |
| Scheduler/runner/concurrency | Relevant lifecycle tests and targeted `-race`; use real temp Git repositories for integration/reset behavior. |
| ACP lifecycle/protocol | Root harness tests plus nested ACP tests/vet; race and helper-process shutdown tests for lifecycle changes. |
| TUI behavior | Model/update/render assertions, focus/text capture/resize cases; targeted race tests for async changes and a terminal smoke for visual behavior when feasible. |
| Persistence/reopen/reset | Journal/snapshot/lease failure cases, interrupted-write recovery, persistence-off coverage; cross-process tests where process boundaries matter. |
| Export/lease/shared budgets | `go test -race ./internal/runexport ./cmd/jig -count=1`, disclosure scans, damaged input and bounds cases. |
| Notification/telemetry | Fake or local test receivers, sanitization, bounded queues and shutdown; verify delivery/exporter failure cannot alter run outcomes. |
| Rust crate | Run its local fmt/clippy/tests; Go checks cannot establish Rust parity. |

For SDD quality-check workflows, the declared
[quality profiles](quality-profiles.md) add their own applicability and evidence
contracts, including the coverage threshold. They do not replace behavioral
tests or automatically apply to a prose-only task. No repository CI workflow is
currently checked in under `.github/workflows`; do not report an assumed CI job
as validation.

## Test design

Use table-driven subtests for repeated input/output shapes. Give failures
behavioral names and useful diagnostics. Use `t.Helper`, `t.TempDir`, and
`t.Cleanup` for fixture setup/lifetime. Prefer direct assertions on state,
returned errors, parsed artifacts, and external effects. Do not recreate the
implementation algorithm in the assertion or lock in incidental call order.

Use `t.Parallel` only for isolated tests. Environment, current directory, global
theme, shared registries, and fixed ports require special care; `t.Setenv` and
`t.Chdir` are incompatible with parallel tests/parallel ancestors. Keep test
failures on the test goroutine by returning worker errors through channels.

Coordinate concurrency with channels, barriers, or explicit completion signals;
avoid sleeps as proof that an action occurred. Use deadlines to bound hangs.
Go 1.25's `testing/synctest` is useful for suitable in-process timer/goroutine
logic; real I/O and subprocesses still need explicit synchronization. It is not
a substitute for the race detector or process-level tests.
[Testing time and asynchronicity](https://go.dev/blog/testing-time)

Add fuzz targets when a parser/decoder/path boundary has meaningful properties:
no panic, bounded handling, valid round trips, or preservation of line identity.
Seed ordinary and hostile cases and keep minimized regressions. Run a specific
fuzz target in its package with `go test -fuzz=<target> -fuzztime=30s`; do not
imply a target already exists. Keep fuzz cases deterministic and independent.
[Go fuzzing](https://go.dev/doc/security/fuzz/)

Use benchmarks for measured performance work with representative graph,
transcript, or document sizes. Track allocations when relevant. Coverage points
to missing cases; a percentage alone says nothing about assertion quality.

## Workflow and engine cases

`workflow.Decode(data, "")` skips filesystem existence checks for structural
cases. It is not a replacement for `Load`: modules and real authoring assets
need temporary filesystem fixtures and a base directory. Add cases for each
new rejection and each supported precedence path. Compare typed errors where
available; aggregated validator text can use distinctive substrings.

Use fake executors for dependency ordering, condition skipping, bounded routes,
retry classification/caps, budgets, gates, and fan-out aggregation. Vary worker
completion order. Check dispatch counts and that dependents never run early.
Cover empty fan-out, failed children, resource saturation, stop/cancel, and
stale messages when the change touches those paths.

Persistence tests should exercise both empty-root behavior and real temporary
storage. Reopen/reset tests verify durable counters, sessions, input/review
snapshots, changed checkout contents, and survivor commits. Hold real advisory
locks in a helper process to test lease exclusion; a second goroutine is not
equivalent. Use the existing engine and harness crash fixtures.

## TUI and review cases

Drive `tea.Msg` through `Update` and inspect the returned model, commands, and
`View` (`tea.View.Content` for full program models). Run returned commands
selectively when they are the behavior under test; never start a real backend
just to test a key binding. Verify stale async results and text-capture routing
as well as happy-path navigation.

For layout, assert terminal-cell widths/heights with ANSI-aware helpers and
include Unicode, empty content, small dimensions, and narrow/wide layouts.
Use existing `internal/tui/chart/testdata` goldens for stable graph projections;
review every golden change instead of blindly accepting regenerated output.
Review tests must prove that preview/folding/panning does not move source-line
anchors or alter immutable text. Manual screenshots supplement these checks.

## Export and external integrations

Export fixtures must be fabricated. Open the real ZIP and decode its JSON/JSONL
members; scan every member and raw ZIP bytes (including headers/comments) for
seeded private values. Cover bounds, damaged storage, missing artifacts,
sanitization, and separate-process leases. Never attach raw real run archives
or seeded credential-shaped fixture values to proof documents.

Authenticated probes are opt-in: `JIG_CODEX_ACP_INTEGRATION=1` enables nested
Codex probes, and `JIG_ACP_QUESTION_INTEGRATION` enables the live question probe
in `internal/harness`. Inspect their source and prerequisites before enabling;
they may use accounts, network, and model budget. These flags control tests,
not workflow backend selection. Most files named `integration_test.go` use
local fixtures; inspect the actual guard rather than excluding by filename.

## Report evidence accurately

Record the commands run and their outcomes. Distinguish pass, assertion
failure, setup/toolchain failure, and intentional skip. If blocked, name the
missing prerequisite and run unaffected checks. Do not weaken assertions or
change unrelated production code to make a documentation task look green.
After applicable checks pass, review the final diff; broaden testing only for
new changes, failures, or an unresolved risk.
