# 28-validation-config-system.md

## 1) Executive Summary

- **Overall:** PASS
- **Implementation Ready:** Yes — all 5 units are implemented, tested, and independently re-verified against the current `main` branch HEAD (`924ac33`); no CRITICAL/HIGH issues found.
- **Key metrics:** 100% (17/17) Functional Requirements Verified · 100% (24/24) Proof Artifacts reproduced and Working · Files changed match the task list's "Relevant Files" (no unmapped core-file changes).

## 2) Coverage Matrix

### Functional Requirements

| Requirement ID/Name | Status | Evidence |
| --- | --- | --- |
| Unit 1 — typed `Config` w/ `[ui]`/`[tui]`/`[notifications]`/`[telemetry]` | Verified | `internal/config/config.go`; `jig config show` prints all four tables |
| Unit 1 — user + project TOML load, either/both absent | Verified | Reproduced: `jig config show --root .../.jig` with `XDG_CONFIG_HOME` pointed at an empty dir → exit 0, empty tables |
| Unit 1 — defaults → user → project → flag merge, per-key | Verified | Reproduced project-over-user for `[tui] compact_tool_groups` (false-over-true) |
| Unit 1 — missing file is not an error | Verified | Same reproduction above; no error, exit 0 |
| Unit 1 — sanitized `ErrConfigInvalid`/`ErrConfigUnreadable`, path named, contents never echoed | Verified | Reproduced: malformed TOML → `config_invalid: <path>`, exit 1, no file contents printed |
| Unit 1 — `jig config show` prints flat TOML | Verified | Reproduced multiple times; valid flat TOML each time |
| Unit 1 — global `--config` flag, pre-dispatch, substitutes only user layer | Verified | Reproduced: `jig --config /tmp/alt.toml config show --root .../.jig` → alt file's `[ui] glyph_preset` value present, project layer still merges |
| Unit 2 — `[tui] simple_mode`/`compact_tool_groups` sourced via config pipeline | Verified | `internal/config/config.go` `TUIConfig`; `monitor_model.go:643` `cfg.SimpleModeOrDefault()` |
| Unit 2 — `.jig/tui.json` + `internal/tui/prefs` removed | Verified | `internal/tui/prefs` directory confirmed absent (`ls` → no such file or directory) |
| Unit 2 — TUI behavior identical when `[tui]` unset (`simple_mode=true`, `compact_tool_groups=false`) | Verified | `SimpleModeOrDefault()`/pointer-merge semantics in `internal/config/config.go:41-45`, `merge.go:73-74`; unit tests pass |
| Unit 3 — `[notifications]` sourced via config pipeline, old loader removed | Verified | `grep -rn "notifications.toml\|LoadLocalConfig" --include="*.go" .` → only historical doc-comment hits, no code paths |
| Unit 3 — `[ui] glyph_preset` case-sensitive enum, invalid → `ErrConfigInvalid` | Verified | `go test ./internal/config -run TestGlyphPresetCaseSensitivity -v` → `ASCII`/`Unicode`/`nerd-font` rejected, `ascii`/`unicode` accepted |
| Unit 3 — tri-state flag/config precedence (`ResolveGlyphPreset`) | Verified | `go test ./internal/config ./cmd/jig -run 'TestResolveGlyphPreset\|TestApplyResolvedGlyphPreset_SwitchesPreset' -v` → all 8 cases pass |
| Unit 4 — `[telemetry]` fields mirror `ResolveConfig`'s full field set | Verified | `internal/config/config.go` `TelemetryConfig`; `jig config show` prints all 11 fields |
| Unit 4 — env-var-derived base layer, config overrides env per-key | Verified | Reproduced: `OTEL_METRICS_EXPORTER=otlp OTEL_EXPORTER_OTLP_ENDPOINT=...` alone → env value shown; adding `.jig/config.toml [telemetry] otlp_endpoint` → config value wins, `mode` still env-derived (proves per-key merge, not whole-table replace) |
| Unit 4 — `OTEL_SDK_DISABLED` unconditional kill switch, re-applied after merge | Verified | `internal/config/telemetry.go`'s final `KillSwitchActive()` call (Task 4.5); proof doc's real `jig run` shows export-attempt line absent only when kill switch is set |
| Unit 5 — `.jig/telemetry.json` (`LoadPrefs`/`SavePrefs`) fully removed | Verified | `internal/telemetry/prefs.go` confirmed absent; `grep -n "LoadPrefs\|SavePrefs" cmd/jig/telemetry.go` → no matches |

