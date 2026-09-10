# Observability export is opt-in, in-machine by default, redacted always

Status: accepted.

jig's optional OpenTelemetry exporter — Prometheus scrape, OTLP push, and
console — is the first process surface that leaves the operator's machine
under jig's own agency. The three decisions in this ADR fix the posture so
future changes cannot silently widen the export surface. The plan lives at
[`docs/plans/a18-otel-prometheus-export.md`](../plans/a18-otel-prometheus-export.md).

## Decision 1 — The exporter is off by default; enabling it requires an explicit signal

`internal/telemetry.Init` builds a no-op `Provider` whenever the resolved
`Config.Mode` is `off`. A no-op provider hands out OTel no-op Meter and Tracer
implementations, binds no listener, dials no endpoint, and starts no goroutine.
An existing install behaves byte-identically after upgrade: no exporter, no
listener, no outbound traffic, no change to `jig run` exit codes.

Enabling requires one of the following:

- `OTEL_METRICS_EXPORTER` or `OTEL_TRACES_EXPORTER` is set to something other
  than `none`.
- `JIG_TELEMETRY_MODE` is set to `prom`, `otlp`, or `both`.
- `.jig/telemetry.json` records a non-`off` mode.
- The workflow `[telemetry]` block sets `enabled = true` *and* the operator has
  already selected an exporter target through env or prefs; the enabled flag
  cannot spin up an exporter on its own.

`OTEL_SDK_DISABLED=true` is the process-wide kill switch and wins over every
other source: a compromised or hostile workflow cannot re-enable the exporter
when the operator has disabled it.

### Why explicit opt-in

Off-machine egress from a developer tool is a real posture change. Making it
opt-in — and requiring the operator to make one deliberate configuration
choice — keeps the trust model simple: a fresh checkout of jig does not
publish anything to anyone, ever. Auto-enabling based on the presence of
`OTEL_EXPORTER_OTLP_ENDPOINT` would let a shared shell profile silently ship
telemetry from every user's runs.

### Rejected alternative — infer intent from any `OTEL_*` variable

A permissive reading of the OTel SDK spec would treat any `OTEL_*` variable
as an opt-in signal. jig deliberately does not: it distinguishes "OpenTelemetry
SDK is available in this environment" from "this user wants jig to publish". The
former is common in CI runners with a collector sidecar; the latter is a
consent decision that belongs to the operator, not to the environment.

---

## Decision 2 — Bounded metric cardinality; unbounded provenance stays on spans

Every metric jig emits carries the same fixed label set:

- `workflow` — `Workflow.Meta.Name`.
- `step` — declared step ID (never a runtime fan-out child ID).
- `step_type`, `backend`, `transport`, `model` — resolved values.
- `outcome` — terminal outcome (`succeeded` / `failed` / `skipped`).
- Optional operator-provided `resource_attributes` from
  `[telemetry].resource_attributes`, applied at the resource level.

Fan-out family/child IDs, session IDs, git SHAs, tool call IDs, review round
IDs, and every other unbounded identifier are recorded as *span attributes*,
not as metric labels. This is the difference between "jig steps that ran"
(bounded by the workflow) and "one specific execution of one specific fan-out
child" (unbounded by definition).

### Why cardinality is a hard rule

Time-series backends amortize storage per unique label combination. A
population of runs with unique fan-out child IDs on a metric label would
create one series per child per metric — millions of series per week for even
a modest population. The bounded set above keeps jig's metric surface
predictable in cost and legible in dashboards, and it prevents an ill-behaved
workflow from filling a shared Prometheus tenant.

The trade-off is that per-child observability requires a trace backend that
indexes span attributes. Every mature APM does. The cardinality trade-off is
worth the transient friction of teaching operators to look at spans for
child-level questions.

### Rejected alternative — allow-list metric labels via workflow config

