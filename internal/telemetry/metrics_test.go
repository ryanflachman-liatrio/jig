package telemetry

import (
	"context"
	"strconv"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/workflow"
)

// newTestExporter builds an EventExporter backed by a ManualReader so tests
// can drive engine events and Collect() the resulting metrics.
func newTestExporter(t *testing.T) (*EventExporter, *sdkmetric.ManualReader, *Provider) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	p := TestProviderWithReader(Config{Mode: ModeProm, MetricPrefix: "jig"}, reader)
	p.tracer = tracenoop.NewTracerProvider()
	ex, err := NewEventExporter(p)
	if err != nil {
		t.Fatalf("NewEventExporter: %v", err)
	}
	t.Cleanup(func() {
		_ = p.Shutdown(context.Background())
	})
	return ex, reader, p
}

// collect returns the map metric-name → sum for every counter/histogram in
// the reader. Sums for histograms are the underlying Sum aggregation field.
func collectAll(t *testing.T, reader *sdkmetric.ManualReader) map[string]float64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	out := map[string]float64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch a := m.Data.(type) {
			case metricdata.Sum[int64]:
				var total int64
				for _, pt := range a.DataPoints {
					total += pt.Value
				}
				out[m.Name] += float64(total)
			case metricdata.Sum[float64]:
				var total float64
				for _, pt := range a.DataPoints {
					total += pt.Value
				}
				out[m.Name] += total
			case metricdata.Histogram[int64]:
				var total int64
				for _, pt := range a.DataPoints {
					total += pt.Sum
				}
				out[m.Name] += float64(total)
			case metricdata.Histogram[float64]:
				var total float64
				for _, pt := range a.DataPoints {
					total += pt.Sum
				}
				out[m.Name] += total
			}
		}
	}
	return out
}

// dataPointsFor returns every data point associated with metricName across
// scope metrics. Tests use it to inspect per-attribute recordings.
func dataPointsFor(t *testing.T, reader *sdkmetric.ManualReader, metricName string) []metricdata.HistogramDataPoint[float64] {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var out []metricdata.HistogramDataPoint[float64]
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != metricName {
				continue
			}
			h, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				continue
			}
			out = append(out, h.DataPoints...)
		}
	}
	return out
}

// sumForCounter returns the total int64 sum for metricName.
func sumForCounter(t *testing.T, reader *sdkmetric.ManualReader, metricName string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != metricName {
				continue
			}
			s, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			var total int64
			for _, pt := range s.DataPoints {
				total += pt.Value
			}
			return total
		}
	}
	return 0
}

// hasAttribute reports whether at least one data point in metricName carries
// the (key, value) attribute pair.
func hasAttribute(t *testing.T, reader *sdkmetric.ManualReader, metricName, key, value string) bool {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != metricName {
				continue
			}
			switch a := m.Data.(type) {
			case metricdata.Sum[int64]:
				for _, pt := range a.DataPoints {
					if attrValue(pt.Attributes, key) == value {
						return true
					}
				}
			case metricdata.Histogram[float64]:
				for _, pt := range a.DataPoints {
					if attrValue(pt.Attributes, key) == value {
						return true
					}
				}
			case metricdata.Histogram[int64]:
				for _, pt := range a.DataPoints {
					if attrValue(pt.Attributes, key) == value {
						return true
					}
				}
			}
		}
	}
	return false
}

func attrValue(set attribute.Set, key string) string {
	for i := 0; i < set.Len(); i++ {
		kv, _ := set.Get(i)
		if string(kv.Key) == key {
			return kv.Value.Emit()
		}
	}
	return ""
}

// makeWF returns a minimal workflow with one agent step and one command step.
// Used to seed RegisterWorkflow so events carry step_type/backend/model.
func makeWF() *workflow.Workflow {
	src := `
[workflow]
name = "wf"
version = "1"

[[step]]
id = "collect"
type = "command"
run = "true"

[[step]]
id = "analyze"
type = "command"
depends_on = ["collect"]
run = "true"
`
	wf, err := workflow.Decode(src, "")
	if err != nil {
		panic(err)
	}
	return wf
}