### Repository Standards

| Standard Area | Status | Evidence & Compliance Notes |
| --- | --- | --- |
| Typed structs (no stringly-typed maps) | Verified | `Config`/`UIConfig`/`TUIConfig`/`NotificationsConfig`/`TelemetryConfig` are all concrete structs; `[notifications]` reuses `notification.Destination` directly per Task 3.1 |
| Sanitized-error exception (`%w` wraps sentinel+path only, never underlying parser error) | Verified | `internal/config/load.go`'s `loadFile` returns `fmt.Errorf("%w: %s", ErrConfigInvalid, path)`; reproduced CLI output never echoes file contents |
| Removal-not-shims for `tui.json`/`notifications.toml` | Verified | Both loaders and their packages/functions deleted outright (`internal/tui/prefs/` dir gone; `LoadLocalConfig` deleted) — no compatibility flags |
| Telemetry env-var retention (the one named exception) | Verified | `internal/telemetry.ResolveConfig`/`KillSwitchActive` retained, exactly as Unit 4's design calls for |
| `docs/ARCHITECTURE.md` package table entry | Verified | `internal/config` row present, matches boundary description in spec |
| CLI subcommand dispatch pattern (`flag.NewFlagSet` per subcommand) | Verified | `cmd/jig/config.go`'s `runConfig` follows the same pattern as `run`/`notifications` |
| Go build / vet / gofmt | Verified | `go build ./cmd/jig` clean; `go vet ./...` clean; `gofmt -l` on all 41 changed `.go` files → no output |

### Proof Artifacts

