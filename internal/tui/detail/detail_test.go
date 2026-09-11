package detail

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/workflow"
)

// TestDetailFooterTracksRunAvailability verifies the footer is driven by the
// KeyMap: the "r run" hint is absent until a valid workflow loads (Run disabled)
// and present afterwards — the guard and the advertised help move as one unit.
func TestDetailFooterTracksRunAvailability(t *testing.T) {
	// The run hint renders as "r run"; match it with its trailing separator so the
	// assertion doesn't trip on the substring inside "enter runs".
	hasRun := func(s string) bool { return strings.Contains(s, "r run  •") }

	m := New("path/to/wf.toml")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 70, Height: 20})

	if got := m.footerView(); hasRun(got) {
		t.Errorf("footer advertised run before a workflow loaded: %q", got)
	}

	// A load failure leaves wf nil, so run stays unavailable.
	m, _ = m.Update(workflowLoadedMsg{meta: workflow.Meta{Name: "Flow"}, err: errors.New("boom")})
	if got := m.footerView(); hasRun(got) {
		t.Errorf("footer advertised run for an invalid workflow: %q", got)
	}

	// A successful load makes run available; the hint appears without any change
	// to the footer code.
	m, _ = m.Update(workflowLoadedMsg{meta: workflow.Meta{Name: "Flow"}, wf: &workflow.Workflow{}})
	if got := m.footerView(); !hasRun(got) {
		t.Errorf("footer omitted run for a valid workflow: %q", got)
	}
}

func TestDetailRunsCarriesWorkflowContext(t *testing.T) {
	wf := &workflow.Workflow{Meta: workflow.Meta{Name: "feature"}}
	m := New("feature.toml")
	m, _ = m.Update(workflowLoadedMsg{meta: wf.Meta, wf: wf})

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter produced no runs command")
	}
	msg, ok := cmd().(ShowRunsMsg)
	if !ok {
		t.Fatalf("enter produced %T, want ShowRunsMsg", cmd())
	}
	if msg.Workflow != "feature" || msg.Wf != wf {
		t.Fatalf("runs context = %#v, want workflow feature", msg)
	}
}

// ansiStrip removes SGR sequences so index math over visible text is meaningful.
func ansiStrip(s string) string {
	var b []byte
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			if i < len(s) {
				i++ // consume 'm'
			}
			continue
		}
		b = append(b, s[i])
		i++
	}
	return string(b)
}

// TestDetail verifies the detail screen renders inside a panel whose top-edge
// title is the workflow name, falling back to the file path when the workflow is
// unnamed (e.g. unparseable).
func TestDetail(t *testing.T) {
	topEdge := func(m Model) string {
		return strings.SplitN(m.View(), "\n", 2)[0]
	}

	t.Run("named workflow titles with the name", func(t *testing.T) {
		m := New("path/to/wf.toml")
		m, _ = m.Update(tea.WindowSizeMsg{Width: 70, Height: 20})
		m, _ = m.Update(workflowLoadedMsg{meta: workflow.Meta{Name: "My Flow"}, err: errors.New("boom")})
		if first := topEdge(m); !strings.Contains(first, "My Flow") {
			t.Errorf("top edge %q missing workflow name title", first)
		}
	})

	t.Run("unnamed workflow falls back to path", func(t *testing.T) {
		m := New("path/to/unnamed.toml")
		m, _ = m.Update(tea.WindowSizeMsg{Width: 70, Height: 20})
		m, _ = m.Update(workflowLoadedMsg{meta: workflow.Meta{}, err: errors.New("boom")})
		if first := topEdge(m); !strings.Contains(first, "unnamed.toml") {
			t.Errorf("top edge %q should fall back to the file path", first)
		}
	})
}

// TestDetailShowsValidationError confirms an invalid workflow still shows a
// detail view that surfaces the validation failure rather than crashing.
func TestDetailShowsValidationError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.toml")
	os.WriteFile(path, []byte(`
[workflow]
name = "broken"

[[step]]
id = "oops"
type = "command"
`), 0o644)

	m := New(path)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(m.Init()())

	view := m.View()
	if !strings.Contains(view, "broken") || !strings.Contains(view, "invalid") {
		t.Fatalf("expected invalid-workflow detail view:\n%s", view)
	}
}

