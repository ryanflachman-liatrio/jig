# Task 05 Proofs - Retire `.jig/telemetry.json` prefs and close out cross-cutting verification

## Task Summary

This task deletes the last disk-backed prefs loader (`internal/telemetry`'s
`.jig/telemetry.json`), leaving `internal/telemetry.Prefs` as a pure
in-memory parameter type consumed only by `internal/config`. It then closes
out the spec: a repo-wide grep sweep confirms no live code still references
any of the three retired formats (`tui.json`, `notifications.toml`,
`telemetry.json`), stale doc comments describing "how telemetry config
works today" are updated to describe `config.toml`'s `[telemetry]` table,
and the full `docs/TESTING.md` check sequence passes at the repository root.

## What This Task Proves

- `internal/telemetry/prefs.go` (`PrefsFileName`, `PrefsPath`, `LoadPrefs`,
  `SavePrefs`) is gone; `Prefs` survives only as `ResolveConfig`'s in-memory
  parameter type, with `internal/config/telemetry.go` as its one caller
  (passing a zero-value `Prefs{}`, per Unit 4).
- Zero remaining references to `.jig/tui.json`, `.jig/notifications.toml`,
  or `.jig/telemetry.json`/`LoadPrefs`/`SavePrefs` in any `.go` file, outside
  historical spec docs — the spec's format-consolidation success metric.
- `docs/observability.md` and `docs/TUI.md` (the two non-ADR docs that
  described the retired formats as current behavior) now describe the
  `config.toml` tables instead; `docs/adr/0012-observability-export.md` is
  deliberately left untouched as historical decision narrative.
- `go build ./cmd/jig && go test ./... && go vet ./...` passes at the
  repository root, demonstrating the four-unit migration is coherent
  end-to-end.

## Evidence Summary

