# Implementation Plan: OTel / Prometheus / OTLP export (A18)

**Status:** Planned — goal A18 (P2)
**Risk:** **medium** — no schema changes to load-time validation, no scheduler
mutation, and no changes to durable content. The risks concentrate in three
places: (1) adding a new outbound / listening surface to a process that has
never had one, (2) redaction and cardinality on labels derived from step
content, and (3) staying strictly out of the scheduler's hot path even when the
collector is slow or unreachable.
**Depends on:** `Manager.Subscribe()` process-wide event bus
    10|(`internal/engine/engine.go:281-306`); `StepStatus.Cost` / `.Tokens` /
`.Attempt` / `.Iteration` / `.Generation` (`internal/engine/event.go:30-56`);
`RunSnapshot.TotalCostUSD` / `.TotalTokens` (`internal/engine/engine.go:123-131`);
`step.Result.Duration` and the `time.Since(start)` pattern already used in
`internal/runner/agent.go:52` and `internal/runner/command.go:48`;
`redactSecrets` (`internal/runner/command.go:196-203`) and `sentinel.RedactJSON`
(`internal/sentinel/rules.go:242-268`); the persistence-off convention that
empty `root` no-ops every writer.
**Complements:** A2 thin ops CLI (`jig status` / `jig logs` — same field
naming); A7 Codex parallel reliability (parallelism metrics per backend);
    20|A8 dynamic foreach fan-out (per-child spend/duration histograms); A19 remote
workers (needs cross-machine correlation).
**Breaks:** Nothing user-visible. Adds three new dependencies
(`go.opentelemetry.io/otel`, one OTLP exporter, and either the OTel Prometheus
exporter or `prometheus/client_golang`), and one new ADR sanctioning outbound
egress from `jig`. Default posture is **exporter off**; nothing changes for
operators who do not opt in.

---

    30|## Summary

Ship an optional observability exporter that publishes jig's already-durable
run/step signals — status transitions, per-step token/cost/duration, gate and
review lifecycle, retry attempts, security findings, and per-tool activity
counts — as OpenTelemetry metrics and spans, with a choice of OTLP push or
Prometheus pull. The exporter is a subscriber on `Manager.Subscribe()` and a
thin wrapper around `runner.Mux` for per-step timing; it never blocks the
scheduler, never emits step content in labels, never turns on without explicit
opt-in, and no-ops cleanly when persistence is off.

    40|## Approach

Build a new `internal/telemetry` package that owns the OTel provider lifecycle
(resource, meter provider, tracer provider, optional Prometheus handler) and
one adapter, `EventExporter`, that subscribes to `Manager.Subscribe()` and
translates events to OTel measurements without touching engine state. Wire
per-step spans through a small `TelemetryReporter` that wraps the
`engine.Reporter` the scheduler builds and a `metricMux` that wraps
`runner.Mux` in `cmd/jig/wire.go` — the two places every step already passes
through — so no engine internals move for a metrics-only change. Configuration
    50|is layered: OTel-standard `OTEL_*` env vars are the source of truth,
overridable per-run by a new optional `[telemetry]` block on `Workflow`
(peer of `[meta]`/`[defaults]`), and per-operator by a new
`.jig/telemetry.json` (peer of `internal/tui/prefs`). Defaults are: exporter
off, no Prometheus listener bound, no OTLP endpoint dialed, and any label
derived from step content passes through `redactSecrets` and a fixed allow-list
first. Follow the ADR precedent from Codex ACP diagnostics
(`docs/plan-codex-acp-concurrency-diagnostics.md`) for redaction posture and
extend it with a new ADR (proposed 0010) that records "off by default,
in-machine by default, redacted always" as the sanctioned stance.
    60|
---

## Problem

Every trust and cost signal jig knows about today lives inside one process and
one filesystem tree:

- Run and step **cost / tokens** are already tracked (`step.State.SpentUSD`,
  `RunSnapshot.TotalCostUSD`, `StepStatus.Cost`) but are only visible in the TUI
    70|  status line, `steps/<id>/result.json`, and the headless JSON envelope
  (`internal/headless/types.go:76-86`).
- **Durations** exist as `step.Result.Duration` (`internal/step/step.go:86`,
  serialized to `duration_ms`) but there is no per-run timeline, no per-tool
  timing, and no per-attempt histogram.
- The **journal** is a rich, timestamped stream (`internal/engine/journal.go`
  writes `run_started`, `run_finished`, `step_status`, `gate_result`,
  `review_request`, `security_finding`, `steps_reset`, `fan_out_expanded`, …),
  but consuming it requires a jig-aware reader.
- Fleet-scale questions — "which backend fails validation most", "how much did
    80|  our Claude spend today across every workflow", "which step type parks on
  review the longest", "is Codex ACP flaking under parallelism" (see
  `docs/plan-codex-acp-concurrency-diagnostics.md`) — currently require ad-hoc
  scripting over `.jig/runs/<id>/journal.jsonl`.
- There is no way for an operator to see jig activity next to the rest of their
  developer platform (Grafana, Honeycomb, Datadog, an existing OTel collector).

A18 exists to publish those already-durable signals in the two standard shapes
the ecosystem understands — an OpenTelemetry OTLP push (traces + metrics) and a
Prometheus scrape endpoint — without expanding what jig durably stores, without
    90|adding a second run database, and without giving the exporter any influence
over the scheduler's decisions.

