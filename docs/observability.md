# Observability export (A18)

jig has an optional OpenTelemetry exporter that publishes the run and step
signals it already durably tracks — status transitions, per-step cost /
tokens / duration, gate and review lifecycle, retry attempts, security
findings, and per-tool activity counts — as OTel metrics and spans. The
exporter is:

- **Off by default.** A fresh install binds no listener, dials no endpoint,
  and starts no exporter goroutine.
- **In-machine by default.** When Prometheus is enabled, the listener binds
  `127.0.0.1` unless the operator explicitly types a wider address.
- **Redacted always.** Every label value is scrubbed with the same
  `sentinel.Redact` rules that gate the transcript.
- **Non-blocking.** The exporter subscribes to `Manager.Subscribe()` and
  drains the bus on its own goroutine; a slow or dead collector never delays
  `RunFinished` or changes `jig run` exit codes.

The posture is fixed by [ADR 0012](adr/0012-observability-export.md). The
implementation plan is [`docs/plans/a18-otel-prometheus-export.md`](plans/a18-otel-prometheus-export.md).

---

## Turning it on

Configuration is layered. The rightmost source wins:

1. OTel-standard `OTEL_*` env vars.
2. `JIG_TELEMETRY_*` env vars (jig-specific supplement).
3. `.jig/telemetry.json` (per operator).
4. Workflow `[telemetry]` block (`enabled = true` requires that an exporter
   has already been selected via env or prefs).

`OTEL_SDK_DISABLED=true` is a hard kill switch and wins over every source.

### Quick starts

**Prometheus scrape (recommended for local development):**

```bash
export JIG_TELEMETRY_MODE=prom
export JIG_TELEMETRY_PROMETHEUS_ADDR=127.0.0.1:9464
jig run examples/observability-smoke.toml --ci &
curl -s http://127.0.0.1:9464/metrics | head
```

**OTLP push (production collector):**

```bash
export JIG_TELEMETRY_MODE=otlp
export OTEL_EXPORTER_OTLP_ENDPOINT=http://collector.internal:4318
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_HEADERS=x-honeycomb-team=$HONEYCOMB_KEY
jig run my-workflow.toml --ci
```

**Console (for one-off debugging without any collector):**

```bash
export OTEL_METRICS_EXPORTER=console
export OTEL_TRACES_EXPORTER=console
jig run my-workflow.toml --ci
```

**Both Prometheus and OTLP:**

```bash
export JIG_TELEMETRY_MODE=both
export JIG_TELEMETRY_PROMETHEUS_ADDR=127.0.0.1:9464
export OTEL_EXPORTER_OTLP_ENDPOINT=http://collector.internal:4318
jig run my-workflow.toml --ci
```

---

## Configuration reference

### OTel-standard env vars

jig follows the [OpenTelemetry Environment Variable Specification](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/)
verbatim so existing collector configuration is reused as-is.

| Env var | Effect |
|---|---|
| `OTEL_SERVICE_NAME` | Sets the `service.name` resource attribute. Default `"jig"`. |
| `OTEL_RESOURCE_ATTRIBUTES` | Comma-separated `k=v` list merged into the resource (e.g. `deployment.environment=ci`). |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP endpoint URL. Required when `OTEL_METRICS_EXPORTER=otlp` and/or `OTEL_TRACES_EXPORTER=otlp`. |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `grpc`, `http/protobuf`, or `http/json`. Default `grpc`. |
| `OTEL_EXPORTER_OTLP_HEADERS` | Comma-separated `k=v` header list; opaque to jig. |
| `OTEL_METRICS_EXPORTER` | `otlp`, `prometheus`, `console`, or `none`. Default `none`. |
| `OTEL_TRACES_EXPORTER` | `otlp`, `console`, or `none`. Default `none`. |
| `OTEL_METRIC_EXPORT_INTERVAL` | Milliseconds between OTLP metric pushes. SDK default (60 000). |
| `OTEL_METRIC_EXPORT_TIMEOUT` | Milliseconds allowed for one OTLP push. SDK default. |
| `OTEL_SDK_DISABLED` | Kill switch; forces the exporter off even if the workflow enables it. |

### jig-specific supplement

Used only where no OTel-standard equivalent exists.

| Env var | Effect |
|---|---|
| `JIG_TELEMETRY_MODE` | `off` / `prom` / `otlp` / `both`. Default `off`. Overrides `OTEL_METRICS_EXPORTER` / `OTEL_TRACES_EXPORTER` when set. |
| `JIG_TELEMETRY_PROMETHEUS_ADDR` | Bind address for the Prometheus listener (e.g. `127.0.0.1:9464`). Empty disables the listener even when `mode=prom`. |
| `JIG_TELEMETRY_PROMETHEUS_PATH` | HTTP path served by the Prometheus handler. Default `/metrics`. |