func TestExporterRunLifecycle(t *testing.T) {
	ex, reader, _ := newTestExporter(t)
	wf := makeWF()
	ex.RegisterWorkflow("run-1", wf)

	ctx := context.Background()
	ex.handle(ctx, engine.RunStarted{RunID: "run-1", Workflow: "wf", Steps: []string{"collect", "analyze"}})
	time.Sleep(2 * time.Millisecond) // ensures duration histogram records >0
	ex.handle(ctx, engine.RunFinished{RunID: "run-1", Failed: false})

	if got := sumForCounter(t, reader, "jig.run.started"); got != 1 {
		t.Errorf("jig.run.started = %d, want 1", got)
	}
	if got := sumForCounter(t, reader, "jig.run.finished"); got != 1 {
		t.Errorf("jig.run.finished = %d, want 1", got)
	}
	if !hasAttribute(t, reader, "jig.run.finished", "outcome", "succeeded") {
		t.Error("jig.run.finished missing outcome=succeeded attribute")
	}
	if !hasAttribute(t, reader, "jig.run.finished", "workflow", "wf") {
		t.Error("jig.run.finished missing workflow=wf attribute")
	}
	if pts := dataPointsFor(t, reader, "jig.run.duration"); len(pts) != 1 || pts[0].Sum <= 0 {
		t.Errorf("jig.run.duration data = %+v", pts)
	}
}

func TestExporterStepStatusTerminalAndDuration(t *testing.T) {
	ex, reader, _ := newTestExporter(t)
	wf := makeWF()
	ex.RegisterWorkflow("r1", wf)

	cost := 0.42
	ex.handle(context.Background(), engine.StepStatus{
		RunID: "r1", StepID: "collect",
		From: step.StatusPending, To: step.StatusRunning,
	})
	time.Sleep(2 * time.Millisecond)
	ex.handle(context.Background(), engine.StepStatus{
		RunID: "r1", StepID: "collect",
		From: step.StatusRunning, To: step.StatusSucceeded,
		Attempt: 0, Iteration: 0, Generation: 0,
		Cost: &cost, Tokens: 128,
	})

	if got := sumForCounter(t, reader, "jig.step.started"); got != 1 {
		t.Errorf("jig.step.started = %d, want 1", got)
	}
	if got := sumForCounter(t, reader, "jig.step.finished"); got != 1 {
		t.Errorf("jig.step.finished = %d, want 1", got)
	}
	if !hasAttribute(t, reader, "jig.step.finished", "outcome", "succeeded") {
		t.Error("jig.step.finished missing outcome attr")
	}
	if !hasAttribute(t, reader, "jig.step.finished", "step_type", "command") {
		t.Error("jig.step.finished missing step_type=command attr")
	}
	if !hasAttribute(t, reader, "jig.step.finished", "backend", "claude") {
		t.Error("jig.step.finished missing backend=claude attr")
	}
	if pts := dataPointsFor(t, reader, "jig.step.duration"); len(pts) != 1 || pts[0].Sum <= 0 {
		t.Errorf("jig.step.duration = %+v", pts)
	}
	if pts := dataPointsFor(t, reader, "jig.step.cost_usd"); len(pts) != 1 || pts[0].Sum != cost {
		t.Errorf("jig.step.cost_usd = %+v", pts)
	}
}

func TestExporterCostNilNotRecorded(t *testing.T) {
	ex, reader, _ := newTestExporter(t)
	ex.RegisterWorkflow("r1", makeWF())

	// nil Cost — distinguishes "unreported" from "reported zero" (Result.TotalCostUSD).
	ex.handle(context.Background(), engine.StepStatus{
		RunID: "r1", StepID: "collect",
		From: step.StatusPending, To: step.StatusRunning,
	})
	ex.handle(context.Background(), engine.StepStatus{
		RunID: "r1", StepID: "collect",
		From: step.StatusRunning, To: step.StatusSucceeded,
		Cost: nil, Tokens: 0,
	})

	// cost histogram should have no data points.
	pts := dataPointsFor(t, reader, "jig.step.cost_usd")
	if len(pts) != 0 {
		t.Errorf("jig.step.cost_usd has %d data points; want none for nil Cost", len(pts))
	}
	// tokens histogram should also have no data points (Tokens == 0 is treated as unreported).
	if got := sumForCounter(t, reader, "jig.step.tokens"); got != 0 {
		t.Errorf("jig.step.tokens sum = %d, want 0", got)
	}
}