The moving pieces already exist. `Manager.Subscribe()` returns a live/ctrl
channel pair that is already the natural sink for a process-wide observer;
`StepStatus` already carries cost/tokens/attempt/iteration/generation on every
terminal transition; every executor already goes through `runner.Mux`;
`redactSecrets` and `sentinel.RedactJSON` already scrub secret values before
persistence. A18 should compose those pieces rather than instrument the
scheduler by hand.

   100|## Goals

1. Give operators a supported way to observe jig activity in the OTel/Prometheus
   ecosystem — traces, metrics, and (optionally) logs — without leaving jig's
   deterministic scheduling story.
2. Publish per-run and per-step **cost, tokens, duration, attempts, iterations,
   generations, and terminal status** as histograms/counters/gauges keyed by
   workflow, step, step type, backend, transport, and model.
3. Publish **lifecycle events** — gate result, review requested/submitted,
   recovery/integration/final-merge park, security finding, foreach expansion,
   110|   reset — as counters and (where useful) as OTel span events.
4. Publish **per-tool activity** (name-only) and **network-request count** so
   parallelism and network-heavy flows are observable.
5. Support both **OTLP push** (endpoint dialed outbound) and **Prometheus pull**
   (endpoint bound in-process), selectable per-run.
6. Default to **exporter off**. Enable requires an explicit signal (env or
   TOML). No opt-out is required for existing installs.
7. Never block, delay, or gate the scheduler; a slow or dead collector must be
   observable but must not change run behavior or exit codes.
8. Never emit step content, prompts, transcript text, tool inputs, tool
   120|   outputs, secret values, or command environment as label or attribute values.
9. Preserve the persistence-off pattern: `NewManager(exec, "")` remains valid
   and the exporter is either off in that mode or emits only what does not
   depend on a run directory.
10. Add exactly one process outbound direction (OTLP) and one process inbound
    listener (Prometheus), both opt-in, both documented, both covered by a new
    ADR.

## Non-goals

- A **jig-hosted collector, TSDB, dashboard, or UI**. Rendering happens in the
   130|  operator's existing observability stack.
- **OTel logs export** in the first delivery. Transcripts remain the durable
  content record; log export is a natural follow-on but is deferred so the
  first exporter surface stays small (metrics + spans).
- **Cross-process trace context propagation** into agent turns (e.g. injecting
  `traceparent` into Claude SDK / ACP session prompts). This requires vendor
  cooperation and belongs to a later spec.
- **Cardinality of unbounded provenance** in labels: run ID, session ID, git
  SHA, and every fan-out child ID are span/attribute-only, never metric-label
  values. Metric cardinality is capped to a fixed dimension set.
   140|- **Reintroducing `JIG_HARNESS` / any env-based backend selection**. Backend /
  transport remain TOML-only (`AGENTS.md`); telemetry env vars follow the
  OpenTelemetry SDK spec (`OTEL_*`), which is a separate namespace.
- **Modifying `internal/engine`'s event set, journal encoding, or `Reporter`
  interface for cosmetics.** A18 subscribes to what exists; any new event type
  must justify itself independently and belongs to another spec.
- **A `.jig/metrics.jsonl`** or other local diagnostic file. The Codex ACP
  concurrency diagnostics plan already owns local per-step JSONL; A18's scope
  is off-machine (OTLP) or in-machine socket (Prometheus).
- **Auth beyond OTel-standard headers.** `OTEL_EXPORTER_OTLP_HEADERS` and
   150|  `OTEL_EXPORTER_OTLP_PROTOCOL` are the whole auth story for the first
  delivery; custom SDKs (Datadog agent, Honeycomb classic, etc.) work by
  pointing OTLP at their collectors.
- **Retroactive export of historical `.jig/runs/`**. A18 is live-only; a
  post-hoc backfill from journal is a follow-on that would reuse the same
  event→measurement map through `internal/ops`.

---

## Configuration surface

   160|A18 introduces three configuration layers, evaluated in order of precedence
(rightmost wins):

1. OTel-standard `OTEL_*` env vars (endpoint, headers, protocol, service name,
   resource attributes, exporter selection, interval).
2. Optional per-operator `.jig/telemetry.json` (peer of
   `internal/tui/prefs/prefs.go`).
3. Optional per-workflow `[telemetry]` block on `Workflow`
   (`internal/workflow/schema.go`; parallel to `[meta]` and `[defaults]`).

   170|### Env vars (source of truth)

Follow the OpenTelemetry Environment Variable Specification verbatim so
operators can reuse existing collector configuration:

| Env var | Effect |
|---|---|
| `OTEL_SERVICE_NAME` | Sets the `service.name` resource attribute (default `"jig"`). |
| `OTEL_RESOURCE_ATTRIBUTES` | Merged into the OTel resource (e.g. `deployment.environment=ci`). |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP endpoint URL. When set with `OTEL_METRICS_EXPORTER=otlp` and/or `OTEL_TRACES_EXPORTER=otlp`, enables the OTLP push exporter. |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `grpc`, `http/protobuf`, or `http/json` (SDK default rules). |
   180|| `OTEL_EXPORTER_OTLP_HEADERS` | Comma-separated `k=v` header list; opaque to jig. |
| `OTEL_METRICS_EXPORTER` | `otlp`, `prometheus`, `console`, or `none` (default). |
| `OTEL_TRACES_EXPORTER` | `otlp`, `console`, or `none` (default). |
| `OTEL_METRIC_EXPORT_INTERVAL` / `OTEL_METRIC_EXPORT_TIMEOUT` | SDK defaults, respected as-is. |
| `OTEL_SDK_DISABLED` | Global kill switch; forces exporter off even if the workflow enables it. |

Jig-specific supplement (used only when no OTel equivalent exists):

