package telemetry

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"jig/internal/engine"
)

// TestPrometheusListenerServesMetricsFamilies boots a real Provider with
// ModeProm bound to 127.0.0.1:0, records a run + step through EventExporter,
// then curls /metrics and asserts every documented metric family is present.
func TestPrometheusListenerServesMetricsFamilies(t *testing.T) {
	resetMetricsCacheForTest()

	p, err := Init(context.Background(), Config{
		Mode:           ModeProm,
		PrometheusAddr: "127.0.0.1:0",
		MetricPrefix:   "jig",
		OnWarn:         func(e error) { t.Logf("warn: %v", e) },
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if p == nil || p.Mode() != ModeProm {
		t.Fatalf("provider mode = %q; want prom", p.Mode())
	}
	if p.PrometheusAddr() == "" || p.PrometheusAddr() == "127.0.0.1:0" {
		t.Fatalf("PrometheusAddr not resolved: %q", p.PrometheusAddr())
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })

	ex, err := NewEventExporter(p)
	if err != nil {
		t.Fatalf("NewEventExporter: %v", err)
	}
	ex.RegisterWorkflow("r1", makeWF())
	ctx := context.Background()
	ex.handle(ctx, engine.RunStarted{RunID: "r1", Workflow: "wf"})
	ex.handle(ctx, engine.RunFinished{RunID: "r1"})
	// One tool + one network observation via the reporter helper.
	tr := NewTelemetryReporter(nil, p, nil)
	tr.ToolCall("Read", "detail")
	tr.recordNetwork()

	url := "http://" + p.PrometheusAddr() + "/metrics"

	// Poll briefly — the Prometheus exporter's scrape is synchronous with
	// Collect, but instrument registration may have raced with the initial
	// request in slow CI. Bounded retry keeps the test deterministic.
	var body string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		body = fetchMetrics(t, url)
		if strings.Contains(body, "jig_run_started_total") && strings.Contains(body, "jig_tool_invoked_total") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	for _, want := range []string{
		"jig_run_started_total",
		"jig_run_finished_total",
		"jig_tool_invoked_total",
		"jig_tool_network_request_total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q\n--- body ---\n%s", want, body)
		}
	}
}

func fetchMetrics(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// TestPrometheusEmptyAddrDisablesListener asserts the "empty bind disables
// the listener" contract (docs/plans/a18-otel-prometheus-export.md).
func TestPrometheusEmptyAddrDisablesListener(t *testing.T) {
	resetMetricsCacheForTest()
	p, err := Init(context.Background(), Config{
		Mode:           ModeProm,
		PrometheusAddr: "",
		OnWarn:         func(error) {},
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if p.PrometheusAddr() != "" {
		t.Errorf("PrometheusAddr = %q, want empty", p.PrometheusAddr())
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

// TestOTLPUnreachableEndpointDowngradesGracefully asserts that a bad OTLP
// endpoint logs a warning and continues with a working (noop) provider — jig
// run exit codes must not change. Uses http/protobuf so an unreachable host
// fails fast (gRPC's default connect timeout would add ~10s here for no
// signal-quality reason).
func TestOTLPUnreachableEndpointDowngradesGracefully(t *testing.T) {
	resetMetricsCacheForTest()
	p, err := Init(context.Background(), Config{
		Mode:         ModeOTLP,
		OTLPEndpoint: "http://127.0.0.1:1", // unreachable
		OTLPProtocol: "http/protobuf",
		OTLPInsecure: true,
		OnWarn:       func(error) {},
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if p == nil {
		t.Fatal("Init returned nil provider")
	}
	// A follow-up record call against the returned Meter must not panic.
	ex, _ := NewEventExporter(p)
	ex.handle(context.Background(), engine.RunStarted{RunID: "r", Workflow: "wf"})
	// Shutdown must not hang forever even when export fails.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = p.Shutdown(ctx)
}

// TestBothModeInstallsPromAndOTLP boots a Provider with mode=both, verifies
// only the Prometheus listener stands up (OTLP endpoint unset ⇒ warning +
// noop OTLP), and asserts /metrics is still served.
func TestBothModeInstallsPromAndOTLP(t *testing.T) {
	resetMetricsCacheForTest()
	warnings := 0
	p, err := Init(context.Background(), Config{
		Mode:           ModeBoth,
		PrometheusAddr: "127.0.0.1:0",
		OTLPEndpoint:   "",
		OnWarn:         func(error) { warnings++ },
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })

	if warnings == 0 {
		t.Error("expected at least one warning for empty OTLP endpoint")
	}
	if p.PrometheusAddr() == "" {
		t.Error("Prometheus listener did not resolve")
	}
	// /metrics still works.
	body := fetchMetrics(t, "http://"+p.PrometheusAddr()+"/metrics")
	if body == "" {
		t.Error("/metrics returned empty body under mode=both")
	}
}