func TestExporterGateAndReviewLifecycle(t *testing.T) {
	ex, reader, _ := newTestExporter(t)
	ex.RegisterWorkflow("r1", makeWF())

	ctx := context.Background()
	ex.handle(ctx, engine.GateResult{RunID: "r1", StepID: "collect", Passed: true})
	ex.handle(ctx, engine.GateResult{RunID: "r1", StepID: "collect", Passed: false, Detail: "flake"})
	ex.handle(ctx, engine.ReviewRequest{RunID: "r1", StepID: "analyze", RoundID: "round-1"})
	time.Sleep(1 * time.Millisecond)
	ex.handle(ctx, engine.ReviewSubmitted{RunID: "r1", StepID: "analyze", RoundID: "round-1", Verdict: "approve"})

	if got := sumForCounter(t, reader, "jig.gate.result"); got != 2 {
		t.Errorf("jig.gate.result = %d, want 2", got)
	}
	if !hasAttribute(t, reader, "jig.gate.result", "outcome", "failed") {
		t.Error("gate result missing outcome=failed attr")
	}
	if got := sumForCounter(t, reader, "jig.review.requested"); got != 1 {
		t.Errorf("jig.review.requested = %d, want 1", got)
	}
	if got := sumForCounter(t, reader, "jig.review.submitted"); got != 1 {
		t.Errorf("jig.review.submitted = %d, want 1", got)
	}
	if pts := dataPointsFor(t, reader, "jig.review.wait_duration"); len(pts) != 1 {
		t.Errorf("jig.review.wait_duration data = %+v", pts)
	}
}

func TestExporterSecurityAndReset(t *testing.T) {
	ex, reader, _ := newTestExporter(t)
	ex.RegisterWorkflow("r1", makeWF())

	ctx := context.Background()
	ex.handle(ctx, engine.SecurityFinding{RunID: "r1", StepID: "collect", Tier: "guard", Severity: "high", Action: "blocked"})
	ex.handle(ctx, engine.SecurityFinding{RunID: "r1", StepID: "collect", Tier: "monitor", Severity: "low", Action: "observed"})
	ex.handle(ctx, engine.StepsReset{RunID: "r1", Target: "collect", Closure: []string{"collect", "analyze"}})

	if got := sumForCounter(t, reader, "jig.security.finding"); got != 2 {
		t.Errorf("jig.security.finding = %d, want 2", got)
	}
	if !hasAttribute(t, reader, "jig.security.finding", "tier", "guard") {
		t.Error("security finding missing tier=guard attr")
	}
	if !hasAttribute(t, reader, "jig.security.finding", "severity", "high") {
		t.Error("security finding missing severity=high attr")
	}
	if got := sumForCounter(t, reader, "jig.reset.applied"); got != 1 {
		t.Errorf("jig.reset.applied = %d, want 1", got)
	}
	if !hasAttribute(t, reader, "jig.reset.applied", "target_step", "collect") {
		t.Error("reset applied missing target_step=collect attr")
	}
}

