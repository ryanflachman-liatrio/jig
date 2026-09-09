package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Config is the resolved observability configuration for one Provider. It is
// deliberately flat so callers can construct it directly (tests) or through
// [ResolveConfig] (production).
type Config struct {
	// Mode selects the exporter surface. ModeOff (zero value) disables every
	// exporter and returns no-op providers from [Init].
	Mode Mode

	// ServiceName populates the service.name resource attribute. Empty falls
	// back to the package default ("jig").
	ServiceName string

	// ResourceAttributes are merged into the resource. Keys must be legal OTel
	// attribute keys; values are recorded as strings.
	ResourceAttributes map[string]string

	// MetricPrefix is prepended to every registered instrument (see
	// docs/observability.md metric catalog). Empty falls back to "jig".
	MetricPrefix string

	// ExportThinkingCounts, when true, records a counter each time an
	// assistant "thinking" delta arrives so operators can graph CoT volume.
	// Content is never exported — count only. Default false because CoT
	// volume can be sensitive.
	ExportThinkingCounts bool

	// OTLPEndpoint / OTLPProtocol / OTLPHeaders mirror the OTel-standard
	// env vars. Empty endpoint disables the OTLP exporter even when Mode is
	// ModeOTLP / ModeBoth (a warning is logged; Provider downgrades cleanly).
	OTLPEndpoint string
	OTLPProtocol string // "grpc", "http/protobuf", "http/json"
	OTLPHeaders  map[string]string
	OTLPInsecure bool // set from OTEL_EXPORTER_OTLP_INSECURE

	// MetricsExporter / TracesExporter follow OTel spec values: otlp,
	// prometheus (metrics only), console (stdout), none.
	MetricsExporter string
	TracesExporter  string

	// PrometheusAddr binds the /metrics listener. Empty disables the
	// listener even when Mode selects Prometheus. Default listener path is
	// PrometheusPath ("/metrics").
	PrometheusAddr string
	PrometheusPath string

	// OnWarn is called for non-fatal exporter installation errors so
	// operators see one line explaining why a requested exporter downgraded
	// to noop. Missing OnWarn falls back to writing to stderr.
	OnWarn func(error)
}

// withDefaults returns a copy of cfg with unset fields replaced by package
// defaults. Callers do not mutate the returned copy — it is used inside
// [Init] and tests.
func (c Config) withDefaults() Config {
	out := c
	if out.ServiceName == "" {
		out.ServiceName = serviceName
	}
	if out.MetricPrefix == "" {
		out.MetricPrefix = defaultMetricPrefix
	}
	if out.PrometheusPath == "" {
		out.PrometheusPath = "/metrics"
	}
	switch out.Mode {
	case ModeOff, ModeProm, ModeOTLP, ModeBoth:
	default:
		// Anything unset or unknown collapses to ModeOff. Keeps Init's
		// "off by default" contract honest — a caller that constructs a
		// Config{} directly gets a noop provider, not an error.
		out.Mode = ModeOff
	}
	return out
}

// warnIfIncomplete surfaces one warning per exporter surface that is selected
// but not fully configured. Called from [Init] so a direct Config caller sees
// the same "empty bind disables the listener" hint that [ResolveConfig] emits.
func (c Config) warnIfIncomplete() {
	if c.Mode == ModeProm || c.Mode == ModeBoth {
		if c.PrometheusAddr == "" {
			c.warn(errors.New("telemetry: prometheus mode selected but JIG_TELEMETRY_PROMETHEUS_ADDR is empty; listener disabled"))
		}
	}
	if c.Mode == ModeOTLP || c.Mode == ModeBoth {
		if c.OTLPEndpoint == "" {
			c.warn(errors.New("telemetry: otlp mode selected but OTEL_EXPORTER_OTLP_ENDPOINT is empty; exporter disabled"))
		}
	}
}

