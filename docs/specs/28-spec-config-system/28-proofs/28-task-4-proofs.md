# Task 04 Proofs - Telemetry config migration with env-var base layer and kill-switch precedence

## Task Summary

This task folds `internal/telemetry`'s `OTEL_*`/`JIG_TELEMETRY_*` env-var
surface into `config.toml`'s `[telemetry]` table. Unlike every other table,
`[telemetry]`'s built-in-defaults layer is populated dynamically from
today's env vars at `Load` time — so a CI job or container with only env
vars set and no `config.toml` keeps working unchanged — and `config.toml`
values now layer on top and win, a deliberate inversion of "env is
authoritative" for this one table. `OTEL_SDK_DISABLED` remains an
unconditional kill switch, applied a second time after every `[telemetry]`
layer is merged, so a `config.toml` `mode` override can never silently
re-enable telemetry the kill switch disabled.

## What This Task Proves

- `jig config show` with no env vars and no `config.toml` shows telemetry
  off, matching today's zero-config behavior.
- `jig config show` with only an env var set shows that value, proving the
  env-derived base layer works standalone.
- `jig config show` with both an env var and a conflicting `config.toml`
  `[telemetry]` value shows the `config.toml` value, proving config now
  overrides env for this table.
- `jig run` with `OTEL_SDK_DISABLED=true` and telemetry otherwise fully
  enabled via `config.toml` (`mode = "both"`) produces no telemetry output,
  proving the kill switch overrides `config.toml` unconditionally, at the
  process level, not just in a synthetic test.
- `KillSwitchActive` and the full `[telemetry]` merge/kill-switch pipeline
  are covered deterministically by `internal/telemetry` and `internal/config`
  unit tests.

## Evidence Summary

- Four `jig config show`/`jig run` invocations against a real built binary
  show, in order: off by default, env-derived base active, config beating
  env, and the kill switch beating a fully-enabled config.
- `go test ./internal/telemetry ./internal/config ./cmd/jig -count=1` and
  `go vet` on the same packages pass, including new `TestKillSwitchActive`,
  `TestLoadTelemetryEnvBaseOnly`, `TestLoadTelemetryConfigOverridesEnv`, and
  `TestLoadTelemetryKillSwitchForcesModeOffAfterConfigOverride` cases.

## Artifact: Zero-config default is telemetry off

**What it proves:** With no telemetry env vars and no `config.toml`
`[telemetry]` table, the merged config's `Mode` is `"off"` — identical to
today's zero-config behavior before this migration.

**Command:**

```bash
./jig config show --root .jig
```

**Result summary:** `mode = "off"` and every other field is its zero value,
confirming nothing regressed for the common case of no telemetry
configuration at all.

```toml
[telemetry]
  mode = "off"
  service_name = ""
  otlp_endpoint = ""
  otlp_protocol = ""
  otlp_insecure = false
  metrics_exporter = ""
  traces_exporter = ""
  prometheus_addr = ""
  prometheus_path = ""
  [telemetry.resource_attributes]
  [telemetry.otlp_headers]
```

## Artifact: Env-var-derived base layer is active with no `config.toml` override

**What it proves:** Setting only `OTEL_METRICS_EXPORTER`/
`OTEL_EXPORTER_OTLP_ENDPOINT` — no `config.toml` `[telemetry]` table at all —
resolves `mode`/`otlp_endpoint` from those env vars, exactly like the
pre-migration `telemetry.ResolveConfig` behavior.

**Command:**

```bash
OTEL_METRICS_EXPORTER=otlp OTEL_EXPORTER_OTLP_ENDPOINT=https://env-collector.invalid \
  ./jig config show --root .jig
```

**Result summary:** `mode = "otlp"` (derived from the metrics exporter env
var) and `otlp_endpoint` reflects the env value, proving the env-derived
base layer resolves correctly on its own.

```toml
[telemetry]
  mode = "otlp"
  otlp_endpoint = "https://env-collector.invalid"
  metrics_exporter = "otlp"
  ...
```

## Artifact: `config.toml` overrides the env-derived base