| Env var | Effect |
|---|---|
| `JIG_TELEMETRY_PROMETHEUS_ADDR` | Bind address for the Prometheus scrape endpoint (e.g. `127.0.0.1:9464`). Empty (default) disables the listener even when `OTEL_METRICS_EXPORTER=prometheus`. |
   190|| `JIG_TELEMETRY_PROMETHEUS_PATH` | HTTP path for `/metrics` (default `/metrics`). |
| `JIG_TELEMETRY_MODE` | `off` (default), `prom`, `otlp`, or `both`. Overrides the pair of OTel exporter env vars for authors who want one knob. |

`JIG_TELEMETRY_*` names never overlap with `JIG_SECRET_*` (secret name
resolution — `cmd/jig/wire.go:33-42`) or with `JIG_INPUT_*` / `JIG_FANOUT_*`
(command child inputs — `internal/runner/command.go:180-193`).

### `.jig/telemetry.json` (per operator)

New file, mirrors `internal/tui/prefs/prefs.go` exactly: `Default()`, `Path()`,
   200|`Load()` (missing/corrupt → default), `Save()` (empty root is a no-op, atomic
tmp+rename write). Shape:

```json
{
  "mode": "off",
  "otlp_endpoint": "",
  "prometheus_addr": "",
  "resource_attributes": {"env": "dev"},
  "sample_transcripts": false
}
```

   210|`Load()` never errors — a bad file falls back to defaults so a broken
telemetry config never bricks the TUI or `jig run`, matching the existing
`tui.json` precedent.

### `[telemetry]` workflow block

New optional top-level field on `Workflow` (`internal/workflow/schema.go`),
parallel to `Meta` and `Defaults`. Only fields that are meaningful per workflow
belong here; endpoint URL and headers stay in env/`.jig/telemetry.json`:

   220|```toml
[telemetry]
enabled = true                # default false
export_thinking_counts = false # never export CoT text; count only when true
metric_prefix = "jig"         # default; used for all metric names below
resource_attributes = { workflow_owner = "platform" }
```

Load-time validation rules (`internal/workflow/validate.go`):

- `enabled` must be bool.
   230|- `metric_prefix` must match `[a-zA-Z][a-zA-Z0-9_]{0,31}` so it is a legal
  Prometheus / OTel metric name prefix.
- `resource_attributes` keys must be legal OTel attribute keys (dotted,
  lowercase, no spaces); values must be strings.
- No per-step telemetry knobs in the first delivery. If per-step opt-out is
  needed later, it plugs in at `Step` the same way `StepSecurity` does today.
- Unknown fields fail load (matching the strict decoder used elsewhere).

Add both valid and invalid TOML fixtures to `internal/workflow/workflow_test.go`
following the house pattern (`docs/TESTING.md`).

   240|### Precedence

Env `OTEL_SDK_DISABLED=true` always wins (kill switch).
Otherwise: workflow `[telemetry].enabled = false` disables at run start;
`enabled = true` requires that env or `.jig/telemetry.json` already selected an
exporter target, otherwise validate fails at load time with an actionable
message (`"[telemetry] enabled but no exporter configured (set OTEL_METRICS_EXPORTER or JIG_TELEMETRY_MODE)"`).

### Persistence-off

When `NewManager(exec, "")` is used, telemetry still initializes if env
   250|configuration is present, but only measurements that do not require a run
directory (no manifest reads, no journal replay) are emitted. Workflow-level
`[telemetry]` in a persistence-off engine test is applied identically because
the config lives in `workflow.Workflow` (in memory), not `.jig/`.

---

## Signal model

The exporter is a **process-wide subscriber**: `EventExporter.Attach(m
   260|*engine.Manager)` calls `Subscribe()` and drains both channels in a dedicated
goroutine. All measurements are recorded via OTel instruments held by the
`internal/telemetry` provider. There is no per-run singleton; every event
carries `RunID` / `StepID`, which the exporter renders as span attributes and
never as metric-label values.

### Metrics (default cardinality)

Metric names use OTel/Prom conventions (no `.` in Prom names — the OTel
Prometheus exporter handles translation). Prefix defaults to `jig` and is
   270|configurable via `[telemetry].metric_prefix`.

Label set (fixed, applied to every metric unless noted):

- `workflow` — `workflow.Meta.Name`.
- `step` — step ID from workflow (never fan-out child ID; those go on spans).
- `step_type` — `agent` / `command` / `check` / `review`.
- `backend` — resolved backend (`claude` / `cursor` / `codex` / …).
- `transport` — `sdk` / `acp` (empty for non-agent steps).
- `model` — resolved model or `""` when unknown.
- `outcome` — for terminal metrics: `succeeded` / `failed` / `skipped` /
  280|  `stopped` / `recovered`.
- Optional custom attributes from `[telemetry].resource_attributes` (resource,
  not per-measurement).

Fan-out family/child IDs, session IDs, git SHAs, and secret values never appear
as label values. Fan-out is expressed as `foreach.total` and `foreach.index`
attributes on **spans**, not on metric labels, so histograms remain bounded.

**Instruments:**

| Instrument | Type | Unit | Description |
|---|---|---|---|
   290|| `jig.run.started` | counter | `1` | Increments on `RunStarted`. Labels: `workflow`. |
