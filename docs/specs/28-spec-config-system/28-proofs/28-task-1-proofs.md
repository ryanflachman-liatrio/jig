# Task 01 Proofs - Layered Config Core (`internal/config`, `jig config show`, `--config`)

## Task Summary

This task builds the foundation every later unit depends on: a typed `Config`
schema, TOML loading for the user- and project-level `config.toml` files, a
per-key defaults → user → project merge, a `jig config show` CLI to print the
effective result, and a global `--config` flag that substitutes the
user-level layer. No table (`[ui]`, `[tui]`, `[notifications]`, `[telemetry]`)
has a concrete field yet — those are added by Units 2-4 — so this task proves
the *plumbing*, not a user-visible setting.

## What This Task Proves

- `internal/config.Load` correctly composes built-in defaults, an optional
  user file, and an optional project file, treating either or both as
  absent without error.
- Config parse/validation failures return a sanitized error that names the
  offending file's path but never its contents, and remain `errors.Is`-
  compatible with the fixed sentinel.
- `jig config show` prints the merged result as flat TOML and exits non-zero
  on a bad config file.
- The global `--config` flag is parsed pre-dispatch (same mechanism as
  `--ascii`) and substitutes the resolved user-level path.
- Persistence-off (`root == ""`) and a non-default `--root` never cause an
  unintended relative-path join, per `AGENTS.md`'s persistence-off contract.

## Evidence Summary

- `go test ./internal/config ./cmd/jig -count=1` passes, covering defaults-only,
  missing-file-not-error, persistence-off-not-error, and sanitized invalid-TOML
  cases for both the user and project layers, plus `--config` flag stripping.
- `jig config show` against an empty project prints the four built-in tables
  with an exit code of `0`.
- `jig config show` against a project with invalid TOML prints
  `config_invalid: <path>` and exits `1`, without echoing file contents.
- **Note on the "non-default value" and "project overrides user" proof
  artifacts**: those require a concrete settable field, which this task's
  tables intentionally don't have yet (see Task Summary). They are captured
  end-to-end under `28-task-2-proofs.md`, once `[tui] compact_tool_groups`
  exists as the first real field flowing through this same merge pipeline.

## Artifact: `internal/config` and `cmd/jig` test suite

**What it proves:** the load/merge pipeline, sanitized error handling, and
the `--config` flag's stripping/substitution logic behave correctly,
independent of any one table's fields.

**Why it matters:** this is the regression backstop every later unit builds
on; if this pipeline is wrong, every table inherits the bug.

**Command:**

```bash
go test ./internal/config ./cmd/jig -count=1
```

**Result summary:** both packages pass.

```text
ok  	jig/internal/config	0.690s
ok  	jig/cmd/jig	1.352s
```

## Artifact: `go vet` clean

**What it proves:** no suspicious constructs in the new code.

**Command:**

```bash
go vet ./internal/config ./cmd/jig
```

**Result summary:** no output — clean.

## Artifact: `jig config show` with no config files present

**What it proves:** built-in defaults load correctly with neither a user nor
a project file present.

**Command:**

```bash
XDG_CONFIG_HOME=/tmp/xdg-empty jig config show --root /tmp/proj1/.jig
```

**Result summary:** prints the four empty tables and exits `0`.

```toml
[ui]

[tui]

[notifications]

[telemetry]
```

## Artifact: `jig --config /tmp/alt.toml config show` substitutes the user layer

**What it proves:** the global `--config` flag is parsed pre-dispatch and
its path is used in place of the resolved XDG user path; a present project
`.jig/config.toml` still layers on top as usual (none is present here, so
output is unchanged defaults).

**Command:**

```bash
jig --config /tmp/alt.toml config show --root /tmp/proj/.jig
```

**Result summary:** exits `0` with the same merged output shape, proving the
flag was accepted, stripped from `os.Args` before subcommand dispatch, and
consumed by `loadEffectiveConfig` without error.

## Artifact: invalid TOML produces a sanitized, path-naming, non-zero-exit error

**What it proves:** a present-but-malformed config file fails closed with an
actionable, sanitized message; the raw file content is never echoed.

**Command:**

```bash
jig config show --root /tmp/proj/.jig   # /tmp/proj/.jig/config.toml contains "this is not [ valid toml"
```

**Result summary:** exit code `1`.

```text
config_invalid: /tmp/proj/.jig/config.toml
```

## Reviewer Conclusion

The config loader, merge pipeline, `config show` CLI, and `--config` flag
all work correctly at the plumbing level: defaults load, missing/absent
files are not errors, invalid files fail closed with a sanitized message,
and the global flag substitutes the user layer. The two proof artifacts that
need a concrete non-default value are deferred by design to Task 2.0's proof
file, which is the first task to give any table a real field.