These names never overlap with `JIG_SECRET_*` (secret name resolution) or
with `JIG_INPUT_*` / `JIG_FANOUT_*` (command child inputs).

### `.jig/telemetry.json`

Peer of `.jig/tui.json`. Missing or corrupt files fall back to the zero
value (`mode: "off"`) so a broken prefs file never bricks the TUI or `jig
run`. Empty root (persistence-off) is a no-op for the writer.

```json
{
  "mode": "off",
  "otlp_endpoint": "",
  "prometheus_addr": "",
  "resource_attributes": {"env": "dev"}
}
```

### Workflow `[telemetry]` block

Optional top-level field on `Workflow`, parallel to `[meta]` and
`[defaults]`. Only fields that are meaningful per workflow belong here;
endpoint URLs and headers stay in env or `.jig/telemetry.json`.

```toml
[telemetry]
enabled = true                  # default false
metric_prefix = "jig"           # default; used for all metric names below
export_thinking_counts = false  # never export thought text; count only when true
resource_attributes = { workflow_owner = "platform" }
```

Load-time validation (`jig validate`):

- `metric_prefix` must match `[a-zA-Z][a-zA-Z0-9_]{0,31}` so it is a legal
  Prometheus / OTel metric name prefix.
- `resource_attributes` keys must be legal OTel attribute keys (dotted,
  lowercase, no spaces); values must be strings.
- `enabled = true` requires that env or prefs already selected an exporter
  target; the flag on its own does not spin up an exporter.
- Unknown fields fail load.

### Precedence summary

- `OTEL_SDK_DISABLED=true` → exporter off (always).
- Otherwise: workflow `[telemetry].enabled = false` → exporter off.
- Otherwise: `JIG_TELEMETRY_MODE` (env) → `.jig/telemetry.json` → derived
  mode from `OTEL_METRICS_EXPORTER` / `OTEL_TRACES_EXPORTER`, first
  non-empty wins.
- Workflow `[telemetry].metric_prefix` and `resource_attributes` are merged
  over env-derived defaults.

---

## Metric catalog

Every instrument is registered under the prefix set by
`[telemetry].metric_prefix` (default `jig`). Names are given without the
prefix; a Prometheus-scraped instance sees them as `jig_run_started_total`
after the OTel Prometheus exporter's naming translation.

### Label set

Every metric carries the same fixed label set unless noted:

| Label | Source | Notes |
|---|---|---|
| `workflow` | `Workflow.Meta.Name` | Redacted. |
| `step` | declared step ID | Never a runtime fan-out child ID. |
| `step_type` | `agent` / `command` / `check` / `review` | Empty for non-step metrics. |
| `backend` | resolved backend (`claude`, `cursor`, …) | Empty when unknown. |
| `transport` | `sdk` / `acp` | Empty for non-agent steps. |
| `model` | resolved model | Empty when unknown. |
| `outcome` | terminal metrics only | `succeeded` / `failed` / `skipped`. |

Resource attributes (`service.name`, operator-provided
`resource_attributes`) are applied once per process at the resource level.
Fan-out family/child IDs, session IDs, git SHAs, and tool call IDs are
**span attributes**, never metric labels — see [ADR 0012](adr/0012-observability-export.md)
for the cardinality argument.

### Instruments

| Instrument | Type | Unit | Notes |
|---|---|---|---|
| `run.started` | counter | `1` | `workflow` label. |
| `run.finished` | counter | `1` | Adds `outcome`. |
| `run.duration` | histogram | `s` | Wall clock between `RunStarted` and `RunFinished`. |
| `run.cost_usd` | histogram | `USD` | `RunSnapshot.TotalCostUSD` at finish. |
| `run.tokens` | histogram | `1` | `RunSnapshot.TotalTokens` at finish. |
| `step.started` | counter | `1` | Increments on `pending → running`. |
| `step.finished` | counter | `1` | Any terminal `StepStatus`; adds `outcome`. |
| `step.duration` | histogram | `s` | Between `StepStatus{To:running}` and terminal transition for one `(RunID, StepID, Attempt, Iteration, Generation)` tuple. |
| `step.cost_usd` | histogram | `USD` | Recorded only when `StepStatus.Cost != nil`, so "unreported" stays distinct from "reported zero". |
| `step.tokens` | histogram | `1` | `StepStatus.Tokens` on terminal transitions. |
| `step.attempts` | histogram | `1` | `StepStatus.Attempt + 1` on terminal transitions. |
| `step.iterations` | histogram | `1` | `StepStatus.Iteration + 1` on terminal transitions. |
| `step.generations` | histogram | `1` | `StepStatus.Generation` on terminal transitions. |
| `gate.result` | counter | `1` | `outcome` label (`passed` / `failed`). |
| `review.requested` | counter | `1` | Per-step gate open. |
| `review.submitted` | counter | `1` | Verdict submitted. |
| `review.wait_duration` | histogram | `s` | Between `ReviewRequest` and matching `ReviewSubmitted` for a `(RunID, StepID, RoundID)` tuple. |
| `recovery.requested` | counter | `1` | Adds `can_resume` (bool). |
| `integration.conflict` | counter | `1` | `IntegrationConflictRequest` events. |
| `final_merge.requested` | counter | `1` | `workflow` label. |
| `security.finding` | counter | `1` | Adds `tier`, `severity`, `action`. Never labels with the finding message. |
| `foreach.expanded` | counter | `1` | Adds `family` (declared step ID), `generation`. Value is child count. |
| `reset.applied` | counter | `1` | Adds `target_step`. |
| `tool.invoked` | counter | `1` | Adds `tool` (name only). |
| `tool.network_request` | counter | `1` | From the runner's `NetworkRequest` hook. |
| `exporter.dropped` | counter | `1` | Self-observability. Adds `kind` (event kind). |