// TestDetailChartToggle verifies the list⇆chart toggle: the body switches
// between the flat list and the box-drawn chart, the footer/help advertise the
// toggle and track its label, and horizontal scroll is rebound per mode so the
// wide-chart escape hatch is live only while charting.
func TestDetailChartToggle(t *testing.T) {
	const src = `
[workflow]
name = "toggle"
version = "1"
[defaults]
max_parallel = 3
[[step]]
id = "root"
type = "command"
run = "x"
[[step]]
id = "leaf"
type = "agent"
depends_on = ["root"]
skill = "s"
`
	wf, err := workflow.Decode(src, "")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	load := func() Model {
		m := New("wf.toml")
		m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		m, _ = m.Update(workflowLoadedMsg{meta: wf.Meta, wf: wf})
		return m
	}
	press := func(m Model, key string) Model {
		m, _ = m.Update(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
		return m
	}
	plain := ansiStrip

	t.Run("footer and help advertise the toggle once a workflow loads", func(t *testing.T) {
		m := load()
		if !strings.Contains(plain(m.footerView()), "v chart") {
			t.Errorf("footer should advertise 'v chart', got %q", plain(m.footerView()))
		}
		found := false
		for _, sec := range m.HelpSections() {
			for _, b := range sec.Bindings {
				if b.Enabled() && b.Help().Key == "v" {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("help overlay should list the enabled toggle binding")
		}
	})

	t.Run("toggle disabled until a valid workflow loads", func(t *testing.T) {
		m := New("wf.toml")
		m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		if strings.Contains(plain(m.footerView()), "chart") {
			t.Errorf("footer should not advertise the chart toggle before load")
		}
	})

	t.Run("v switches the body from list to chart and back", func(t *testing.T) {
		m := load()
		if strings.Contains(m.body(), "╭") {
			t.Fatalf("default body should be the flat list, not the chart")
		}
		m = press(m, "v")
		if !m.viewMode {
			t.Fatalf("viewMode should be true after toggling")
		}
		if !strings.Contains(m.body(), "╭") {
			t.Errorf("chart body should contain box-drawing glyphs")
		}
		if !strings.Contains(plain(m.footerView()), "v list") {
			t.Errorf("footer should flip to 'v list' in chart mode, got %q", plain(m.footerView()))
		}
		m = press(m, "v")
		if m.viewMode {
			t.Errorf("viewMode should be false after toggling back")
		}
		if strings.Contains(m.body(), "╭") {
			t.Errorf("body should return to the flat list after toggling back")
		}
	})

	t.Run("horizontal scroll is rebound only in chart mode", func(t *testing.T) {
		m := load()
		if m.vp.KeyMap.Left.Enabled() {
			t.Errorf("horizontal scroll should be unbound in list mode")
		}
		if !containsKey(m.keys.Back.Keys(), "left") {
			t.Errorf("Back should own 'left' in list mode")
		}
		m = press(m, "v")
		if !m.vp.KeyMap.Left.Enabled() {
			t.Errorf("horizontal scroll should be bound in chart mode")
		}
		if containsKey(m.keys.Back.Keys(), "left") {
			t.Errorf("Back should shed 'left' in chart mode so it scrolls the chart")
		}
		m = press(m, "v")
		if m.vp.KeyMap.Left.Enabled() {
			t.Errorf("horizontal scroll should be unbound again after toggling back")
		}
		if !containsKey(m.keys.Back.Keys(), "left") {
			t.Errorf("Back should reclaim 'left' after toggling back to the list")
		}
	})
}

func TestDetailChartHorizontalScrollAndUnavailableFallback(t *testing.T) {
	const src = `
[workflow]
name = "wide-chart"
version = "1"
[[step]]
id = "first_root"
type = "command"
run = "x"
[[step]]
id = "second_root"
type = "command"
run = "x"
[[step]]
id = "third_root"
type = "command"
run = "x"
[[step]]
id = "fourth_root"
type = "command"
run = "x"
`
	wf, err := workflow.Decode(src, "")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	t.Run("wide chart changes viewport horizontal offset", func(t *testing.T) {
		m := New("wide.toml")
		m, _ = m.Update(tea.WindowSizeMsg{Width: 28, Height: 24})
		m, _ = m.Update(workflowLoadedMsg{meta: wf.Meta, wf: wf})
		m, _ = m.Update(tea.KeyPressMsg{Code: 'v', Text: "v"})
		if got := m.vp.XOffset(); got != 0 {
			t.Fatalf("initial chart x offset = %d, want 0", got)
		}

		m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		right := m.vp.XOffset()
		if right <= 0 {
			t.Fatalf("right key left chart x offset at %d; chart should be horizontally scrollable", right)
		}
		m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
		if got := m.vp.XOffset(); got >= right {
			t.Errorf("left key changed chart x offset from %d to %d, want a smaller offset", right, got)
		}
	})

	t.Run("failed reload leaves chart mode through the defensive list fallback", func(t *testing.T) {
		m := New("wide.toml")
		m, _ = m.Update(tea.WindowSizeMsg{Width: 28, Height: 24})
		m, _ = m.Update(workflowLoadedMsg{meta: wf.Meta, wf: wf})
		m, _ = m.Update(tea.KeyPressMsg{Code: 'v', Text: "v"})
		m, _ = m.Update(workflowLoadedMsg{meta: wf.Meta, err: errors.New("reload failed")})
		if m.viewMode {
			t.Errorf("failed reload should leave chart mode")
		}
		if m.keys.Toggle.Enabled() {
			t.Errorf("failed reload should disable the chart toggle")
		}
		if got := ansiStrip(m.body()); !strings.Contains(got, "invalid workflow") {
			t.Errorf("failed reload body should render the list fallback error, got:\n%s", got)
		}
	})

	t.Run("nil workflow chart fallback is empty and does not panic", func(t *testing.T) {
		m := Model{viewMode: true}
		if got := m.chartView(); got != "" {
			t.Errorf("nil-workflow chart fallback = %q, want empty flat list", got)
		}
	})
}

func containsKey(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}