| `jig.run.finished` | counter | `1` | Increments on `RunFinished`. Labels: `workflow`, `outcome` (`succeeded`/`failed`). |
| `jig.run.duration` | histogram | `s` | Wall clock between `RunStarted` and `RunFinished`. |
| `jig.run.cost_usd` | histogram | `USD` | `RunSnapshot.TotalCostUSD` at run finish. |
| `jig.run.tokens` | histogram | `1` | `RunSnapshot.TotalTokens` at run finish. |
| `jig.step.started` | counter | `1` | Increments on `pending → running`. |
| `jig.step.finished` | counter | `1` | Increments on any terminal `StepStatus`. Labels include `outcome`. |
| `jig.step.duration` | histogram | `s` | Delta between `StepStatus{To:running}` and terminal `StepStatus` for the same `(RunID, StepID, Attempt, Iteration, Generation)` tuple. |
| `jig.step.cost_usd` | histogram | `USD` | `StepStatus.Cost` on terminal transitions (recorded only when non-nil). |
| `jig.step.tokens` | histogram | `1` | `StepStatus.Tokens` on terminal transitions. |
   300|| `jig.step.attempts` | histogram | `1` | `StepStatus.Attempt + 1` on terminal transitions. |
| `jig.step.iterations` | histogram | `1` | `StepStatus.Iteration + 1` on terminal transitions. |
| `jig.step.generations` | histogram | `1` | `StepStatus.Generation` on terminal transitions. |
| `jig.gate.result` | counter | `1` | Increments on `GateResult`. Labels include gate outcome + rule name. |
| `jig.review.requested` / `.submitted` | counter | `1` | Increments on the review events; labels: `workflow`, `step`. |
| `jig.review.wait_duration` | histogram | `s` | Delta between `ReviewRequest` and matching `ReviewSubmitted`. |
| `jig.recovery.requested` | counter | `1` | Increments on `RecoveryRequest`. Labels include `cause` (from `RecoveryAction`). |
| `jig.integration.conflict` | counter | `1` | Increments on `IntegrationConflictRequest`. |
| `jig.final_merge.requested` | counter | `1` | Increments on `FinalMergeRequest`. |
| `jig.security.finding` | counter | `1` | Increments on `SecurityFinding`. Labels: `workflow`, `step`, `tier` (`1`/`2`), `severity`. Never labels with the finding message. |
   310|| `jig.foreach.expanded` | counter | `1` | Increments on `FanOutExpanded`. Additional label: `family` (declared step ID; not child IDs). |
| `jig.reset.applied` | counter | `1` | Increments on `StepsReset`. Labels: `workflow`, `target_step`. |
| `jig.tool.invoked` | counter | `1` | Increments on each `Reporter.ToolCall` (via wrapping reporter). Labels: `workflow`, `step`, `tool`. |
| `jig.tool.network_request` | counter | `1` | Increments when the runner fires its existing `NetworkRequest` hook (`internal/engine/executor.go:96-98`). Labels: `workflow`, `step`, `backend`. |
| `jig.exporter.dropped` | counter | `1` | Increments when the exporter had to drop a bus event (drop-on-full or backpressure). Labels: `kind` (event kind). |

Every metric is derivable from data jig already durably persists — no new
durable state is introduced. `Cost` is recorded only when non-nil so
"unreported" stays distinct from "reported zero" (`step.Result.TotalCostUSD`
   320|preserves the same distinction — `internal/step/step.go:87-97`).

### Spans

Emitted only when `OTEL_TRACES_EXPORTER != "none"`. Metrics work without spans.

- `jig.run` — one span per run, from `RunStarted` to `RunFinished`. Attributes:
  `workflow`, `run.id`, resource attributes.
- `jig.step` — one child span per step attempt/iteration/generation tuple, from
  `pending → running` to terminal. Attributes: `step`, `step_type`, `backend`,
   330|  `transport`, `model`, `attempt`, `iteration`, `generation`, `outcome`,
  `foreach.index`, `foreach.total`, `foreach.family`, `session.id` when known.
- `jig.tool` — one child span per tool call, wrapping the runner's per-tool
  observation. Attributes: `tool`, `duration_ms`. Body is never attached.

Span events (recorded on `jig.step`): `gate.result`, `review.requested`,
`review.submitted`, `recovery.requested`, `security.finding`,
`integration.conflict`, `final_merge.requested`. Events carry structured
attributes (`gate.rule`, `severity`, etc.) but never the finding message or
review body.

   340|Trace context is not propagated **into** agents in the first delivery (see
non-goals). Spans are jig-local; a downstream operator can correlate by
resource attributes (`service.name`, `deployment.environment`).

---

## Architecture and ownership

```text
   350|                           OTel-standard OTEL_* env
                              (endpoint / headers / etc.)
                                        |
cmd/jig -----> internal/telemetry.Init(root, wf) --> OTel providers
   |                     ^                            (Meter, Tracer,
   |                     |                             Prometheus Handler,
   |                     |                             OTLP Exporter)
   |                     |
   |     workflow.Telemetry / .jig/telemetry.json / JIG_TELEMETRY_*
   |
   360||
   +--> engine.NewManager(mux, root)
   |          |
   |          +-- subs: EventExporter (telemetry) ----> Meter/Tracer
   |
   +--> runner.NewMux (wrapped by telemetry.metricMux) --> per-step span
                       |                                   + duration histogram
                       +-- inner mux dispatches ordinary executors

.jig/telemetry.json (optional, per operator)
   370|
Prometheus listener (optional):
   127.0.0.1:9464/metrics served by internal/telemetry, lifecycle tied to
   Manager (started before subscribe, stopped after RunFinished drain).
```

Key boundaries:

- `internal/engine` gets **zero** new imports. It exposes only what already
  exists (`Subscribe`, `Snapshot`, event types, `Reporter`).
- `internal/runner` gets **zero** new imports; `metricMux` wraps `runner.Mux`
   380|  in `cmd/jig/wire.go` and lives entirely inside `internal/telemetry`.
