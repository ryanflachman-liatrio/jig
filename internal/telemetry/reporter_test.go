package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"jig/internal/engine"
	"jig/internal/interaction"
	"jig/internal/step"
	"jig/internal/workflow"
)

// captureReporter is a minimal engine.Reporter that records ToolCall, Output,
// and Finding observations for later assertion.
type captureReporter struct {
	tools    []string
	details  []string
	outputs  []string
	messages int
	findings []engine.SecurityFinding
}

func (c *captureReporter) Output(d string) { c.outputs = append(c.outputs, d) }
func (c *captureReporter) ToolCall(t, d string) {
	c.tools = append(c.tools, t)
	c.details = append(c.details, d)
}
func (c *captureReporter) Message(seq, iter int) { c.messages++ }
func (c *captureReporter) Question(ctx context.Context, r interaction.QuestionRequest) interaction.QuestionResponse {
	return interaction.QuestionResponse{}
}
func (c *captureReporter) Finding(sf engine.SecurityFinding) { c.findings = append(c.findings, sf) }

// scriptedExec is a minimal engine.Executor that reports one ToolCall and
// returns the caller's requested Result, so tests can assert per-tool and
// per-step measurements without a real runner.
type scriptedExec struct {
	tool   string
	fail   bool
	fanOut bool
}

