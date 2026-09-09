package telemetry

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestInitOffReturnsNoopProvider(t *testing.T) {
	p, err := Init(context.Background(), Config{})
	if err != nil {
		t.Fatalf("Init(zero): %v", err)
	}
	if p == nil {
		t.Fatal("Init returned nil Provider")
	}
	if p.Mode() != ModeOff {
		t.Errorf("mode = %q, want %q", p.Mode(), ModeOff)
	}
	if got := p.Meter("test"); got == nil {
		t.Error("Meter returned nil")
	}
	if got := p.Tracer("test"); got == nil {
		t.Error("Tracer returned nil")
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestShutdownIsIdempotent(t *testing.T) {
	p, err := Init(context.Background(), Config{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := p.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown %d: %v", i, err)
		}
	}
}

func TestNilProviderMethodsAreSafe(t *testing.T) {
	var p *Provider
	if p.Mode() != ModeOff {
		t.Errorf("nil Provider Mode() = %q, want %q", p.Mode(), ModeOff)
	}
	if p.PrometheusAddr() != "" {
		t.Error("nil Provider PrometheusAddr() should be empty")
	}
	if got := p.Meter("test"); got == nil {
		t.Error("nil Provider Meter returned nil")
	}
	if got := p.Tracer("test"); got == nil {
		t.Error("nil Provider Tracer returned nil")
	}
	if p.Resource() == nil {
		t.Error("nil Provider Resource() returned nil")
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("nil Provider Shutdown: %v", err)
	}
}

func TestInitPromWithoutBindDowngradesToNoop(t *testing.T) {
	// Phase 1: newProdMetricReaders returns nothing, so installMeter builds an
	// empty MeterProvider. Phase 5 will actually stand up the Prometheus
	// listener. The important Phase-1 contract is that Init never panics and
	// never returns nil for an unimplemented exporter.
	var warned error
	p, err := Init(context.Background(), Config{
		Mode: ModeProm,
		OnWarn: func(e error) { warned = e },
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if p == nil {
		t.Fatal("Init returned nil")
	}
	if warned == nil {
		// Prom mode with an empty bind address should have warned via
		// validateResolved's OnWarn dispatch.
		t.Error("expected warning for empty PrometheusAddr under ModeProm")
	}
}

func TestConfigWithDefaults(t *testing.T) {
	c := Config{}.withDefaults()
	if c.ServiceName != "jig" {
		t.Errorf("ServiceName = %q, want %q", c.ServiceName, "jig")
	}
	if c.MetricPrefix != "jig" {
		t.Errorf("MetricPrefix = %q, want %q", c.MetricPrefix, "jig")
	}
	if c.PrometheusPath != "/metrics" {
		t.Errorf("PrometheusPath = %q, want %q", c.PrometheusPath, "/metrics")
	}
}

func TestWarnDispatch(t *testing.T) {
	var got error
	c := Config{OnWarn: func(e error) { got = e }}
	c.warn(errors.New("boom"))
	if got == nil || !strings.Contains(got.Error(), "boom") {
		t.Errorf("warn: got %v", got)
	}
	// Nil error must be a no-op.
	got = nil
	c.warn(nil)
	if got != nil {
		t.Errorf("warn(nil) called OnWarn: %v", got)
	}
}