func TestExporterFanOutCardinality(t *testing.T) {
	ex, reader, _ := newTestExporter(t)
	ex.RegisterWorkflow("r1", makeWF())

	// The exporter records total children per family in one counter; family
	// and generation ride as attributes, never child ids.
	desc := make([]engine.FanOutInstanceDescriptor, 0, 5)
	for i := 0; i < 5; i++ {
		desc = append(desc, engine.FanOutInstanceDescriptor{InstanceID: "collect#" + strconv.Itoa(i), Index: i, ItemSHA256: "sha"})
	}
	ex.handle(context.Background(), engine.FanOutExpanded{
		RunID: "r1", FamilyID: "collect", Generation: 0, Instances: desc,
	})

	if got := sumForCounter(t, reader, "jig.foreach.expanded"); got != 5 {
		t.Errorf("jig.foreach.expanded = %d, want 5", got)
	}
	// Family label present.
	if !hasAttribute(t, reader, "jig.foreach.expanded", "family", "collect") {
		t.Error("foreach expanded missing family=collect attr")
	}
	// Child InstanceIDs must NOT leak as attribute values on the metric.
	for _, d := range desc {
		if hasAttribute(t, reader, "jig.foreach.expanded", "instance_id", d.InstanceID) {
			t.Errorf("cardinality leak: instance_id=%q on jig.foreach.expanded", d.InstanceID)
		}
	}
}

func TestExporterExporterDroppedSelfObservability(t *testing.T) {
	ex, reader, _ := newTestExporter(t)
	ctx := context.Background()
	ex.RecordDropped(ctx, "step_status")
	ex.RecordDropped(ctx, "step_status")
	ex.RecordDropped(ctx, "run_started")

	if got := sumForCounter(t, reader, "jig.exporter.dropped"); got != 3 {
		t.Errorf("jig.exporter.dropped = %d, want 3", got)
	}
	if !hasAttribute(t, reader, "jig.exporter.dropped", "kind", "step_status") {
		t.Error("dropped missing kind=step_status attr")
	}
}

func TestExporterMetricPrefix(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	p := TestProviderWithReader(Config{Mode: ModeProm, MetricPrefix: "acme"}, reader)
	p.tracer = tracenoop.NewTracerProvider()
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	ex, err := NewEventExporter(p)
	if err != nil {
		t.Fatalf("NewEventExporter: %v", err)
	}
	ex.RegisterWorkflow("r1", makeWF())

	ex.handle(context.Background(), engine.RunStarted{RunID: "r1", Workflow: "wf"})
	ex.handle(context.Background(), engine.RunFinished{RunID: "r1"})

	if got := sumForCounter(t, reader, "acme.run.started"); got != 1 {
		t.Errorf("acme.run.started = %d, want 1", got)
	}
	if got := sumForCounter(t, reader, "jig.run.started"); got != 0 {
		t.Errorf("jig.run.started leak = %d", got)
	}
}

func TestExporterForgetsRunAfterFinish(t *testing.T) {
	ex, _, _ := newTestExporter(t)
	ex.RegisterWorkflow("r1", makeWF())
	ex.handle(context.Background(), engine.RunStarted{RunID: "r1", Workflow: "wf"})
	ex.handle(context.Background(), engine.RunFinished{RunID: "r1"})

	ex.mu.Lock()
	_, hasWF := ex.workflows["r1"]
	_, hasStart := ex.runStarts["r1"]
	ex.mu.Unlock()

	if hasWF {
		t.Error("workflows[r1] retained after RunFinished")
	}
	if hasStart {
		t.Error("runStarts[r1] retained after RunFinished")
	}
}

func TestExporterUnknownStepDegradesGracefully(t *testing.T) {
	ex, reader, _ := newTestExporter(t)
	// Do NOT register a workflow. Events should still record with empty
	// step_type / backend / transport / model.
	ex.handle(context.Background(), engine.StepStatus{
		RunID: "r1", StepID: "unknown", From: step.StatusPending, To: step.StatusRunning,
	})
	ex.handle(context.Background(), engine.StepStatus{
		RunID: "r1", StepID: "unknown", From: step.StatusRunning, To: step.StatusSucceeded,
	})
	if got := sumForCounter(t, reader, "jig.step.finished"); got != 1 {
		t.Errorf("jig.step.finished = %d, want 1", got)
	}
	if !hasAttribute(t, reader, "jig.step.finished", "step_type", "") {
		t.Error("step_type should be empty string when workflow not registered")
	}
}

func TestExporterHandleIgnoresUnknownEvent(t *testing.T) {
	ex, _, _ := newTestExporter(t)
	// Passing a nil event must not panic.
	ex.handle(context.Background(), nil)
}
