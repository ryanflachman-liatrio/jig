package tui

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// updateGolden regenerates the .golden fixtures instead of comparing against
// them: `go test ./internal/tui -run TestChartGolden -update`. Because the chart
// layout is fully deterministic (longest-path ranks over depends_on + Steps-order
// within a rank), the rendered art is stable and safe to golden-test.
var updateGolden = flag.Bool("update", false, "update chart golden files")

// ansiEscape strips SGR color codes so the goldens are readable box-art and do
// not depend on the terminal's color profile.
var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;]*m")

// goldenChart renders a workflow at a fixed width, strips styling, and asserts
// it matches (or, under -update, rewrites) testdata/<name>.golden.
func goldenChart(t *testing.T, name, src string, width int) {
	t.Helper()
	wf := mustDecode(t, src)
	got := ansiEscape.ReplaceAllString(renderChart(wf, width), "")

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
review = "diff"
output_type = "bool"
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
review = "@plan.summary"
output_type = { enum = ["approve", "revise"] }
[step.loop]
when = "review == 'revise'"
goto = "plan"
max_iterations = 3
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
review = "@plan.summary"
output_type = { enum = ["approve", "revise_with_detailed_feedback"] }
[step.loop]
when = "review == 'revise_with_detailed_feedback'"
goto = "plan"
max_iterations = 3
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goldenChart(t, tc.name, tc.src, tc.width)
		})
	}
}