- `internal/telemetry` is the only package that imports OpenTelemetry. It
  imports `internal/engine` (event types), `internal/workflow` (config type),
  and `internal/step` (status type); it never imports `internal/tui`.
- `internal/tui` gets no new imports; a small status-line indicator ("otel:on")
  reads from a boolean the telemetry package exposes.
- `internal/ops` gets no new dependency in this delivery. A future
  post-hoc/backfill exporter would reuse the same event → measurement map.
- Persistence-off remains valid: telemetry initialization succeeds when
  `root == ""` and only skips measurements that require a run directory.

   390|---

## Delivery phases

### Phase 1 — package scaffold and no-op provider

1. Create `internal/telemetry` package with `Config`, `Init(cfg) (*Provider,
   error)`, `Provider.Meter()`, `Provider.Tracer()`, and `Provider.Shutdown()`.
2. Add `go.opentelemetry.io/otel`, `go.opentelemetry.io/otel/sdk`,
   `go.opentelemetry.io/otel/sdk/metric` to `go.mod`. Deliberately do **not**
   add exporter dependencies yet.
   400|3. Implement a `NoopExporter` used when `mode == off`; provider returns
   `noop.NewMeterProvider()` and `trace.NewNoopTracerProvider()` under the
   hood.

**Exit:** `internal/telemetry` builds, `go vet` passes, and an engine test can
call `telemetry.Init(Config{})` without initializing any exporter.

### Phase 2 — configuration surfaces

1. Add `Workflow.Telemetry` with `Enabled`, `MetricPrefix`,
   `ExportThinkingCounts`, `ResourceAttributes`; parse/default/validate in
   410|   `internal/workflow`.
2. Add `.jig/telemetry.json` reader/writer as a peer of `internal/tui/prefs`.
3. Add env parsing (OTel-standard + `JIG_TELEMETRY_*` supplement) inside
   `internal/telemetry`.
4. Add table-driven tests for every valid/invalid combination and precedence
   rule (env kill switch, workflow enabled without exporter target, malformed
   metric prefix, unknown workflow field, corrupt `.jig/telemetry.json`).

**Exit:** every configuration source is parsed and validated in isolation,
`.agents/jig/feature.toml` still validates unchanged.

   420|### Phase 3 — event-to-measurement adapter (metrics only)

1. Add `EventExporter.Attach(m *engine.Manager)`; drain live+ctrl on a
   dedicated goroutine bounded by a `context.Context` the caller cancels on
   shutdown.
2. Implement the metric table (Signal model → Metrics). Route `StepStatus.Cost`
   through nil-aware recording; derive `jig.step.duration` from a
   scheduler-provided timestamp on `StepStatus{To:running}` matched to the
   terminal transition by `(RunID, StepID, Attempt, Iteration, Generation)`.
3. Implement `jig.exporter.dropped` self-observability: when the subscriber
   430|   cannot keep up with the drop-on-full live bus, increment this counter
   before continuing to drain.

**Exit:** an engine test using the fake executor drives a small workflow, an
in-process OTel `metricdata.ResourceMetrics` reader captures the expected
counters/histograms, and asserting on them passes deterministically.

### Phase 4 — per-step spans and per-tool activity

1. Wrap the `engine.Reporter` the scheduler constructs with
   `telemetry.TelemetryReporter` inside `EventExporter`, translating each
   440|   `Reporter.ToolCall` into a `jig.tool.invoked` counter increment and (when
   tracing is on) a `jig.tool` child span.
2. Add `metricMux` in `internal/telemetry`; wire it around `runner.Mux` in
   `cmd/jig/wire.go` so per-step `time.Since(start)` spans are recorded at
   `runner.Mux.Execute` boundaries.
3. Add the existing `NetworkRequest` callback to `jig.tool.network_request`.

**Exit:** unit tests verify per-step span start/end, one child span per
`Reporter.ToolCall`, and non-zero span duration for a fake executor.

### Phase 5 — exporter surfaces
   450|
1. Add OTLP metric exporter (`go.opentelemetry.io/otel/exporters/otlp/otlpmetricgrpc`
   or `otlpmetrichttp`; expose both, protocol-select via
   `OTEL_EXPORTER_OTLP_PROTOCOL`). Same for traces if `OTEL_TRACES_EXPORTER=otlp`.
2. Add the OTel Prometheus exporter (`go.opentelemetry.io/otel/exporters/prometheus`)
   and a minimal `net/http` server bound to `JIG_TELEMETRY_PROMETHEUS_ADDR`,
   default disabled. The listener uses `net/http.Server` with explicit
   `ReadHeaderTimeout`, an idle timeout, and a graceful `Shutdown(ctx)` tied to
   provider `Shutdown`.
3. Emit `console` exporter when `OTEL_METRICS_EXPORTER=console` (useful for
   460|   local debugging without a collector).

**Exit:** `curl http://127.0.0.1:9464/metrics` on an enabled `jig run` returns
the expected series, and an OTLP-mocked server records the same measurements
via push.

### Phase 6 — CLI, TUI, docs, and dogfood

1. `cmd/jig`: initialize provider before `newManager`; attach exporter after
   `Subscribe`; ensure `Shutdown(ctx)` runs on run terminal drain in both TUI
   and headless paths.
   470|2. `internal/tui`: read a boolean from telemetry provider and render a
   right-aligned status indicator (`otel` / `prom` / `off`) in the status line;
   no new theme fields, reuse `theme.Muted`.
