package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"jig/internal/engine"
)

// TestOTLPHTTPPushDeliversMetrics stands up an in-process HTTP server and
// asserts the OTLP exporter delivers at least one metric export request when
// the exporter is configured with OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf.
//
// This avoids gRPC's 10-second default connect timeout and validates the
// http/protobuf transport selection.
func TestOTLPHTTPPushDeliversMetrics(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// WithEndpointURL treats the URL as-is (does not append /v1/metrics),
		// so count every POST regardless of path.
		if r.Method == http.MethodPost {
			atomic.AddInt32(&hits, 1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resetMetricsCacheForTest()
	p, err := Init(context.Background(), Config{
		Mode:         ModeOTLP,
		OTLPEndpoint: srv.URL,
		OTLPProtocol: "http/protobuf",
		OTLPInsecure: true,
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })

	ex, err := NewEventExporter(p)
	if err != nil {
		t.Fatalf("NewEventExporter: %v", err)
	}
	ex.RegisterWorkflow("r", makeWF())
	ex.handle(context.Background(), engine.RunStarted{RunID: "r", Workflow: "wf"})
	ex.handle(context.Background(), engine.RunFinished{RunID: "r"})

	// Force flush via Shutdown so we don't have to wait for the periodic
	// reader's default interval.
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
	// One or more export attempts should have been made.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&hits) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Error("OTLP HTTP exporter did not deliver any /v1/metrics requests")
	}
}