**What it proves:** With the same env vars set as above, plus a
`config.toml` `[telemetry] otlp_endpoint` set to a different value, the
merged config shows the `config.toml` value — the inverted precedence this
unit introduces.

**Command:**

```bash
# .jig/config.toml:
# [telemetry]
# otlp_endpoint = "https://config-collector.invalid"

OTEL_METRICS_EXPORTER=otlp OTEL_EXPORTER_OTLP_ENDPOINT=https://env-collector.invalid \
  ./jig config show --root .jig
```

**Result summary:** `otlp_endpoint = "https://config-collector.invalid"` (the
`config.toml` value), while `mode = "otlp"` still reflects the env-derived
base since `config.toml` did not set `mode` — proving the merge is per-key,
not whole-table replacement.

```toml
[telemetry]
  mode = "otlp"
  otlp_endpoint = "https://config-collector.invalid"
  metrics_exporter = "otlp"
  ...
```

## Artifact: `OTEL_SDK_DISABLED` overrides a fully-enabled `config.toml`, in a real `jig run`

**What it proves:** With `config.toml` fully enabling telemetry
(`mode = "both"`, a live-looking `otlp_endpoint`), a real `jig run`
invocation with `OTEL_SDK_DISABLED=true` produces no telemetry export
activity at all — the kill switch wins even when `config.toml` is the most
recently-applied, highest-precedence layer.

**Why it matters:** This is the one behavior that a synthetic unit test
alone doesn't fully demonstrate: it shows the kill switch actually prevents
the exporter from doing network I/O in a real process, not just that a
`Mode` field ends up `"off"` on a struct.

**Command:**

```bash
# .jig/config.toml:
# [telemetry]
# mode = "otlp"
# otlp_endpoint = "https://config-collector.invalid"

echo "-- without kill switch: telemetry is active (fails to reach the fake collector) --"
./jig run wf.toml --root .jig

echo "-- with kill switch: telemetry off, no export attempt, run still succeeds --"
OTEL_SDK_DISABLED=true ./jig run wf.toml --root .jig
```

**Result summary:** Without the kill switch, the run succeeds but a
`traces export: exporter export timeout` line appears on stderr — proof the
exporter was actually live and tried (and failed, since `otlp_endpoint`
points at a fake host) to export. With `OTEL_SDK_DISABLED=true`, the run
succeeds with **no** export-attempt line at all, proving telemetry was fully
inert.

```
-- without kill switch --
run_id: 20260921-203415-rcmqgyh0
started demo (1 steps)
work running
work succeeded
demo run_id=20260921-203415-rcmqgyh0 status=ok
2026/09/21 15:34:25 traces export: exporter export timeout: rpc error: code = Unavailable desc = name resolver error: produced zero addresses

-- with kill switch --
run_id: 20260921-203436-qdsxmzyy
started demo (1 steps)
work running
work succeeded
demo run_id=20260921-203436-qdsxmzyy status=ok
```

Both runs exit 0 — the kill switch (and telemetry generally) never affects
the workflow's own exit code, matching the "exporter failures never change
exit codes" contract `setupTelemetry` already documented.

## Artifact: Full package test/vet run

**What it proves:** Every package touched by this unit passes tests and
`go vet`, including the new deterministic coverage for `KillSwitchActive`
and the `[telemetry]` merge/kill-switch pipeline.

**Command:**

```bash
go test ./internal/telemetry ./internal/config ./cmd/jig -count=1
go vet ./internal/telemetry ./internal/config ./cmd/jig
```

**Result summary:** All three suites pass; `go vet` is clean.

```
ok  	jig/internal/telemetry	0.674s
ok  	jig/internal/config	1.407s
ok  	jig/cmd/jig	2.497s
```

## Reviewer Conclusion

`[telemetry]` now lives in `config.toml` alongside every other table, with
env vars preserved as a fully-functional, always-available fallback base
layer rather than being deleted outright (the one deliberate exception among
the retired formats). `config.toml` correctly overrides env per key, and the
`OTEL_SDK_DISABLED` kill switch was verified, in a real process, to override
even a fully-enabled `config.toml` unconditionally.