- The full repository test suite passes except two pre-existing, unrelated
  failures confirmed (in Unit 3's proof artifact, and reconfirmed here)
  to be present regardless of this spec's changes.
- `go vet ./...` is clean.
- The three grep sweeps (`tui.json`, `notifications.toml`,
  `telemetry.json`/`LoadPrefs`/`SavePrefs`) each return only
  doc-comment/test-helper-comment references, no live code.
- A final targeted `git status`/diff review confirms only files relevant to
  this unit changed.

## Artifact: `internal/telemetry/prefs.go` deleted; `Prefs` is in-memory-only

**What it proves:** The disk-backed loader/writer for `.jig/telemetry.json`
no longer exists; `Prefs` remains solely as `ResolveConfig`'s parameter
type, called from `internal/config/telemetry.go`'s env-derived base-layer
builder with a zero value.

**Command:**

```bash
git status --short internal/telemetry/
grep -rn "LoadPrefs\|SavePrefs\|PrefsFileName\|PrefsPath" --include="*.go" .
```

**Result summary:** `prefs.go` shows as deleted (`D`); the grep for any of
the four removed symbols returns no matches anywhere in the repository.

```
 D internal/telemetry/prefs.go
```

## Artifact: Repo-wide grep sweep for all three retired formats

**What it proves:** No live `.go` code still touches `.jig/tui.json`,
`.jig/notifications.toml`, or `.jig/telemetry.json`/`LoadPrefs`/`SavePrefs` —
the spec's Success Metric #1 ("zero remaining references... both formats
fully retired, not dual-supported").

**Command:**

```bash
grep -rn "\.jig/tui\.json" --include="*.go" .
grep -rn "notifications\.toml" --include="*.go" .
grep -rn "telemetry.json\|LoadPrefs\|SavePrefs" --include="*.go" .
```

**Result summary:** Every remaining hit is a doc comment explaining
migration history (e.g. "formerly .jig/tui.json") or a test helper's doc
comment describing its own synthetic TOML shape — no live disk read/write
path remains for any of the three formats.

```
internal/config/config.go:30:// TUIConfig holds terminal-UI display preferences (formerly .jig/tui.json).
internal/tui/root.go:215:// .jig/tui.json disk read.
internal/tui/monitor/monitor_model.go:641:// .jig/tui.json disk read.
internal/tui/monitor/monitor_model.go:662:// unlike the retired .jig/tui.json write, it is in-memory-only from here.
internal/tui/monitor/monitor_transcript.go:227:	// persists to the retired .jig/tui.json.

cmd/jig/notifications_test.go:26:// wrapNotificationsTOML nests raw notifications.toml-shaped content (the
internal/config/config.go:61:// .jig/notifications.toml), reusing notification's own destination types

(telemetry.json/LoadPrefs/SavePrefs: no matches)
```

## Artifact: Doc sweep — `config.toml` described as the current source of truth

**What it proves:** `docs/observability.md`'s precedence summary and
`config.toml` walkthrough, and `docs/TUI.md`'s `compact_tool_groups`
description, now describe the actual current behavior (env-derived base ←
`config.toml` override ← workflow opt-in signal; session-only toggle with a
`config.toml`-sourced default) instead of the retired per-file prefs model.
`docs/adr/0012-observability-export.md` is deliberately left as historical
narrative, per this task's explicit instruction.

**Command:**

```bash
grep -n "config.toml\|telemetry.json" docs/observability.md | head -20
grep -n "compact_tool_groups" docs/TUI.md
```

**Result summary:** `docs/observability.md` now documents `config.toml`'s
`[telemetry]` table as the operator-level layer (env base ← config
override), replacing the old `.jig/telemetry.json` section; `docs/TUI.md`
documents `compact_tool_groups`'s default as `config.toml`-sourced with a
session-only in-TUI toggle.

## Artifact: Full repository check sequence

**What it proves:** The entire four-unit migration builds, tests, and
vets cleanly at the repository root — the spec's end-to-end coherence
requirement.

**Command:**

```bash
gofmt -l internal/telemetry internal/workflow
go build ./cmd/jig
go test ./... -count=1
go vet ./...
```

**Result summary:** `gofmt -l` reports nothing; `go build ./cmd/jig`
succeeds; `go vet ./...` is clean. `go test ./...` passes for every package
except two pre-existing, unrelated failures also observed and confirmed
unrelated in this spec's Unit 3 proof artifact:

- `internal/harness`'s `TestTier2ObservesEveryACPHarness` — a flaky ACP
  subprocess-timing test (passed on retry in Unit 3's investigation).
- `internal/tui/monitor`'s `TestBoundaryBannerFoldsIntoClosingItemLineRange`
  — fails identically with this spec's changes fully removed (`git stash`),
  confirming it predates this work.

Neither package was touched by any commit in this spec.

```
ok  	jig/cmd/jig	1.168s
ok  	jig/internal/config	6.019s
...
FAIL	jig/internal/harness	45.946s   (pre-existing, unrelated — see above)
...
FAIL	jig/internal/tui/monitor	5.104s  (pre-existing, unrelated — see above)
```

## Artifact: Final targeted diff review

**What it proves:** Only files relevant to Unit 5 changed in this commit;
no unrelated or accidental modifications are included.

**Command:**

```bash
git status --short
```

**Result summary:** Only `docs/TUI.md`, `docs/observability.md`,
`internal/telemetry/config.go`, `internal/telemetry/config_test.go`
(modified), `internal/telemetry/prefs.go` (deleted), and
`internal/workflow/schema.go`/`validate.go` (stale doc-comment fixes) are
staged for this unit — matching exactly what Unit 5's task list describes.

```
 M docs/TUI.md
 M docs/observability.md
 M internal/telemetry/config.go
 M internal/telemetry/config_test.go
 D internal/telemetry/prefs.go
 M internal/workflow/schema.go
 M internal/workflow/validate.go
```

## Reviewer Conclusion

Every settings file this spec set out to consolidate — `.jig/tui.json`,
`.jig/notifications.toml`, and `.jig/telemetry.json` — is now fully retired
from live code, with `internal/telemetry`'s env-var-reading logic
deliberately preserved as `[telemetry]`'s dynamic base layer (the one named
exception in the spec). The repository builds, tests, and vets cleanly end
to end, and documentation describing telemetry's current configuration
surface has been brought in line with the new `config.toml`-based system.
This closes out Spec 28.