3. `docs/observability.md` (new): full env/config table, metric catalog, span
   catalog, redaction rules, Prometheus setup snippet, OTLP setup snippet,
   security stance summary, links to ADR 0010 and the T6 transcript-redaction
   discussion in `open-goals.md`.
4. ADR 0010 (new): "OTel/Prometheus export is opt-in, in-machine by default,
   redacted always."
5. Update `docs/plans/open-goals.md` to link the plan and mark A18 as planned
   480|   (not done) with a pointer to this document.
6. Update `docs/ARCHITECTURE.md` and `docs/engine-design.md` to describe the
   telemetry seam.
7. Add one dogfood invocation to `.agents/jig/feature.toml` verification: the
   kitchen-sink workflow still validates (no telemetry block by default), and a
   small `examples/observability-smoke.toml` runs under headless with
   `JIG_TELEMETRY_MODE=prom JIG_TELEMETRY_PROMETHEUS_ADDR=127.0.0.1:0` and
   asserts `/metrics` returns the expected families.

**Exit:** every documented env var / config combination is exercised, both
reference workflows validate, `docs/observability.md` is the operator contract,
   490|and the ADR is linked from `docs/adr/README.md` (if present) and from A18 in
`open-goals.md`.

---

## Ordered implementation tasks

Each task is confined to one package or file. Tests follow the substantive
change they prove.

   500|| # | Title | Area | Estimate |
|---:|---|---|---:|
| 1 | Create `internal/telemetry` package skeleton with `Config`, `Provider`, `Init`, `Shutdown`, and a noop provider | `internal/telemetry/telemetry.go` | 30 min |
| 2 | Add OpenTelemetry SDK deps to `go.mod` (`otel`, `sdk`, `sdk/metric`); do not add exporters yet | `go.mod` / `go.sum` | 15 min |
| 3 | Test `Init` returns a working noop provider, `Shutdown` is idempotent, and every method is safe on a zero `Provider` | `internal/telemetry/telemetry_test.go` | 20 min |
| 4 | Add `Telemetry` field to `Workflow` with `Enabled`, `MetricPrefix`, `ExportThinkingCounts`, `ResourceAttributes`; parse and default | `internal/workflow/schema.go`, `internal/workflow/load.go` | 30 min |
| 5 | Validate metric prefix regex, resource attribute keys, exporter-target coupling, unknown fields | `internal/workflow/validate.go` | 30 min |
| 6 | Add valid + invalid workflow fixtures for `[telemetry]` | `internal/workflow/workflow_test.go` | 25 min |
| 7 | Add `.jig/telemetry.json` reader/writer mirroring `internal/tui/prefs` (default, load, atomic save, empty-root no-op) | `internal/telemetry/prefs.go` | 25 min |
| 8 | Test `.jig/telemetry.json` missing/corrupt/valid; atomic write; empty-root no-op | `internal/telemetry/prefs_test.go` | 25 min |
   510|| 9 | Parse OTel-standard `OTEL_*` env + jig supplement (`JIG_TELEMETRY_*`) into `telemetry.Config`; env kill switch (`OTEL_SDK_DISABLED`) wins | `internal/telemetry/config.go` | 30 min |
| 10 | Test precedence: env > `.jig/telemetry.json` > `[telemetry]`; kill switch; enabled-without-exporter validation error | `internal/telemetry/config_test.go` | 30 min |
| 11 | Add `EventExporter` that subscribes to `Manager.Subscribe()` and drains live+ctrl on a bounded goroutine | `internal/telemetry/exporter.go` | 35 min |
| 12 | Implement metric instruments (counters/histograms) for run/step lifecycle, cost, tokens, duration, attempts | `internal/telemetry/metrics.go` | 40 min |
| 13 | Implement gate/review/recovery/security/foreach/reset counters and wait-duration histograms | `internal/telemetry/metrics.go` | 30 min |
| 14 | Add `jig.exporter.dropped` self-observability and back-pressure diagnostic | `internal/telemetry/metrics.go` | 15 min |
| 15 | Test full metric surface with a fake executor and an in-process OTel `metricdata` reader, including nil-Cost handling and fan-out cardinality caps | `internal/telemetry/metrics_test.go` | 45 min |
| 16 | Add `TelemetryReporter` wrapping `engine.Reporter` (per-tool counter + span) and `metricMux` wrapping `runner.Mux` (per-step span + duration) | `internal/telemetry/reporter.go` | 35 min |
| 17 | Test span parent/child structure, per-tool count, per-step span duration for both success and failure | `internal/telemetry/reporter_test.go` | 30 min |
| 18 | Add OTLP metric+trace exporters (`otlpmetric[grpc|http]`, `otlptrace[grpc|http]`) with protocol selection | `internal/telemetry/otlp.go` | 30 min |
   520|| 19 | Test OTLP export against an in-process mock collector; verify protocol selection, headers pass-through, and timeout behavior | `internal/telemetry/otlp_test.go` | 30 min |