func (s scriptedExec) Execute(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (*step.Result, error) {
	rep.ToolCall(s.tool, `{"path":"a.go"}`)
	if s.fail {
		return &step.Result{Status: step.StatusFailed, Err: "scripted failure"}, nil
	}
	return &step.Result{Status: step.StatusSucceeded}, nil
}

// SupportsSessionResume opts out; used to verify MetricMux delegates.
func (s scriptedExec) SupportsSessionResume(backend, transport string) bool { return true }

func newTracingProvider(t *testing.T) (*Provider, *tracetest.InMemoryExporter, *sdkmetric.ManualReader) {
	t.Helper()
	spans := tracetest.NewInMemoryExporter()
	sp := sdktrace.NewSimpleSpanProcessor(spans)
	reader := sdkmetric.NewManualReader()
	p := TestProviderWithReader(Config{Mode: ModeBoth, MetricPrefix: "jig"}, reader, sp)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	return p, spans, reader
}

func TestTelemetryReporterPassThroughAndToolCounter(t *testing.T) {
	p, _, reader := newTracingProvider(t)
	inner := &captureReporter{}
	base := []attribute.KeyValue{
		attribute.String("workflow", "wf"),
		attribute.String("step", "collect"),
		attribute.String("step_type", "command"),
		attribute.String("backend", "claude"),
		attribute.String("transport", "sdk"),
		attribute.String("model", ""),
	}
	tr := NewTelemetryReporter(inner, p, base)
	tr.Output("chunk-a")
	tr.ToolCall("Read", `{"path":"a.go"}`)
	tr.ToolCall("Read", `{"path":"b.go"}`)
	tr.ToolCall("Bash", `{"cmd":"echo"}`)
	tr.Message(3, 0)
	tr.Finding(engine.SecurityFinding{Tier: "guard", Severity: "low"})

	if len(inner.tools) != 3 || inner.tools[0] != "Read" {
		t.Errorf("inner tools = %v, want [Read,Read,Bash]", inner.tools)
	}
	if inner.messages != 1 {
		t.Errorf("inner messages = %d, want 1", inner.messages)
	}
	if len(inner.outputs) != 1 || inner.outputs[0] != "chunk-a" {
		t.Errorf("inner outputs = %v", inner.outputs)
	}
	if len(inner.findings) != 1 {
		t.Errorf("inner findings = %v", inner.findings)
	}

	if got := sumForCounter(t, reader, "jig.tool.invoked"); got != 3 {
		t.Errorf("jig.tool.invoked = %d, want 3", got)
	}
	if !hasAttribute(t, reader, "jig.tool.invoked", "tool", "Read") {
		t.Error("tool.invoked missing tool=Read attr")
	}
	if !hasAttribute(t, reader, "jig.tool.invoked", "tool", "Bash") {
		t.Error("tool.invoked missing tool=Bash attr")
	}
	if !hasAttribute(t, reader, "jig.tool.invoked", "step", "collect") {
		t.Error("tool.invoked missing step=collect attr")
	}
	// detail must not appear as an attribute value on any metric.
	if hasAttribute(t, reader, "jig.tool.invoked", "detail", `{"path":"a.go"}`) {
		t.Error("cardinality leak: detail on tool.invoked")
	}
}

func TestTelemetryReporterNilInner(t *testing.T) {
	// nil inner Reporter must not panic and must still record metrics.
	p, _, reader := newTracingProvider(t)
	tr := NewTelemetryReporter(nil, p, nil)
	tr.Output("x")
	tr.ToolCall("Read", "detail")
	tr.Message(1, 0)
	tr.Question(context.Background(), interaction.QuestionRequest{})
	tr.Finding(engine.SecurityFinding{})
	tr.recordNetwork()

	if got := sumForCounter(t, reader, "jig.tool.invoked"); got != 1 {
		t.Errorf("jig.tool.invoked = %d, want 1", got)
	}
	if got := sumForCounter(t, reader, "jig.tool.network_request"); got != 1 {
		t.Errorf("jig.tool.network_request = %d, want 1", got)
	}
}

func TestNilTelemetryReporterIsSafe(t *testing.T) {
	var tr *TelemetryReporter
	tr.Output("x")
	tr.ToolCall("t", "d")
	tr.Message(1, 0)
	tr.Question(context.Background(), interaction.QuestionRequest{})
	tr.Finding(engine.SecurityFinding{})
	tr.recordNetwork()
}

func TestMetricMuxRecordsStepSpanAndDuration(t *testing.T) {
	p, spans, reader := newTracingProvider(t)
	labels := func(runID, stepID string) []attribute.KeyValue {
		return []attribute.KeyValue{
			attribute.String("workflow", "wf"),
			attribute.String("step", stepID),
			attribute.String("step_type", "command"),
			attribute.String("backend", "claude"),
			attribute.String("transport", "sdk"),
			attribute.String("model", ""),
		}
	}
	mm := NewMetricMux(scriptedExec{tool: "Bash"}, p, labels)

	wf, err := workflow.Decode(`
[workflow]
name = "wf"
version = "1"
[[step]]
id = "collect"
type = "command"
run = "true"
`, "")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	stp := &wf.Steps[0]
	req := engine.StepRequest{RunID: "r1", Step: stp, Attempt: 2, Iteration: 1}
	inner := &captureReporter{}
	res, err := mm.Execute(context.Background(), req, inner)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != step.StatusSucceeded {
		t.Errorf("Status = %q", res.Status)
	}

	// Reporter should have recorded the ToolCall on the wrapper (metric),
	// and pass-through to inner.
	if len(inner.tools) != 1 || inner.tools[0] != "Bash" {
		t.Errorf("inner tools = %v", inner.tools)
	}
	if got := sumForCounter(t, reader, "jig.tool.invoked"); got != 1 {
		t.Errorf("jig.tool.invoked = %d, want 1", got)
	}

	// Spans: one jig.step parent + one jig.tool child.
	got := spans.GetSpans()
	if len(got) < 1 {
		t.Fatalf("no spans emitted: %+v", got)
	}
	var stepSpan, toolSpan bool
	for _, s := range got {
		if s.Name == "jig.step" {
			stepSpan = true
			if getAttr(s.Attributes, "outcome") != "succeeded" {
				t.Errorf("step span outcome = %q", getAttr(s.Attributes, "outcome"))
			}
			if getAttr(s.Attributes, "step") != "collect" {
				t.Errorf("step span step attr = %q", getAttr(s.Attributes, "step"))
			}
			if getAttr(s.Attributes, "backend") != "claude" {
				t.Errorf("step span backend = %q", getAttr(s.Attributes, "backend"))
			}
			if getAttrInt(s.Attributes, "attempt") != 2 {
				t.Errorf("step span attempt = %d", getAttrInt(s.Attributes, "attempt"))
			}
		}
		if s.Name == "jig.tool" {
			toolSpan = true
			if getAttr(s.Attributes, "tool") != "Bash" {
				t.Errorf("tool span tool = %q", getAttr(s.Attributes, "tool"))
			}
		}
	}
	if !stepSpan {
		t.Error("missing jig.step span")
	}
	if !toolSpan {
		t.Error("missing jig.tool span")
	}
}

func TestMetricMuxRecordsFailureSpanStatus(t *testing.T) {
	p, spans, _ := newTracingProvider(t)
	mm := NewMetricMux(scriptedExec{tool: "Bash", fail: true}, p, nil)
	wf, _ := workflow.Decode(`
[workflow]
name = "wf"
version = "1"
[[step]]
id = "collect"
type = "command"
run = "true"
`, "")
	req := engine.StepRequest{RunID: "r1", Step: &wf.Steps[0]}
	res, _ := mm.Execute(context.Background(), req, &captureReporter{})
	if res.Status != step.StatusFailed {
		t.Errorf("Status = %q", res.Status)
	}
	// Span should be marked Error.
	for _, s := range spans.GetSpans() {
		if s.Name != "jig.step" {
			continue
		}
		if s.Status.Code.String() != "Error" {
			t.Errorf("span status = %s, want Error", s.Status.Code)
		}
	}
}

func TestMetricMuxWrapsNetworkRequest(t *testing.T) {
	p, _, reader := newTracingProvider(t)
	// scriptedExec doesn't call NetworkRequest, so use an anonymous inline
	// executor that does.
	inner := engine.Executor(engineExecFn(func(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (*step.Result, error) {
		req.NetworkRequest()
		req.NetworkRequest()
		return &step.Result{Status: step.StatusSucceeded}, nil
	}))
	mm := NewMetricMux(inner, p, nil)

	wf, _ := workflow.Decode(`
[workflow]
name = "wf"
version = "1"
[[step]]
id = "collect"
type = "command"
run = "true"
`, "")

	origCalls := 0
	req := engine.StepRequest{
		RunID: "r1", Step: &wf.Steps[0],
		NetworkRequest: func() { origCalls++ },
	}
	res, err := mm.Execute(context.Background(), req, &captureReporter{})
	if err != nil || res == nil {
		t.Fatalf("Execute err=%v res=%v", err, res)
	}
	// Original callback still runs (delegated) — proves we wrap, not replace.
	if origCalls != 2 {
		t.Errorf("original NetworkRequest called %d times, want 2", origCalls)
	}
	if got := sumForCounter(t, reader, "jig.tool.network_request"); got != 2 {
		t.Errorf("jig.tool.network_request = %d, want 2", got)
	}
}

func TestMetricMuxNilProviderIsPassThrough(t *testing.T) {
	// Zero telemetry: MetricMux should still delegate cleanly and never
	// record a measurement (no reader available anyway).
	mm := NewMetricMux(scriptedExec{tool: "Bash"}, nil, nil)
	wf, _ := workflow.Decode(`
[workflow]
name = "wf"
version = "1"
[[step]]
id = "s"
type = "command"
run = "true"
`, "")
	inner := &captureReporter{}
	req := engine.StepRequest{RunID: "r1", Step: &wf.Steps[0]}
	res, err := mm.Execute(context.Background(), req, inner)
	if err != nil || res == nil || res.Status != step.StatusSucceeded {
		t.Fatalf("Execute err=%v res=%v", err, res)
	}
	if len(inner.tools) != 1 || inner.tools[0] != "Bash" {
		t.Errorf("inner tools = %v", inner.tools)
	}
}

func TestMetricMuxForwardsSessionResume(t *testing.T) {
	p, _, _ := newTracingProvider(t)
	mm := NewMetricMux(scriptedExec{tool: "Bash"}, p, nil)
	if !mm.SupportsSessionResume("claude", "sdk") {
		t.Error("MetricMux did not forward SupportsSessionResume")
	}
	// Executor without SessionResumeSupport returns false.
	mm2 := NewMetricMux(engineExecFn(func(context.Context, engine.StepRequest, engine.Reporter) (*step.Result, error) {
		return &step.Result{Status: step.StatusSucceeded}, nil
	}), p, nil)
	if mm2.SupportsSessionResume("claude", "sdk") {
		t.Error("MetricMux forwarded SessionResume to non-supporting executor")
	}
}

// engineExecFn adapts a bare function to engine.Executor.
type engineExecFn func(context.Context, engine.StepRequest, engine.Reporter) (*step.Result, error)

func (f engineExecFn) Execute(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (*step.Result, error) {
	return f(ctx, req, rep)
}

// getAttr / getAttrInt fetch a named attribute value from a KeyValue slice.
func getAttr(kvs []attribute.KeyValue, key string) string {
	for _, kv := range kvs {
		if string(kv.Key) == key {
			return kv.Value.AsString()
		}
	}
	return ""
}

func getAttrInt(kvs []attribute.KeyValue, key string) int64 {
	for _, kv := range kvs {
		if string(kv.Key) == key {
			return kv.Value.AsInt64()
		}
	}
	return 0
}