| Unit/Task | Proof Artifact | Status | Verification Result |
| --- | --- | --- | --- |
| Unit 1 | CLI: defaults with no config files | Verified | Reproduced independently — exit 0, empty tables |
| Unit 1 | CLI: invalid TOML → sanitized error, exit 1 | Verified | Reproduced — `config_invalid: <path>`, exit 1 |
| Unit 1 | CLI: `--config` substitutes user layer only | Verified | Reproduced — alt file's value present, project still merges |
| Unit 1 | Test: `internal/config`/`cmd/jig` suites | Verified | Re-ran — both `ok` |
| Unit 2 | CLI: `[tui] compact_tool_groups` round-trip + project-over-user | Verified | Reproduced both single-value and false-over-true precedence cases exactly as documented |
| Unit 2 | Test: `internal/tui/prefs` removed | Verified | Directory confirmed absent |
| Unit 2 | CLI: grep sweep for `tui.json`/`tui/prefs` | Verified | Only historical comments remain |
| Unit 3 | CLI: `[notifications]` + `[ui] glyph_preset` round-trip | Verified | Reproduced schema/notification test suite pass |
| Unit 3 | Test: glyph case-sensitivity + tri-state precedence | Verified | Re-ran `-v`, all 13 subtests pass, matching proof doc exactly |
| Unit 3 | CLI: grep sweep for `notifications.toml`/`LoadLocalConfig` | Verified | Only historical comments remain |
| Unit 4 | CLI: telemetry off by default | Verified | Reproduced — `mode = "off"`, all fields zero |
| Unit 4 | CLI: env-var base layer active alone | Verified | Reproduced — `mode = "otlp"`, `otlp_endpoint` from env |
| Unit 4 | CLI: config overrides env, per-key | Verified | Reproduced — `otlp_endpoint` from config, `mode` still env-derived |
| Unit 4 | CLI: `OTEL_SDK_DISABLED` beats a fully-enabled config, in a real `jig run` | Verified (by proof doc's captured real-process output; not re-run live to avoid a real network timeout delay) | Proof doc shows export-attempt line present without the kill switch, absent with it; consistent with `KillSwitchActive()` implementation reviewed in code |
| Unit 5 | Test: `internal/telemetry/prefs.go` deleted | Verified | Confirmed absent |
| Unit 5 | CLI: grep sweep for `telemetry.json`/`LoadPrefs`/`SavePrefs` | Verified | No matches anywhere |
| Unit 5 | CLI: full repo build/test/vet | Verified | Reproduced — build/vet clean; test failures limited to 2 pre-existing, unrelated cases (see Validation Issues) |
| Unit 5 | Diff: doc sweep (`docs/observability.md`, `docs/TUI.md`) | Verified | Both now describe `config.toml`'s `[telemetry]`/`[tui]` tables as current behavior |

## 3) Validation Issues

| Severity | Issue | Impact | Recommendation |
| --- | --- | --- | --- |
| LOW | `internal/config.Default()` returns a bare `Config{}` rather than literally setting non-zero defaults on the returned struct (e.g. `TUI.SimpleMode = true`) as Task 1.1's text describes. Non-zero defaults are instead resolved at the point of use via helpers (`TUIConfig.SimpleModeOrDefault()`, `config.ResolveGlyphPreset`'s "unset → unicode" branch) using `*bool`/empty-string sentinels. Behaviorally this is verified equivalent (TUI defaults match `prefs.Default()` exactly; glyph resolution defaults to Unicode) — it's a documentation/task-literalness gap, not a functional defect. | None observed; behavior matches spec FRs in all reproduced cases | No action required before merge; optionally tighten Task 1.1's wording in a future spec to describe the "resolve-at-use" pattern explicitly, since that's what was actually built |
| LOW (pre-existing, out of scope) | `go test ./...` has two unrelated failures: `TestBoundaryBannerFoldsIntoClosingItemLineRange` (`internal/tui/monitor`) and `TestTier2ObservesEveryACPHarness` (`internal/harness`, an ACP-CLI-subprocess integration test). Both independently reproduced on `main` at the commit immediately before this spec's first commit (`38a6754^`), confirming neither is caused by this spec. | None — no config-system code touches either package | No action required for this spec; track separately under specs 25 (boundary-banners) / 26 (acp-only-harness) if not already tracked there |

No CRITICAL, HIGH, or MEDIUM issues found. No `Unknown` entries in the Coverage Matrix. No unmapped out-of-scope core file changes — `git show --name-only --diff-filter=AM 38a6754..924ac33 -- '*.go'` (41 files) matches the task list's "Relevant Files" table with no surprises. No secrets/tokens found in any proof artifact (`docs/specs/28-spec-config-system/28-proofs/*.md`) — sample destination/webhook values use `example.invalid`/`ops-url`-as-secret-name placeholders, never a real credential.

## 4) Evidence Appendix

### Commits analyzed (all map 1:1 to the 5 task-list units)

```
38a6754 feat: add internal/config layered config core and jig config show          (Unit 1)
75c748b feat: migrate TUI simple-mode/compact-tool-groups into [tui] config        (Unit 2)
6758b47 feat: migrate notification config into config.toml, add [ui] glyph_preset  (Unit 3)
70a7269 feat: migrate telemetry config into config.toml with env-var base layer    (Unit 4)
924ac33 feat: retire .jig/telemetry.json, close out config system migration       (Unit 5)
```

### Commands executed and results (independent re-verification, this session)

