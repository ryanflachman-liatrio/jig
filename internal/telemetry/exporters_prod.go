package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// newProdMetricReaders is the Phase 5 real implementation: it constructs the
// sdkmetric.Reader set (OTLP push, Prometheus pull, or stdout) matching
// p.cfg.Mode / p.cfg.MetricsExporter. Each reader failure downgrades to a
// noop via cfg.warn — never fatal — so a mistyped endpoint cannot crash
// jig run.
//
// The second return is a slice of shutdown closers for out-of-band resources
// (Prometheus HTTP server) that OTel's Reader.Shutdown does not own.
func (p *Provider) newProdMetricReaders(ctx context.Context) ([]sdkmetric.Reader, []func(context.Context) error, error) {
	if p == nil {
		return nil, nil, nil
	}

	var readers []sdkmetric.Reader
	var closers []func(context.Context) error

	// OTLP metric exporter. Skip when the endpoint is empty — that state
	// already triggered a startup warning via warnIfIncomplete, and dialing
	// grpc://"" spends the gRPC default timeout before failing.
	if wantsOTLPMetrics(p.cfg) && p.cfg.OTLPEndpoint != "" {
		exp, err := newOTLPMetricExporter(ctx, p.cfg)
		if err != nil {
			p.cfg.warn(fmt.Errorf("telemetry: otlp metric exporter: %w", err))
		} else {
			readers = append(readers, sdkmetric.NewPeriodicReader(exp))
		}
	}

	// stdout metric exporter (OTEL_METRICS_EXPORTER=console).
	if p.cfg.MetricsExporter == "console" {
		exp, err := stdoutmetric.New(stdoutmetric.WithWriter(os.Stdout))
		if err != nil {
			p.cfg.warn(fmt.Errorf("telemetry: stdout metric exporter: %w", err))
		} else {
			readers = append(readers, sdkmetric.NewPeriodicReader(exp))
		}
	}

	// Prometheus pull exporter + HTTP server.
	if wantsPrometheus(p.cfg) && p.cfg.PrometheusAddr != "" {
		reg := prometheus.NewRegistry()
		promReader, err := otelprom.New(otelprom.WithRegisterer(reg))
		if err != nil {
			p.cfg.warn(fmt.Errorf("telemetry: prometheus exporter: %w", err))
		} else {
			readers = append(readers, promReader)
			srvClose, err := startPromServer(ctx, p, reg)
			if err != nil {
				p.cfg.warn(fmt.Errorf("telemetry: prometheus listener: %w", err))
			} else {
				closers = append(closers, srvClose)
			}
		}
	}

	return readers, closers, nil
}

// newProdSpanProcessors builds trace exporters matching cfg.TracesExporter.
// OTLP is the primary transport; stdout is an operator-debug fallback.
func (p *Provider) newProdSpanProcessors(ctx context.Context) ([]sdktrace.SpanProcessor, error) {
	if p == nil {
		return nil, nil
	}
	var processors []sdktrace.SpanProcessor

	if wantsOTLPTraces(p.cfg) && p.cfg.OTLPEndpoint != "" {
		exp, err := newOTLPTraceExporter(ctx, p.cfg)
		if err != nil {
			p.cfg.warn(fmt.Errorf("telemetry: otlp trace exporter: %w", err))
		} else {
			processors = append(processors, sdktrace.NewBatchSpanProcessor(exp))
		}
	}
	if p.cfg.TracesExporter == "console" {
		exp, err := stdouttrace.New(stdouttrace.WithWriter(os.Stdout))
		if err != nil {
			p.cfg.warn(fmt.Errorf("telemetry: stdout trace exporter: %w", err))
		} else {
			processors = append(processors, sdktrace.NewBatchSpanProcessor(exp))
		}
	}
	return processors, nil
}

// wantsOTLPMetrics reports whether the OTLP metric exporter should be
// installed. Either MetricsExporter == "otlp" or ModeOTLP/Both selects it.
func wantsOTLPMetrics(cfg Config) bool {
	if cfg.MetricsExporter == "otlp" {
		return true
	}
	if cfg.MetricsExporter != "" && cfg.MetricsExporter != "prometheus" && cfg.MetricsExporter != "console" && cfg.MetricsExporter != "none" {
		return false
	}
	return cfg.Mode == ModeOTLP || cfg.Mode == ModeBoth
}

// wantsOTLPTraces reports whether the OTLP trace exporter should be installed.
func wantsOTLPTraces(cfg Config) bool {
	switch cfg.TracesExporter {
	case "otlp":
		return true
	case "none", "console":
		return false
	case "":
		return cfg.Mode == ModeOTLP || cfg.Mode == ModeBoth
	}
	return false
}