// warn dispatches a non-fatal exporter installation error to OnWarn (or, if
// unset, to stderr). Never panics on a nil error so call sites stay small.
func (c Config) warn(err error) {
	if err == nil {
		return
	}
	if c.OnWarn != nil {
		c.OnWarn(err)
		return
	}
	fmt.Fprintln(os.Stderr, err.Error())
}

const defaultMetricPrefix = "jig"

// metricPrefixRe is the exact contract documented in
// docs/plans/a18-otel-prometheus-export.md ("Configuration surface").
// Rejects prefixes that would produce illegal Prometheus / OTel metric names.
var metricPrefixRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,31}$`)

// resourceAttrKeyRe accepts dotted, lowercase, snake-friendly attribute keys.
// Matches the OTel semantic-conventions style ("service.name",
// "deployment.environment").
var resourceAttrKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*$`)

// ValidateMetricPrefix reports whether prefix is a legal instrument prefix.
// Exported for [internal/workflow] load-time validation.
func ValidateMetricPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if !metricPrefixRe.MatchString(prefix) {
		return fmt.Errorf("metric_prefix %q must match %s", prefix, metricPrefixRe.String())
	}
	return nil
}

// ValidateResourceAttrKey reports whether key is a legal OTel attribute key.
// Exported for load-time validation.
func ValidateResourceAttrKey(key string) error {
	if key == "" {
		return errors.New("resource attribute key is empty")
	}
	if !resourceAttrKeyRe.MatchString(key) {
		return fmt.Errorf("resource attribute key %q must match %s", key, resourceAttrKeyRe.String())
	}
	return nil
}

// EnvLookup is the interface [ResolveConfig] uses to read env vars. The
// default (os.LookupEnv) is production; tests inject a map-backed lookup so
// they can exercise every env-var combination without touching process state.
type EnvLookup func(key string) (string, bool)

// OSEnv reads from process env; the production EnvLookup.
func OSEnv() EnvLookup { return os.LookupEnv }