Letting workflows add labels ("we care about `owner`, `team`, `env`") sounds
harmless but is a footgun: operators inevitably end up adding fields that vary
per request (session IDs, request IDs, "just this once" debugging tags). The
existing `resource_attributes` field on `[telemetry]` covers workflow-level
metadata (which does not multiply the series count) and is a strictly better
home than per-metric labels.

---

## Decision 3 — Redaction is unconditional and secret patterns never appear on the wire

Every label value derived from step content — `workflow` name, step ID,
resolved `model`, `backend`, `transport`, gate rule name, security tier /
severity, and any operator-provided resource attribute — flows through the
same redaction seams jig already uses on disk:

- `sentinel.Redact` for secret-shaped strings (AWS keys, PEM markers, GitHub
  tokens, high-entropy fragments) — same rules that gate what enters
  `transcript.jsonl` and `findings.jsonl`.
- The same fixed allow-list that keeps step content, prompts, tool inputs,
  tool outputs, and command environment out of labels *and* out of span
  attributes.

`OTEL_EXPORTER_OTLP_HEADERS` is passed through to the OTel SDK as an opaque
string and is never read, logged, or serialized by jig itself.

The transcript file remains the durable content record: nothing that is not
already in `transcript.jsonl` (post-redaction) can escape via telemetry.

### Why redaction is not opt-out

The set of things an operator might not want to publish (customer names in
step IDs, project codenames in workflow names, API keys accidentally pasted
into review draft text) is larger and more dynamic than what any allow-list
can enumerate. Redacting unconditionally, and running every label through the
same regex pack that already protects the transcript, keeps the export
posture consistent with the on-disk posture — the export can never be more
permissive than what jig would already write to `.jig/`.

### Rejected alternative — trust the source and redact only labels flagged as sensitive

A per-field "this is sensitive" flag would require every future contributor
to remember to mark the new field. That is exactly the class of decision that
belongs behind a default-safe seam, not on a checklist. Blanket redaction is
cheap (a bounded regex scan on strings under 256 bytes) and it survives
refactors that the flag approach would not.

---

## Consequences

- **The listener is 127.0.0.1 by default.** A blank `JIG_TELEMETRY_PROMETHEUS_ADDR`
  disables the Prometheus listener entirely; a `0.0.0.0` bind is possible but
  requires the operator to type it explicitly and is documented as a step
  outside the default posture. The server also carries an explicit
  `ReadHeaderTimeout` and idle timeout, and its `Shutdown(ctx)` is tied to
  `Provider.Shutdown` so a clean process exit cleanly closes the listener.
- **Provider errors are logged once, not fatal.** OTLP dial failure,
  Prometheus bind failure, or exporter shutdown timeout writes one line to
  stderr via `Config.OnWarn` and continues with a no-op provider. `jig run`
  exit codes are unaffected by exporter failures.
- **Persistence-off remains valid.** With `NewManager(exec, "")`, the exporter
  still initialises from env if requested, but only measurements that do not
  require a run directory (no manifest reads, no journal replay) are emitted.
  `.jig/telemetry.json` writers no-op on an empty root, matching the
  `internal/tui/prefs` precedent.
- **The scheduler owns the hot path.** `EventExporter.Attach` drains the
  live/ctrl channel pair on a dedicated goroutine that never applies
  backpressure to the scheduler. When the exporter cannot keep up it drops
  events and increments `jig.exporter.dropped`, mirroring the drop-on-full
  contract Manager already advertises.
- **The metric catalog is documentation-tested.** `docs/observability.md` lists
  every registered instrument. A test asserts that no metric name appears in
  code without appearing in the doc and vice versa, so future changes cannot
  quietly widen the export surface.
- **`JIG_TELEMETRY_*` and `OTEL_*` never overlap with `JIG_SECRET_*` /
  `JIG_INPUT_*` / `JIG_FANOUT_*`.** Secret resolution and command child env
  contracts stay untouched.