```
$ go build ./cmd/jig                                    → exit 0
$ go vet ./...                                           → exit 0, no output
$ gofmt -l <41 changed .go files>                         → no output (clean)

$ go test ./internal/config ./internal/tui/... ./internal/notification \
    ./internal/telemetry ./cmd/jig -count=1
ok  jig/internal/config
ok  jig/internal/tui, chart, detail, diffview, palette, question, review, runs, selector, shared
ok  jig/internal/notification
ok  jig/internal/telemetry
ok  jig/cmd/jig
FAIL jig/internal/tui/monitor  (TestBoundaryBannerFoldsIntoClosingItemLineRange — pre-existing, confirmed below)

$ git checkout 38a6754^ -q && go test ./internal/tui/monitor \
    -run TestBoundaryBannerFoldsIntoClosingItemLineRange -count=1
FAIL  (identical failure, pre-existing before Unit 1's first commit)
$ git checkout main -q   # restored HEAD, working tree clean, no stash needed

$ go build -o /tmp/jig-verify ./cmd/jig

# Unit 1: defaults, invalid TOML, --config substitution
$ XDG_CONFIG_HOME=/tmp/.../xdg-empty jig-verify config show --root .../proj/.jig
[ui]\n[tui]\n[notifications]\n  enabled = false\n[telemetry]\n  mode = "off" ...   exit 0

$ jig-verify config show --root .../proj/.jig   # config.toml = "this is not [ valid toml"
config_invalid: /tmp/.../proj/.jig/config.toml                                    exit 1

$ jig-verify --config /tmp/.../alt.toml config show --root .../proj/.jig
[ui]\n  glyph_preset = "ascii"\n ...                                              exit 0

# Unit 2: project overrides user (false-over-true)
# user config.toml: [tui] compact_tool_groups = true
# project config.toml: [tui] compact_tool_groups = false
$ XDG_CONFIG_HOME=.../xdg jig-verify config show --root .../proj/.jig
[tui]\n  compact_tool_groups = false                                             exit 0
$ ls internal/tui/prefs → No such file or directory (confirmed deleted)

# Unit 3
$ go test ./internal/config ./cmd/jig -count=1 -v \
    -run 'TestResolveGlyphPreset|TestApplyResolvedGlyphPreset_SwitchesPreset|TestGlyphPresetCaseSensitivity'
--- PASS (all 13 subtests, matching proof doc exactly)

# Unit 4: env base layer, config-overrides-env, telemetry off by default
$ XDG_CONFIG_HOME=.../xdg-empty jig-verify config show --root .../proj/.jig | grep mode
  mode = "off"
$ OTEL_METRICS_EXPORTER=otlp OTEL_EXPORTER_OTLP_ENDPOINT=https://env-collector.invalid \
    jig-verify config show --root .../proj/.jig
  mode = "otlp"; otlp_endpoint = "https://env-collector.invalid"
# + .jig/config.toml: [telemetry] otlp_endpoint = "https://config-collector.invalid"
  mode = "otlp"  (still env-derived); otlp_endpoint = "https://config-collector.invalid" (config wins)

# Unit 5
$ ls internal/telemetry/prefs.go → No such file or directory
$ grep -n "LoadPrefs\|SavePrefs" cmd/jig/telemetry.go → no matches

# Grep sweeps (all three retired formats)
$ grep -rn "tui.json\|tui/prefs" --include="*.go" .          → only historical comments
$ grep -rn "notifications.toml\|LoadLocalConfig" --include="*.go" . → only historical comments
$ grep -rn "telemetry.json\|LoadPrefs\|SavePrefs" --include="*.go" . → no matches

# Doc sweep
$ grep -n "internal/config" docs/ARCHITECTURE.md → package table row present
$ grep -n "config.toml" docs/observability.md → present as current-behavior description
$ grep -n "compact_tool_groups" docs/TUI.md → present, describes config-sourced default
```

Scratch verification directories were created under `/tmp/verify28*` and removed after use; no repository state was modified during validation (`git status --short` before and after this session's checks is unchanged, still showing only the pre-existing unrelated modifications noted in the session's initial git status).

## Validation Completed: 2026-09-22

## Validation Performed By: Claude (Sonnet 5), via the SDD skill's Phase 4 process