// wantsPrometheus reports whether the Prometheus pull exporter should be
// installed. Requires either MetricsExporter == "prometheus" or ModeProm/Both.
func wantsPrometheus(cfg Config) bool {
	if cfg.MetricsExporter == "prometheus" {
		return true
	}
	if cfg.MetricsExporter != "" && cfg.MetricsExporter != "otlp" && cfg.MetricsExporter != "console" && cfg.MetricsExporter != "none" {
		return false
	}
	return cfg.Mode == ModeProm || cfg.Mode == ModeBoth
}

// newOTLPMetricExporter picks HTTP or gRPC based on OTEL_EXPORTER_OTLP_PROTOCOL.
// Endpoint / headers / insecure come from cfg, so operators can point OTLP at
// any collector (Grafana Alloy, Honeycomb, Datadog OTLP, …) via env alone.
func newOTLPMetricExporter(ctx context.Context, cfg Config) (sdkmetric.Exporter, error) {
	switch cfg.OTLPProtocol {
	case "http/protobuf", "http/json", "http":
		opts := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpointURL(cfg.OTLPEndpoint),
		}
		if len(cfg.OTLPHeaders) > 0 {
			opts = append(opts, otlpmetrichttp.WithHeaders(cfg.OTLPHeaders))
		}
		if cfg.OTLPInsecure {
			opts = append(opts, otlpmetrichttp.WithInsecure())
		}
		return otlpmetrichttp.New(ctx, opts...)
	case "grpc", "":
		opts := []otlpmetricgrpc.Option{
			otlpmetricgrpc.WithEndpointURL(cfg.OTLPEndpoint),
		}
		if len(cfg.OTLPHeaders) > 0 {
			opts = append(opts, otlpmetricgrpc.WithHeaders(cfg.OTLPHeaders))
		}
		if cfg.OTLPInsecure {
			opts = append(opts, otlpmetricgrpc.WithInsecure())
		}
		return otlpmetricgrpc.New(ctx, opts...)
	}
	return nil, fmt.Errorf("unsupported OTEL_EXPORTER_OTLP_PROTOCOL %q", cfg.OTLPProtocol)
}

// newOTLPTraceExporter mirrors newOTLPMetricExporter for spans.
func newOTLPTraceExporter(ctx context.Context, cfg Config) (sdktrace.SpanExporter, error) {
	switch cfg.OTLPProtocol {
	case "http/protobuf", "http/json", "http":
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpointURL(cfg.OTLPEndpoint),
		}
		if len(cfg.OTLPHeaders) > 0 {
			opts = append(opts, otlptracehttp.WithHeaders(cfg.OTLPHeaders))
		}
		if cfg.OTLPInsecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		return otlptracehttp.New(ctx, opts...)
	case "grpc", "":
		opts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpointURL(cfg.OTLPEndpoint),
		}
		if len(cfg.OTLPHeaders) > 0 {
			opts = append(opts, otlptracegrpc.WithHeaders(cfg.OTLPHeaders))
		}
		if cfg.OTLPInsecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		return otlptrace.New(ctx, otlptracegrpc.NewClient(opts...))
	}
	return nil, fmt.Errorf("unsupported OTEL_EXPORTER_OTLP_PROTOCOL %q", cfg.OTLPProtocol)
}

// startPromServer starts a minimal net/http server bound to cfg.PrometheusAddr
// serving /metrics via the OTel Prometheus exporter's registry. When the
// caller binds to :0 the resolved port is written back to p.cfg.PrometheusAddr
// so tests can discover it.
//
// The server uses ReadHeaderTimeout and IdleTimeout so an idle scrape client
// cannot leak file descriptors, and its Shutdown honors the ctx passed to
// Provider.Shutdown.
func startPromServer(ctx context.Context, p *Provider, reg *prometheus.Registry) (func(context.Context) error, error) {
	mux := http.NewServeMux()
	mux.Handle(p.cfg.PrometheusPath, promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))

	ln, err := net.Listen("tcp", p.cfg.PrometheusAddr)
	if err != nil {
		return nil, err
	}
	// Reflect the actual bind address so tests using ":0" can find the port.
	p.mu.Lock()
	p.cfg.PrometheusAddr = ln.Addr().String()
	p.mu.Unlock()

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			p.cfg.warn(fmt.Errorf("telemetry: prometheus server: %w", err))
		}
	}()

	_ = ctx // reserved for future readiness hooks
	return func(shutdownCtx context.Context) error {
		return srv.Shutdown(shutdownCtx)
	}, nil
}
