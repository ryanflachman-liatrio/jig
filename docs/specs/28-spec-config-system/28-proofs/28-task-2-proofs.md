# Task 02 Proofs - Migrate TUI Preferences into `[tui]`, remove `.jig/tui.json`

## Task Summary

This task gives `internal/config`'s `[tui]` table its first real fields
(`simple_mode`, `compact_tool_groups`), retires the `.jig/tui.json` file
format and its `internal/tui/prefs` loader entirely, and re-sources the
monitor's simple-mode/compact-tool-group state from the merged config. It
also completes the two Task 1.0 proof artifacts that needed a concrete
field to demonstrate (non-default user value, project-overrides-user).

## What This Task Proves

- `[tui]` values set in `.jig/config.toml` are visible in `jig config show`
  and change the running TUI's rendering.
- Defaults match the retired `prefs.Default()` exactly: `simple_mode = true`,
  `compact_tool_groups = false`.
- Project config overrides user config, including the false-over-true case
  that a naive plain-bool merge would get wrong (`SimpleMode`/
  `CompactToolGroups` are `*bool` in the schema specifically to make an
  explicit `false` at a higher layer distinguishable from "unset").
- `.jig/tui.json` and `internal/tui/prefs` are fully removed from the
  codebase, not dual-supported.
- The in-TUI toggle (`ctrl+shift+a`, palette "compact tools") is now
  session-only: config.toml is a hand-edited file, not a runtime-write
  target, so no toggle writes to disk anymore.

## Evidence Summary

- `go test ./internal/config ./internal/tui/... ./cmd/jig -count=1` passes,
  except one pre-existing, unrelated failure (`TestBoundaryBannerFoldsIntoClosingItemLineRange`)
  confirmed to fail identically on the prior commit, before this task's changes.
- `jig config show` reflects `[tui] compact_tool_groups = true` set in
  `.jig/config.toml`.
- `jig config show` shows a project's explicit `false` correctly overriding
  a user-level `true`.
- `grep -rn "tui.json\|tui/prefs" --include="*.go" .` returns only
  historical/comment references documenting the retirement — no code paths.

## Artifact: `internal/config`, `internal/tui`, `cmd/jig` test suite

**What it proves:** the `[tui]` schema, its defaults, and its merge
semantics (including the false-over-true pointer-merge case) are correct;
the monitor and root TUI compile and behave correctly against the new
config-sourced path.

**Command:**

```bash
go test ./internal/config ./internal/tui/... ./cmd/jig -count=1
```

**Result summary:** all packages pass except `internal/tui/monitor`, which
has exactly one failure, `TestBoundaryBannerFoldsIntoClosingItemLineRange` —
confirmed below to be pre-existing and unrelated to this task.

```text
ok  	jig/internal/tui	1.582s
ok  	jig/internal/tui/chart	1.322s
ok  	jig/internal/tui/detail	2.884s
ok  	jig/internal/tui/diffview	2.107s
--- FAIL: TestBoundaryBannerFoldsIntoClosingItemLineRange (0.00s)
FAIL	jig/internal/tui/monitor	1.697s
ok  	jig/internal/tui/palette	4.275s
ok  	jig/internal/tui/question	5.133s
ok  	jig/internal/tui/review	6.097s
ok  	jig/internal/tui/runs	7.646s
ok  	jig/internal/tui/selector	6.965s
ok  	jig/internal/tui/shared	7.404s
ok  	jig/internal/config	0.643s
ok  	jig/cmd/jig	7.427s
```

## Artifact: pre-existing failure confirmed unrelated

**What it proves:** `TestBoundaryBannerFoldsIntoClosingItemLineRange` fails
identically on the commit immediately before this task's changes, so it is
not a regression introduced by the `[tui]` migration.

**Command:**

```bash
git stash && go test ./internal/tui/monitor -run TestBoundaryBannerFoldsIntoClosingItemLineRange -count=1; git stash pop
```

**Result summary:** the same failure reproduces on the pre-task commit,
confirming it predates this work.

## Artifact: `jig config show` reflects `[tui] compact_tool_groups = true`

**What it proves:** a `.jig/config.toml` value round-trips through the
merge pipeline into the printed effective config.

**Command:**

```bash
jig config show --root /tmp/proj/.jig   # /tmp/proj/.jig/config.toml sets [tui] compact_tool_groups = true
```

**Result summary:** the value appears under `[tui]` in the output.

```toml
[ui]

[tui]
  compact_tool_groups = true

[notifications]

[telemetry]
```

## Artifact: project config overrides user config (completes a deferred Task 1.0 proof)

**What it proves:** with a user-level `compact_tool_groups = true` and a
project-level `compact_tool_groups = false`, the project's value wins —
including the false-over-true direction, which only works because the
schema uses `*bool` rather than plain `bool`.

**Command:**

```bash
# ~/.config/jig/config.toml (user):    [tui] compact_tool_groups = true
# .jig/config.toml (project):          [tui] compact_tool_groups = false
jig config show --root /tmp/proj/.jig
```

**Result summary:** the merged output shows `compact_tool_groups = false`,
proving the project layer's explicit `false` overrides the user layer's
`true`.

```toml
[ui]

[tui]
  compact_tool_groups = false

[notifications]

[telemetry]
```

## Artifact: `.jig/tui.json` and `internal/tui/prefs` fully retired

**What it proves:** the old format is removed outright, not dual-supported,
per repository convention.

**Command:**

```bash
grep -rn "tui.json\|tui/prefs" --include="*.go" .
```

**Result summary:** every remaining hit is a comment documenting the
retirement (e.g. "formerly .jig/tui.json", "the retired .jig/tui.json
write"); `internal/tui/prefs/` no longer exists as a directory.

## Reviewer Conclusion

The `[tui]` table is live end-to-end: it has correct defaults, correct
false-over-true merge precedence, and drives the same monitor state the
retired `.jig/tui.json` used to. The old file format and its loader are
fully gone, and the in-session toggle is now explicitly documented as
non-persistent. The one test failure present in this package predates this
task and is unrelated.