| 20 | Add Prometheus exporter + `net/http` server with `ReadHeaderTimeout`, idle timeout, and `Shutdown(ctx)` | `internal/telemetry/prom.go` | 30 min |
| 21 | Test Prometheus handler serves the expected metric families, listener starts/stops cleanly, and empty bind addr disables the listener | `internal/telemetry/prom_test.go` | 30 min |
| 22 | Wire provider init and exporter attach in `cmd/jig` for both TUI and `jig run`; ensure `Shutdown(ctx)` on terminal drain and on signal cancellation | `cmd/jig/wire.go`, `cmd/jig/main.go`, `cmd/jig/ops.go` (or whichever run entry lives there) | 30 min |
| 23 | Add a right-aligned status-line indicator (`otel` / `prom` / `off`) using existing theme tokens | `internal/tui/monitor/monitor_view.go` (or the current status-line file) | 20 min |
| 24 | Test status-line indicator string derivation from the provider mode | `internal/tui/monitor/monitor_test.go` (or the correct test file) | 20 min |
| 25 | Add ADR 0010 sanctioning off-machine egress with redaction and cardinality rules | `docs/adr/0010-observability-export.md` | 30 min |
| 26 | Write `docs/observability.md` (env/config table, metric catalog, span catalog, redaction, examples for Prom scrape and OTLP push, security stance) | `docs/observability.md` | 45 min |
| 27 | Cross-reference from `docs/engine-design.md` (subscriber pattern, Reporter wrap) and `docs/ARCHITECTURE.md` (new `internal/telemetry` package) | `docs/engine-design.md`, `docs/ARCHITECTURE.md` | 20 min |
| 28 | Add `examples/observability-smoke.toml` and a headless smoke test with `JIG_TELEMETRY_PROMETHEUS_ADDR=127.0.0.1:0` verifying `/metrics` families | `examples/observability-smoke.toml`, `internal/telemetry/smoke_test.go` | 30 min |
   530|| 29 | Update `docs/plans/open-goals.md`: A18 → planned + link to this plan; add to F. Related docs; update D. Ranked top 20 if applicable | `docs/plans/open-goals.md` | 15 min |
| 30 | Run focused telemetry/workflow/engine tests, full tests, race-sensitive engine tests, vet, build, both workflow validations | repository-wide verification | 30 min |

Estimated focused implementation time: **13–15 hours**, best delivered as the
six phases above rather than one review unit.

---

## Test and proof matrix

| Contract | Required proof |
   540||---|---|
| Off by default | Fresh checkout with no env / no `[telemetry]` / no `.jig/telemetry.json` runs a workflow with byte-identical engine, transcript, and headless output as before the change |
| Kill switch | `OTEL_SDK_DISABLED=true` disables the exporter regardless of workflow/prefs |
| Validation | `[telemetry].enabled=true` without an exporter target fails at `jig validate`; malformed metric prefix / resource attribute fails at load |
| Precedence | Env > `.jig/telemetry.json` > `[telemetry]`; workflow-enabled respects env-disabled |
| Persistence-off | `NewManager(exec, "")` + telemetry produces run/step counters without file writes |
| Metric shape | Every listed instrument appears with the documented labels; nil `Cost` is recorded as no-measurement (not zero); attempt/iteration/generation histograms use the correct fields |
| Cardinality | Fan-out family/child IDs, session IDs, and secret values never appear as metric-label values; asserted with a `metricdata` inspection helper |
| Redaction | Any label derived from step content passes through `redactSecrets` (`internal/runner/command.go:196-203`) and the sentinel secret patterns (`internal/sentinel/rules.go:14-70`); assertion covers `AKIA…`, PEM, GitHub token forms |
| Non-blocking | A slow / dead OTLP endpoint or a paused scrape client does not delay `Manager.Subscribe()` drain, `RunFinished` emission, or exit codes; drop counter increments visibly |
   550|| Spans | Per-step spans are children of the per-run span; per-tool spans are children of per-step spans; span attributes include only allow-listed keys |
| Exporter parity | OTLP push and Prometheus scrape produce the same instrument set for the same events |
| Shutdown | `Provider.Shutdown(ctx)` is idempotent, drains buffered measurements, closes the Prom listener, and respects `ctx` deadline |
| TUI | Status-line indicator matches the mode; no color-only cue |
| Docs | `docs/observability.md` metric catalog matches the runtime surface (test asserts every documented name is registered) |
| Regression | `go test ./...`, `go test -race ./internal/engine ./internal/telemetry`, `go vet ./...`, `go build ./cmd/jig`, both reference workflow validations |

## Verification commands

```bash
   560|go test ./internal/telemetry
go test ./internal/workflow
go test ./internal/engine
go test -race ./internal/engine ./internal/telemetry
go test ./...
go vet ./...
go build ./cmd/jig
go run ./cmd/jig validate .agents/jig/feature.toml
go run ./cmd/jig validate examples/observability-smoke.toml
   570|JIG_TELEMETRY_MODE=prom JIG_TELEMETRY_PROMETHEUS_ADDR=127.0.0.1:0 \
  go run ./cmd/jig run examples/observability-smoke.toml --ci
```

---

## Security and failure handling

- **Egress is opt-in only.** Default `mode == off` binds no listener and dials
  no endpoint. Existing installs behave byte-identically after upgrade.
   580|- **Prometheus listener defaults to `127.0.0.1`.** A blank `JIG_TELEMETRY_PROMETHEUS_ADDR`
  disables the listener entirely; a `0.0.0.0` bind is possible but requires the
  operator to type it and is documented as a step outside the default posture.
- **No step content in labels or attributes.** Metric labels come from a fixed
  allow-list (`workflow`, `step`, `step_type`, `backend`, `transport`,
  `model`, `outcome`, gate rule name, tier, severity). Span attributes are
  allow-listed similarly.
- **Redaction is unconditional.** Any string label derived from user content
  (tool name, gate rule, review outcome text) passes through `redactSecrets`
  and the sentinel patterns before recording. A test asserts that even a
   590|  deliberately spilled secret in a tool name is rendered as `[REDACTED]` on
  the wire.
- **Cardinality is bounded.** Fan-out family/child IDs are span attributes; run
  IDs are span attributes; git SHAs are span attributes; nothing that grows
  unboundedly per run appears on a metric.