// MapEnv adapts a map[string]string for tests.
func MapEnv(m map[string]string) EnvLookup {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

// Prefs is the shape of .jig/telemetry.json. Mirrors internal/tui/prefs style:
// missing file → zero value; corrupt file → zero value with a warning; empty
// jigRoot → no-op reads/writes.
//
// Fields intentionally use the same names as [Config] so operators can
// map them mentally 1:1.
type Prefs struct {
	Mode                 string            `json:"mode,omitempty"`
	OTLPEndpoint         string            `json:"otlp_endpoint,omitempty"`
	OTLPProtocol         string            `json:"otlp_protocol,omitempty"`
	OTLPHeaders          map[string]string `json:"otlp_headers,omitempty"`
	OTLPInsecure         bool              `json:"otlp_insecure,omitempty"`
	PrometheusAddr       string            `json:"prometheus_addr,omitempty"`
	PrometheusPath       string            `json:"prometheus_path,omitempty"`
	MetricsExporter      string            `json:"metrics_exporter,omitempty"`
	TracesExporter       string            `json:"traces_exporter,omitempty"`
	ResourceAttributes   map[string]string `json:"resource_attributes,omitempty"`
	MetricPrefix         string            `json:"metric_prefix,omitempty"`
	ServiceName          string            `json:"service_name,omitempty"`
	ExportThinkingCounts bool              `json:"export_thinking_counts,omitempty"`
}

// TelemetryFields carries the subset of workflow.Workflow.Telemetry that
// [ResolveConfig] consumes. Passed as its own type so this package does not
// import workflow (breaking a possible import cycle) — cmd/jig adapts.
type TelemetryFields struct {
	Enabled              bool
	MetricPrefix         string
	ExportThinkingCounts bool
	ResourceAttributes   map[string]string
}

// ResolveConfig folds env vars, .jig/telemetry.json prefs, and per-workflow
// [telemetry] settings into one Config. Precedence (rightmost wins):
//
//	 [telemetry] workflow ← prefs (.jig/telemetry.json) ← env (OTEL_*, JIG_TELEMETRY_*)
//	 with OTEL_SDK_DISABLED as an unconditional kill switch.
//
// Callers pass an [EnvLookup] rather than reading os.Getenv directly so tests
// stay hermetic.
func ResolveConfig(env EnvLookup, prefs Prefs, tel TelemetryFields) (Config, error) {
	if env == nil {
		env = OSEnv()
	}

	cfg := Config{
		ResourceAttributes: map[string]string{},
		OTLPHeaders:        map[string]string{},
	}

	// Layer 1: workflow [telemetry]. This layer never sets Mode — a workflow's
	// enabled=true is an opt-in signal, but the actual exporter target must
	// come from env or prefs so the operator (not the workflow author) picks
	// the endpoint. validateResolved below enforces that pairing.
	if tel.MetricPrefix != "" {
		cfg.MetricPrefix = tel.MetricPrefix
	}
	cfg.ExportThinkingCounts = tel.ExportThinkingCounts
	for k, v := range tel.ResourceAttributes {
		cfg.ResourceAttributes[k] = v
	}

	// Layer 2: prefs.
	if prefs.Mode != "" {
		cfg.Mode = Mode(strings.ToLower(strings.TrimSpace(prefs.Mode)))
	}
	if prefs.OTLPEndpoint != "" {
		cfg.OTLPEndpoint = prefs.OTLPEndpoint
	}
	if prefs.OTLPProtocol != "" {
		cfg.OTLPProtocol = prefs.OTLPProtocol
	}
	for k, v := range prefs.OTLPHeaders {
		cfg.OTLPHeaders[k] = v
	}
	if prefs.OTLPInsecure {
		cfg.OTLPInsecure = true
	}
	if prefs.PrometheusAddr != "" {
		cfg.PrometheusAddr = prefs.PrometheusAddr
	}
	if prefs.PrometheusPath != "" {
		cfg.PrometheusPath = prefs.PrometheusPath
	}
	if prefs.MetricsExporter != "" {
		cfg.MetricsExporter = prefs.MetricsExporter
	}
	if prefs.TracesExporter != "" {
		cfg.TracesExporter = prefs.TracesExporter
	}
	for k, v := range prefs.ResourceAttributes {
		cfg.ResourceAttributes[k] = v
	}
	if prefs.MetricPrefix != "" {
		cfg.MetricPrefix = prefs.MetricPrefix
	}
	if prefs.ServiceName != "" {
		cfg.ServiceName = prefs.ServiceName
	}
	if prefs.ExportThinkingCounts {
		cfg.ExportThinkingCounts = true
	}

	// Layer 3: env (highest precedence except for kill switch below).
	if v, ok := env("OTEL_SERVICE_NAME"); ok && strings.TrimSpace(v) != "" {
		cfg.ServiceName = strings.TrimSpace(v)
	}
	if v, ok := env("OTEL_RESOURCE_ATTRIBUTES"); ok {
		for k, val := range parseKVList(v) {
			cfg.ResourceAttributes[k] = val
		}
	}
	if v, ok := env("OTEL_EXPORTER_OTLP_ENDPOINT"); ok && strings.TrimSpace(v) != "" {
		cfg.OTLPEndpoint = strings.TrimSpace(v)
	}
	if v, ok := env("OTEL_EXPORTER_OTLP_PROTOCOL"); ok && strings.TrimSpace(v) != "" {
		cfg.OTLPProtocol = strings.TrimSpace(v)
	}
	if v, ok := env("OTEL_EXPORTER_OTLP_HEADERS"); ok {
		for k, val := range parseKVList(v) {
			cfg.OTLPHeaders[k] = val
		}
	}
	if v, ok := env("OTEL_EXPORTER_OTLP_INSECURE"); ok {
		cfg.OTLPInsecure = parseBool(v)
	}
	if v, ok := env("OTEL_METRICS_EXPORTER"); ok && strings.TrimSpace(v) != "" {
		cfg.MetricsExporter = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := env("OTEL_TRACES_EXPORTER"); ok && strings.TrimSpace(v) != "" {
		cfg.TracesExporter = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := env("JIG_TELEMETRY_PROMETHEUS_ADDR"); ok {
		cfg.PrometheusAddr = strings.TrimSpace(v)
	}
	if v, ok := env("JIG_TELEMETRY_PROMETHEUS_PATH"); ok && strings.TrimSpace(v) != "" {
		cfg.PrometheusPath = strings.TrimSpace(v)
	}
	if v, ok := env("JIG_TELEMETRY_MODE"); ok && strings.TrimSpace(v) != "" {
		cfg.Mode = Mode(strings.ToLower(strings.TrimSpace(v)))
	}

	// Derive Mode from OTEL_*_EXPORTER when JIG_TELEMETRY_MODE and workflow
	// [telemetry].enabled did not set one. This keeps operators who use only
	// the OTel-standard vars from having to also set a jig-specific mode.
	if cfg.Mode == "" || cfg.Mode == ModeOff {
		cfg.Mode = deriveMode(cfg)
	}

	// Kill switch: overrides every other source.
	if v, ok := env("OTEL_SDK_DISABLED"); ok && parseBool(v) {
		cfg.Mode = ModeOff
	}

	// Normalize inputs so callers see a consistent Mode. Invalid mode names
	// become ModeOff so a typo never activates an exporter surprise.
	switch cfg.Mode {
	case ModeOff, ModeProm, ModeOTLP, ModeBoth:
	default:
		cfg.Mode = ModeOff
	}

	if err := validateResolved(cfg, tel); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// validateResolved checks the final Config once every source has been folded.
// It fails when a workflow explicitly enables telemetry but neither env nor
// prefs configured an exporter target — the operator asked for observability
// and would otherwise get silence.
func validateResolved(cfg Config, tel TelemetryFields) error {
	if err := ValidateMetricPrefix(cfg.MetricPrefix); err != nil {
		return err
	}
	for k := range cfg.ResourceAttributes {
		if err := ValidateResourceAttrKey(k); err != nil {
			return err
		}
	}
	if tel.Enabled && cfg.Mode == ModeOff {
		return fmt.Errorf("telemetry: [telemetry].enabled = true but no exporter configured " +
			"(set OTEL_METRICS_EXPORTER, OTEL_TRACES_EXPORTER, JIG_TELEMETRY_MODE, or a .jig/telemetry.json mode)")
	}
	// Not fatal — matches the "empty bind disables listener" contract in
	// the plan. Log a warning so the operator notices at run start.
	cfg.warnIfIncomplete()
	return nil
}

// deriveMode maps OTel-standard exporter env vars to a jig Mode when the
// operator did not set JIG_TELEMETRY_MODE.
func deriveMode(cfg Config) Mode {
	m, t := cfg.MetricsExporter, cfg.TracesExporter
	prom := m == "prometheus"
	otlp := m == "otlp" || t == "otlp"
	switch {
	case prom && otlp:
		return ModeBoth
	case prom:
		return ModeProm
	case otlp:
		return ModeOTLP
	}
	return ModeOff
}

// parseKVList parses an OTel-style comma-separated `key=value` list. Trims
// whitespace and ignores empty segments so headers pasted from
// docs stay parseable. Duplicate keys keep the last value (matching OTel SDK).
func parseKVList(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(part[:eq])
		v := strings.TrimSpace(part[eq+1:])
		if k == "" {
			continue
		}
		out[k] = v
	}
	return out
}

// parseBool accepts "1"/"true"/"yes"/"on" (any case) as true. Every other
// value — including "" — is false. Matches how the OTel SDK reads boolean
// env vars.
func parseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// SortedKeys returns the keys of m in stable order. Used by tests and by
// debug rendering; exported so callers do not re-implement it.
func SortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// nowContext is a helper for tests to inject a deadline-aware context.
// Reserved for Phase 3+ where the exporter needs a cancellable context.
func nowContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
