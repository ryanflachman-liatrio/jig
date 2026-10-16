package telemetry

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
)

func TestD04ReopenPolicyNoTelemetryFields(t *testing.T) {
	ex, reader, _ := newTestExporter(t)
	before := collectAll(t, reader)
	ex.handle(context.Background(), engine.SandboxPolicyBlocked{RunID: "PRIVATE_RUN", Reason: "PRIVATE_DAEMON_PATH", Epoch: 17})
	ex.handle(context.Background(), engine.SandboxPolicyCleared{RunID: "PRIVATE_RUN", Epoch: 17})
	after := collectAll(t, reader)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("policy introduced metrics: %v", after)
	}
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(data)
	if strings.Contains(string(raw), "PRIVATE_") {
		t.Fatal("private policy metadata became labels")
	}
	t.Log("policy events add no metrics, labels, or exporter work")
}
