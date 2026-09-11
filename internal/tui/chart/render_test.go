package chart

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"jig/internal/tui/shared"
)

// updateGolden regenerates the .golden fixtures instead of comparing against
// them: `go test ./internal/tui/chart -run TestChartGolden -update`. Because
// the chart layout is fully deterministic (longest-path ranks over depends_on +
// Steps-order within a rank), the rendered art is stable and safe to golden-test.
var updateGolden = flag.Bool("update", false, "update chart golden files")

// ansiEscape strips SGR color codes so the goldens are readable box-art and do
// not depend on the terminal's color profile.
var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;]*m")

// goldenChart renders a workflow at a fixed width, strips styling, and asserts
// it matches (or, under -update, rewrites) testdata/<name>.golden.
func goldenChart(t *testing.T, name, src string, width int) {
	t.Helper()
	wf := mustDecode(t, src)
	got := ansiEscape.ReplaceAllString(RenderChart(wf, width), "")

	path := filepath.Join("testdata", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	if got != string(want) {
		t.Errorf("chart %q mismatch:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestChartGolden(t *testing.T) {
	cases := []struct {
		name  string
		width int
		src   string
	}{
		{
			// A near-linear chain renders as a centered vertical spine.
			name:  "linear",
			width: 60,
			src: `
[workflow]
name = "linear"
version = "1"
[defaults]
max_parallel = 3
[[step]]
id = "build"
type = "command"
run = "x"
[[step]]
id = "analyze"
type = "agent"
depends_on = ["build"]
skill = "s"
[[step]]
id = "gate"
type = "review"
depends_on = ["analyze"]
output_type = "bool"
[[step.review]]
source = "diff"
label = "Code changes"
`,
		},
		{
			// Fan-out from a root, fan-in into a join: exercises the bus-row
			// routing and junction glyphs.
			name:  "fanout_fanin",
			width: 70,
			src: `
[workflow]
name = "fan"
version = "1"
[defaults]
max_parallel = 3
[[step]]
id = "intake"
type = "agent"
skill = "s"
[[step]]
id = "back"
type = "agent"
depends_on = ["intake"]
skill = "s"
[[step]]
id = "front"
type = "agent"
depends_on = ["intake"]
skill = "s"
[[step]]
id = "plan"
type = "agent"
depends_on = ["back", "front"]
skill = "s"
`,
		},
		{
			// Conditional (when) edge, a validate gate, and a bounded loop
			// back-edge routed up the right-side channel.
			name:  "conditional_loop",
			width: 72,
			src: `
[workflow]
name = "cond"
version = "1"
[defaults]
max_parallel = 3
[[step]]
id = "plan"
type = "agent"
skill = "s"
[[step]]
id = "review"
type = "review"
depends_on = ["plan"]
output_type = { enum = ["approve", "revise"] }
[[step.route]]
when = "review == 'revise'"
goto = "plan"
max_iterations = 3
[[step.review]]
source = "@plan.summary"
label = "Plan summary"
[[step]]
id = "impl"
type = "command"
depends_on = ["review"]
when = "review == 'approve'"
run = "make"
[step.validate]
command = "go build"
`,
		},
		{
			// A guard, loop guard, and gate check all longer than chartLabelMax:
			// exercises label truncation and the right-margin/canvas growth that
			// keeps a wide loop caption and gate label from clipping.
			name:  "wide_labels",
			width: 72,
			src: `
[workflow]
name = "wide"
version = "1"
[defaults]
max_parallel = 3
[[step]]
id = "plan"
type = "agent"
skill = "s"
[[step]]
id = "review"
type = "review"
depends_on = ["plan"]
output_type = { enum = ["approve", "revise_with_detailed_feedback"] }
[[step.route]]
when = "review == 'revise_with_detailed_feedback'"
goto = "plan"
max_iterations = 3
[[step.review]]
source = "@plan.summary"
label = "Plan summary"
[[step]]
id = "impl"
type = "command"
depends_on = ["review"]
when = "review == 'approve'"
run = "make"
[step.validate]
command = "go build ./... && go test ./... -race -count=1"
`,
		},
		{
			// A foreach family node (analyze): the static graph stays one node
			// per declared family, annotated with a compact ×N marker.
			name:  "foreach",
			width: 72,
			src: `
[workflow]
name = "fanout"
version = "1"
[defaults]
max_parallel = 3
[[step]]
id = "discover"
type = "agent"
skill = "s"
  [step.schema]
  targets = { list = { name = "text", path = "text" } }
[[step]]
id = "analyze"
type = "agent"
depends_on = ["discover"]
skill = "s"
  [step.foreach]
  items = "@discover.targets"
  as = "target"
  max_items = 8
  [step.schema]
  finding = "text"
[[step]]
id = "report"
type = "command"
depends_on = ["analyze"]
run = "x"
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goldenChart(t, tc.name, tc.src, tc.width)
		})
	}
}

// foreachWF is a minimal discover -> analyze(foreach, max_items=8) -> report
// workflow, reused by the narrow-width and horizontal-scroll tests below.
const foreachWF = `
[workflow]
name = "fanout"
version = "1"
[defaults]
max_parallel = 3
[[step]]
id = "discover"
type = "agent"
skill = "s"
  [step.schema]
  targets = { list = { name = "text", path = "text" } }
[[step]]
id = "analyze"
type = "agent"
depends_on = ["discover"]
skill = "s"
  [step.foreach]
  items = "@discover.targets"
  as = "target"
  max_items = 8
  [step.schema]
  finding = "text"
`

// TestChartForEachAnnotation proves a foreach family's node type line carries
// the compact ×N marker at a normal width.
func TestChartForEachAnnotation(t *testing.T) {
	wf := mustDecode(t, foreachWF)
	got := ansiEscape.ReplaceAllString(RenderChart(wf, 72), "")
	if !strings.Contains(got, "×8") {
		t.Errorf("expected ×8 foreach annotation in chart:\n%s", got)
	}
}

// TestChartForEachNarrowWidth proves a very narrow terminal width neither
// panics nor silently drops the family node — chartLayout may (like any wide
// chart) render a canvas wider than the requested width, which the caller
// scrolls horizontally, but every declared node — family included — still
// appears somewhere in the rendered art.
func TestChartForEachNarrowWidth(t *testing.T) {
	wf := mustDecode(t, foreachWF)
	got := ansiEscape.ReplaceAllString(RenderChart(wf, 20), "")
	for _, id := range []string{"discover", "analyze"} {
		if !strings.Contains(got, id) {
			t.Errorf("narrow chart missing node %q:\n%s", id, got)
		}
	}
}

// TestChartForEachHorizontalScroll proves RenderChart never clips a family
// node's ×N marker to fit a requested width smaller than the node's own
// content needs — every rendered line stays at least as wide as the node
// boxes it draws (the caller, e.g. the workflow detail screen, scrolls
// horizontally rather than the chart clipping content), the same contract
// every other wide chart element (a long gate/loop label) already relies on.
func TestChartForEachHorizontalScroll(t *testing.T) {
	wf := mustDecode(t, foreachWF)
	const requested = 10 // far narrower than "analyze" + "agent ×8" naturally needs.
	got := RenderChart(wf, requested)
	plain := ansiEscape.ReplaceAllString(got, "")
	if !strings.Contains(plain, "×8") {
		t.Errorf("expected ×8 marker to survive unclipped at a narrow requested width:\n%s", plain)
	}
	if !strings.Contains(plain, "analyze") {
		t.Errorf("expected the family node id to survive at a narrow requested width:\n%s", plain)
	}
	for i, line := range strings.Split(got, "\n") {
		if w := lipgloss.Width(line); w > requested*4 {
			t.Fatalf("line %d implausibly wide (%d) — rendering likely runaway, not a deliberate scrollable canvas:\n%s", i, w, plain)
		}
	}
}

func TestGateLabelsRenderInsideNode(t *testing.T) {
	tests := []struct {
		name     string
		validate string
		want     string
	}{
		{name: "command", validate: `command = "go test ./..."`, want: "go test ./..."},
		{name: "schema", validate: `output_schema = "result.schema.json"`, want: "schema"},
		{name: "contains", validate: `output_contains = "ready"`, want: `contains "ready"`},
		{name: "exists", validate: `output_exists = true`, want: "exists"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf := mustDecode(t, `
[workflow]
name = "gate-label"
version = "1"
[[step]]
id = "verify"
type = "command"
run = "x"
output = "result.txt"
[step.validate]
`+tt.validate)
			got := ansiEscape.ReplaceAllString(RenderChart(wf, 72), "")
			if !strings.Contains(got, tt.want) {
				t.Errorf("chart missing %q gate label:\n%s", tt.want, got)
			}
			if !strings.Contains(got, "command "+shared.GateGlyph) {
				t.Errorf("chart missing command gate marker:\n%s", got)
			}
		})
	}
}

func TestNodeBoxGateLabelTruncationAndMarkerComposition(t *testing.T) {
	const innerW = chartBoxMaxInner
	gateLabel := "驗證結果包含很多文字 and more"
	node := chartNode{
		id:         "combined",
		typ:        "agent",
		gate:       true,
		gateLabel:  gateLabel,
		retry:      true,
		maxRetries: 3,
		loop:       &chartLoop{target: "combined", maxIter: 2},
		foreach:    &chartForEach{maxItems: 8},
	}

	got := ansiEscape.ReplaceAllString(renderNodeBox(node, innerW), "")
	for _, marker := range []string{
		shared.GateGlyph,
		shared.LoopGlyph,
		shared.ForEachGlyph + "8",
		shared.RetryGlyph + "3",
	} {
		if !strings.Contains(got, marker) {
			t.Errorf("node box missing marker %q:\n%s", marker, got)
		}
	}
	wantGate := shared.TruncateTitle(gateLabel, innerW)
	if !strings.Contains(got, wantGate) {
		t.Errorf("node box missing Unicode-aware truncated gate label %q:\n%s", wantGate, got)
	}
	if lipgloss.Width(wantGate) > innerW {
		t.Errorf("truncated gate label width = %d, want <= %d", lipgloss.Width(wantGate), innerW)
	}
	if gotHeight := lipgloss.Height(got); gotHeight != chartBoxHeight {
		t.Errorf("node box height = %d, want uniform %d:\n%s", gotHeight, chartBoxHeight, got)
	}

	nonGate := ansiEscape.ReplaceAllString(renderNodeBox(chartNode{id: "plain", typ: "command"}, innerW), "")
	if gotHeight := lipgloss.Height(nonGate); gotHeight != chartBoxHeight {
		t.Errorf("non-gated node box height = %d, want uniform %d:\n%s", gotHeight, chartBoxHeight, nonGate)
	}
}
