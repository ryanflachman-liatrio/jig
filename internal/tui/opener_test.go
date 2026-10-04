package tui

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ryanflachman-liatrio/jig/internal/config"
	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/tui/monitor"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

func openerFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("synthetic\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func openerHelperArgv(t *testing.T, mode, output string) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return []string{exe, "-test.run=^TestOpenLocationHelper$", "--", "jig-opener-helper", mode, output}
}

// The explicit argv marker keeps ordinary test execution inert. All files and
// tool inputs are synthetic; this helper never dispatches a workflow.
func TestOpenLocationHelper(t *testing.T) {
	idx := -1
	for i, arg := range os.Args {
		if arg == "jig-opener-helper" {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	mode, output := os.Args[idx+1], os.Args[idx+2]
	openerInteractive(os.Args[idx+3:])
	data, _ := json.Marshal(os.Args[idx+3:])
	if err := os.WriteFile(output, data, 0600); err != nil {
		os.Exit(3)
	}
	switch mode {
	case "fail":
		os.Exit(7)
	case "wait":
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt)
		<-ch
	}
	os.Exit(0)
}

func TestOpenLocationLaunchArguments(t *testing.T) {
	path := openerFile(t, "雪 file:12;$(touch SENTINEL).go")
	output := filepath.Join(t.TempDir(), "argv.json")
	terminal := false
	base := openerHelperArgv(t, "success", output)
	for _, tc := range []struct {
		name         string
		line, column int
		position     string
	}{
		{"supplied", 8, 3, "8:3"}, {"absent", 0, 0, "1:1"}, {"nonpositive", -8, -1, "1:1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := shared.OpenLocationRequest{Target: shared.OpenLocationTarget{Path: path, Line: tc.line, Column: tc.column}}
			cfg := config.OpenerConfig{Argv: append(append([]string{}, base...), "{line}:{column}", "{path}", "literal;$HOME|>"), Terminal: &terminal}
			msg := prepareOpenLocation(1, req, cfg, "must-not-be-used")
			if msg.err != nil {
				t.Fatal(msg.err)
			}
			result := launchOpenLocation(t.Context(), msg, nil)().(shared.OpenLocationResult)
			if !strings.HasPrefix(result.Notice, "Opened") {
				t.Fatal(result.Notice)
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			want := []string{tc.position, path, "literal;$HOME|>"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("argv = %q, want %q", got, want)
			}
		})
	}
	// A real shell command would create this sentinel. Literal tokenization must not.
	sentinel := filepath.Join(t.TempDir(), "SENTINEL")
	editor := strings.Join([]string{`"` + base[0] + `"`, base[1], base[2], base[3], base[4], `"` + base[5] + `"`, `'$(touch ` + sentinel + `)'`, `'two words'`, `escaped\ space`, `""`, `|`, `>`, `$HOME`}, " ")
	req := shared.OpenLocationRequest{Target: shared.OpenLocationTarget{Path: path, Line: 4}}
	msg := prepareOpenLocation(2, req, config.OpenerConfig{Terminal: &terminal}, editor)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	result := launchOpenLocation(t.Context(), msg, nil)().(shared.OpenLocationResult)
	if !strings.Contains(result.Notice, "without position navigation") {
		t.Fatal(result.Notice)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("shell substitution executed")
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"$(touch " + sentinel + ")", "two words", "escaped space", "", "|", ">", "$HOME", path}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback argv = %q, want %q", got, want)
	}
	for _, editor := range []string{"", "'unterminated", `editor trailing\`, `'' x`, "editor\x00"} {
		if _, err := tokenizeEditor(editor); err == nil {
			t.Fatalf("accepted malformed editor %q", editor)
		}
	}
}

func TestOpenLocationLaunchFileValidation(t *testing.T) {
	file := openerFile(t, "outside project.go")
	symlink := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(file, symlink); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, symlink} {
		if err := shared.ValidateOpenLocationPath(path); err != nil {
			t.Fatalf("%q: %v", path, err)
		}
	}
	for _, path := range []string{"", "relative.go", "https://example.test/a", "file:///tmp/a", file + "\n", file + "\x1b", file + "\u0085", file + ":5", t.TempDir(), file + "missing"} {
		if err := shared.ValidateOpenLocationPath(path); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := shared.ValidateOpenLocationPath(file); err == nil {
		t.Fatal("accepted deleted file")
	}
	if err := shared.ValidateOpenLocationPath(symlink); err == nil {
		t.Fatal("accepted dangling link")
	}
}

func openerRoot(t *testing.T) (rootModel, shared.OpenLocationRequest) {
	t.Helper()
	terminal := false
	m := rootModel{ctx: t.Context(), active: screenMonitor, monitor: monitor.New("synthetic-run"), tuiConfig: config.TUIConfig{Opener: config.OpenerConfig{Terminal: &terminal}}}
	m.tuiConfig.Opener.Argv = append(openerHelperArgv(t, "success", filepath.Join(t.TempDir(), "args")), "{path}")
	req := shared.OpenLocationRequest{Owner: m.monitor.OpenLocationOwner(), Target: shared.OpenLocationTarget{Path: openerFile(t, "target")}}
	return m, req
}

func TestOpenLocationLaunchLifecycle(t *testing.T) {
	m, req := openerRoot(t)
	m, prepare := m.admitOpenLocation(req)
	id := m.opener.pending
	var duplicate tea.Cmd
	m, duplicate = m.admitOpenLocation(req)
	if duplicate != nil || m.opener.pending != id {
		t.Fatal("duplicate admitted")
	}
	msg := prepare().(openerPreparedMsg)
	m, launch := m.preparedOpenLocation(msg)
	if launch == nil {
		t.Fatal("no launcher")
	}
	m, duplicate = m.preparedOpenLocation(msg)
	if duplicate != nil {
		t.Fatal("duplicate prepared message launched twice")
	}
	before := m.monitor.OpenLocationOwner()
	result := launch().(shared.OpenLocationResult)
	m, _ = m.completeOpenLocation(result)
	if m.opener.pending != 0 || !strings.HasPrefix(m.opener.notice, "Opened") || m.monitor.OpenLocationOwner() != before {
		t.Fatalf("bad completion: %+v", m.opener)
	}
	m, _ = m.completeOpenLocation(shared.OpenLocationResult{ID: id, Notice: "late"})
	if m.opener.notice == "late" {
		t.Fatal("applied stale completion")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*rootModel)
	}{
		{"screen", func(m *rootModel) { m.active = screenHome }},
		{"run", func(m *rootModel) { m.monitor = monitor.New("other") }},
		{"same run replaced", func(m *rootModel) { m.monitor = monitor.New("synthetic-run") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, req := openerRoot(t)
			m, prepare := m.admitOpenLocation(req)
			tc.mutate(&m)
			m, cmd := m.preparedOpenLocation(prepare().(openerPreparedMsg))
			if cmd != nil || m.opener.pending != 0 || m.opener.notice != "" {
				t.Fatal("stale preparation launched or leaked slot")
			}
			m, req = openerRoot(t)
			m, prepare = m.admitOpenLocation(req)
			m, launch := m.preparedOpenLocation(prepare().(openerPreparedMsg))
			tc.mutate(&m)
			m, _ = m.completeOpenLocation(launch().(shared.OpenLocationResult))
			if m.opener.pending != 0 || m.opener.notice != "" {
				t.Fatal("stale completion changed selection")
			}
		})
	}
}

func TestOpenLocationLaunchFailures(t *testing.T) {
	for _, mode := range []string{"fail", "missing", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			m, req := openerRoot(t)
			if mode == "fail" {
				m.tuiConfig.Opener.Argv[4] = "fail"
			}
			if mode == "missing" {
				m.tuiConfig.Opener.Argv[0] = filepath.Join(t.TempDir(), "PRIVATE-missing")
			}
			if mode == "canceled" {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				m.ctx = ctx
			}
			m, prepare := m.admitOpenLocation(req)
			m, launch := m.preparedOpenLocation(prepare().(openerPreparedMsg))
			if launch != nil {
				m, _ = m.completeOpenLocation(launch().(shared.OpenLocationResult))
			}
			if m.opener.pending != 0 || strings.HasPrefix(m.opener.notice, "Opened") || strings.Contains(m.opener.notice, "PRIVATE") {
				t.Fatalf("bad failure %+v", m.opener)
			}
		})
	}
	// Executable removed after preparation exercises Cmd.Start failure, not LookPath.
	msg := openerPreparedMsg{id: 1, argv: []string{filepath.Join(t.TempDir(), "deleted")}}
	result := launchOpenLocation(t.Context(), msg, nil)().(shared.OpenLocationResult)
	if !strings.HasPrefix(result.Notice, "Open failed") {
		t.Fatal(result.Notice)
	}
}

func TestOpenLocationInputRootAndPalette(t *testing.T) {
	m, _ := newOpenerFixture(t, t.Context(), "gui")
	for _, overlay := range []string{"help", "delete", "leave", "palette"} {
		t.Run(overlay, func(t *testing.T) {
			root := m
			switch overlay {
			case "help":
				root.showHelp = true
			case "delete":
				root.confirmDelete = true
			case "leave":
				root.leaveConfirm = true
			case "palette":
				root.palette = root.palette.Show(root.paletteCommands())
			}
			_, cmd := root.Update(makeKey("e"))
			if cmd != nil {
				if _, ok := cmd().(shared.OpenLocationRequest); ok {
					t.Fatal("root overlay passed through e")
				}
			}
		})
	}
	m.palette = m.palette.Show(m.paletteCommands())
	mm, _ := m.Update(tea.KeyPressMsg{Code: 'o', Text: "open file in editor"})
	m = mm.(rootModel)
	selected, ok := m.palette.Selected()
	if !ok || selected.Key != "e" {
		t.Fatalf("palette selected=%+v", selected)
	}
	mm, cmd := m.Update(makeKey("enter"))
	m = mm.(rootModel)
	if cmd == nil {
		t.Fatal("palette did not activate")
	}
	mm, cmd = m.Update(cmd())
	m = mm.(rootModel)
	if cmd == nil {
		t.Fatal("dispatched key did not create request")
	}
	req, ok := cmd().(shared.OpenLocationRequest)
	if !ok || req.Unavailable != "" || req.Target.Path == "" {
		t.Fatalf("req=%+v", req)
	}
	before := m.monitor.OpenLocationOwner()
	m, prepare := m.admitOpenLocation(req)
	m, launch := m.preparedOpenLocation(prepare().(openerPreparedMsg))
	if launch == nil {
		t.Fatal(m.opener.notice)
	}
	m, _ = m.completeOpenLocation(launch().(shared.OpenLocationResult))
	if m.monitor.OpenLocationOwner() != before || !strings.HasPrefix(m.opener.notice, "Opened") {
		t.Fatalf("palette launch=%+v", m.opener)
	}
	// Navigating to another step after admission invalidates the prepared request.
	m, prepare = m.admitOpenLocation(req)
	m.monitor = m.monitor.WithSnapshot(engine.RunSnapshot{ID: "synthetic-run", Steps: []step.State{{ID: "edit"}, {ID: "other"}}})
	m.monitor, _ = m.monitor.Update(makeKey("esc"))
	m.monitor, _ = m.monitor.Update(makeKey("j"))
	m, launch = m.preparedOpenLocation(prepare().(openerPreparedMsg))
	if launch != nil || m.opener.pending != 0 {
		t.Fatal("launched after step navigation")
	}
}