The catalog is documentation-tested: `TestMetricCatalog` asserts every
registered instrument appears here and vice versa, so a new metric cannot
land without this table also being updated.

---

## Span catalog

Spans are emitted only when a trace exporter is enabled
(`OTEL_TRACES_EXPORTER != none` or `JIG_TELEMETRY_MODE` selects a mode that
includes traces). Metrics work without spans.

- `jig.step` — one span per step attempt, from `pending → running` to
  terminal transition. Attributes include the metric label set plus
  `attempt`, `iteration`, `generation`, `outcome`, and, when the step is a
  fan-out child, `foreach.family` and `foreach.instance`.
- `jig.tool` — one child span per `Reporter.ToolCall` observation.
  Attributes: `tool` (name only), `duration_ms`. Body is never attached.
- `jig.tool.network_request` — one child span per runner-observed network
  request. Attributes: `backend`.

Trace context is not propagated **into** agents in the first delivery
(vendor-specific; belongs to a later spec). Span attributes come from the
same fixed allow-list metrics use.

---

## Security

The full posture is fixed by [ADR 0012](adr/0012-observability-export.md).
Highlights:

- **Off by default.** A fresh install ships no exporter, no listener, no
  outbound traffic.
- **`127.0.0.1` by default.** Blank `JIG_TELEMETRY_PROMETHEUS_ADDR` disables
  the Prometheus listener entirely; a `0.0.0.0` bind is possible but
  requires the operator to type it explicitly.
- **Redaction is unconditional.** Every label value flows through
  `sentinel.Redact` before recording, so the transcript's on-disk redaction
  policy applies identically to what leaves the process.
- **`OTEL_EXPORTER_OTLP_HEADERS` is opaque.** jig passes it through to the
  OTel SDK and never reads, logs, or serialises it. `jig doctor` will not
  reveal its value.
- **Bounded cardinality.** Fan-out and reset produce counters; per-child
  and per-round provenance stays on spans, never on metric labels.

---

## Failure handling

- **OTLP dial failure.** One warning to stderr; provider downgrades to
  no-op; `jig run` exit code is unaffected.
- **Prometheus bind failure.** One warning to stderr; listener is not
  started; other exporters (if any) continue.
- **Backpressure.** `EventExporter` drains the manager bus on its own
  goroutine and never applies backpressure. When it cannot keep up it drops
  events and increments `exporter.dropped` so the loss is visible.
- **Shutdown.** `Provider.Shutdown(ctx)` is idempotent, flushes buffered
  measurements, stops each exporter, and closes the Prometheus listener.
  Deferred in both TUI and headless entry points.

---

## Persistence-off

`NewManager(exec, "")` remains valid. When telemetry is enabled through env
in a persistence-off engine test, the exporter still initialises and emits
run/step counters, but skips measurements that would require a run
directory (no manifest reads, no journal replay). `.jig/telemetry.json`
writers no-op on an empty root, matching the `internal/tui/prefs`
precedent.

---

## Related

- [ADR 0012 — Observability export is opt-in, in-machine by default, redacted always](adr/0012-observability-export.md)
- [Plan — A18 OTel/Prometheus/OTLP export](plans/a18-otel-prometheus-export.md)
- [ADR 0002 — Gates are non-blocking focus regions](adr/0002-gates-are-nonblocking-focus-regions.md) (the same subscriber pattern telemetry follows)
- [Agent security monitoring](security-monitoring.md) (redaction rules the exporter reuses)
- [Headless contract](headless.md) (exit codes never change under telemetry)
- [Open goals — A18](plans/open-goals.md)