- **The scheduler owns the hot path.** The exporter subscribes to
  `Manager.Subscribe()`, which is drop-on-full for the `live` channel and
  bounded for `ctrl`. If the exporter cannot keep up, it drops and increments
  `jig.exporter.dropped`; it never applies backpressure to the scheduler.
- **Provider errors are logged once, not fatal.** OTLP dial failure, Prometheus
   600|  bind failure, or exporter shutdown timeout logs a single warning to stderr
  and continues with a noop provider. `jig run` exit codes are unaffected.
- **No secrets in headers.** `OTEL_EXPORTER_OTLP_HEADERS` is passed through
  opaquely to the OTel SDK; jig neither reads nor logs it. `jig doctor` will
  never report its value (aligning with A2's stance on secret handling).
- **ADR 0010** records the stance so future changes cannot silently expand the
  export surface.

## Risks and mitigations

   610|| Risk | Mitigation |
|---|---|
| Off-machine egress ships enabled and surprises operators | Default `mode == off`; env kill switch honored; upgrade path is byte-identical; ADR + docs require explicit opt-in |
| Metric labels leak secrets | Fixed allow-list, unconditional redaction, sentinel pattern scan, and a golden test that asserts no field outside the allow-list ever appears |
| Metric cardinality explodes under fan-out | Family/child IDs stay on spans; `jig.foreach.expanded` labels use the declared family step ID only; `foreach.total`/`foreach.index` are span attributes |
| Exporter blocks the scheduler | Drop-on-full drain semantics preserved; `jig.exporter.dropped` counter provides self-observability; race test proves no scheduler stall under a slow reader |
| OTLP dial failure kills the run | Provider init errors log once and downgrade to noop; run continues; test covers `dial tcp: connect: connection refused` |
| Prometheus listener exposes internals unexpectedly | Default bind is `127.0.0.1`; empty bind disables; only the OTel-emitted metric families are served (no debug/pprof) |
| Duration histograms mislabel retries | Duration key is the `(RunID, StepID, Attempt, Iteration, Generation)` tuple; matching test covers retry, loop, and reset |
| Trace context accidentally flows into agent prompts | First delivery does not inject `traceparent` anywhere; test asserts the runner does not add trace headers to Claude/ACP session prompts |
   620|| Existing tests break because scheduler behavior changes under an active exporter | `EventExporter.Attach` is a read-only subscriber and does not mutate engine state; comparison test runs the same workflow with and without an exporter and asserts identical journal bytes |
| Persistence-off tests start writing files | `internal/telemetry/prefs` mirrors the `internal/tui/prefs` empty-root no-op; explicit test proves no writes when `root == ""` |
| Nil cost recorded as zero, muddying dashboards | Recording path checks `Cost == nil` before histogram record; test covers ACP (no cost) vs. Claude SDK (cost) side by side |
| Docs and code drift | A `TestMetricCatalog` test asserts every metric name registered at runtime is documented in `docs/observability.md`, and vice versa |

## Completion criteria

A18 is done when:

   630|1. `internal/telemetry` initializes cleanly for `off`, `prom`, `otlp`, and
   `both` modes, from any documented configuration source, without any change
   to the scheduler's public API.
2. Enabling the exporter emits the full metric catalog and the documented span
   surface under both a Prometheus scrape and an OTLP push, with byte-identical
   journal/transcript/headless output compared to the exporter-off baseline.
3. Every metric label and span attribute comes from a fixed allow-list; secret
   patterns are unconditionally redacted; fan-out fan-in cannot inflate
   cardinality.
4. A slow, dead, or backpressured collector never delays `RunFinished` or
   640|   changes `jig run` exit codes; the drop counter reports observed loss.
5. `.jig/telemetry.json`, `[telemetry]`, and `OTEL_*` / `JIG_TELEMETRY_*` env
   vars are documented, validated, and precedence-ordered.
6. `docs/observability.md`, ADR 0010, and updates to `docs/engine-design.md`
   and `docs/ARCHITECTURE.md` describe the exporter contract; `docs/plans/open-goals.md`
   A18 links this plan and stays marked planned until code lands.
7. `.agents/jig/feature.toml` still validates unchanged; the new
   `examples/observability-smoke.toml` validates and runs headless with a
   Prometheus listener on `127.0.0.1:0`.
8. `go test ./...`, `go test -race ./internal/engine ./internal/telemetry`,
   650|   `go vet ./...`, and `go build ./cmd/jig` all pass on the change branch.

## Acceptance checklist

- [ ] Exporter defaults off; no metrics/spans/listener without explicit opt-in.
- [ ] `OTEL_SDK_DISABLED=true` overrides every other config source.
- [ ] `[telemetry].enabled=true` without a configured exporter fails at
      `jig validate` with an actionable message.
- [ ] Every metric in the documented catalog is registered and exported under
      both Prometheus and OTLP.
   660|- [ ] Per-step spans are children of the per-run span; per-tool spans are
      children of the per-step span; attributes come from the allow-list.
- [ ] Fan-out and reset produce the expected counters without inflating metric
      cardinality; per-child provenance lives on spans only.
- [ ] Secret-shaped strings in any potential label position are redacted
      before recording; a targeted test proves it.
- [ ] A dead OTLP endpoint and a paused Prom scrape client do not stall the
      scheduler; `jig.exporter.dropped` reflects observed loss.
- [ ] Persistence-off (`root == ""`) still runs; telemetry emits what it can
      without any file writes.
   670|- [ ] `docs/observability.md`, ADR 0010, engine/architecture cross-links, and
      the `open-goals.md` update are all in place.
- [ ] Full test/race/vet/build and both reference workflow validations pass.
