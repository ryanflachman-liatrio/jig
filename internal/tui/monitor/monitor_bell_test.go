package monitor

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ryanflachman-liatrio/jig/internal/config"
	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/interaction"
	"github.com/ryanflachman-liatrio/jig/internal/tui/palette"
)

// countBells runs cmd and counts tea.RawMsg{"\a"} results, flattening batches.
// Each command runs with a short timeout so tick commands in the same batch
// don't stall the test; the bell command itself returns immediately.
func countBells(t *testing.T, cmd tea.Cmd) int {
	t.Helper()
	if cmd == nil {
		return 0
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(50 * time.Millisecond):
		return 0
	}
	switch msg := msg.(type) {
	case tea.BatchMsg:
		n := 0
		for _, c := range msg {
			n += countBells(t, c)
		}
		return n
	case tea.RawMsg:
		if msg.Msg == "\a" {
			return 1
		}
	}
	return 0
}

// pinBellClock replaces the bell clock with a controllable one for the test.
func pinBellClock(t *testing.T) *time.Time {
	t.Helper()
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	prev := bellClock
	bellClock = func() time.Time { return now }
	t.Cleanup(func() { bellClock = prev })
	return &now
}

func newBellMonitor(t *testing.T, bell *bool) Model {
	t.Helper()
	m := newMonitorWithSteps(t).WithTUIConfig(config.TUIConfig{Bell: bell})
	m.focus = focusSteps
	return m
}

func reviewGate(step string) EngineEventMsg {
	return EngineEventMsg{Event: engine.ReviewRequest{
		RunID: "run-1", StepID: step, Choices: []string{"approve"},
	}}
}

func questionGate(step string) EngineEventMsg {
	return EngineEventMsg{Event: questionEvent("run-1", step, "q-"+step,
		selectQuestion("f", "", "More?", false,
			interaction.QuestionOption{Value: "y", Label: "Yes"},
		),
	)}
}

func TestGateBell(t *testing.T) {
	on := true
	tests := []struct {
		name string
		bell *bool
		run  func(t *testing.T, m Model, now *time.Time) int
		want int
	}{
		{
			name: "first gate rings",
			bell: &on,
			run: func(t *testing.T, m Model, _ *time.Time) int {
				_, cmd := m.Update(reviewGate("a"))
				return countBells(t, cmd)
			},
			want: 1,
		},
		{
			name: "default config never rings",
			bell: nil,
			run: func(t *testing.T, m Model, _ *time.Time) int {
				_, cmd := m.Update(reviewGate("a"))
				return countBells(t, cmd)
			},
			want: 0,
		},
		{
			name: "gate-focused operator is not rung",
			bell: &on,
			run: func(t *testing.T, m Model, _ *time.Time) int {
				m.focus = focusGate
				_, cmd := m.Update(reviewGate("a"))
				return countBells(t, cmd)
			},
			want: 0,
		},
		{
			name: "first gate after run entry rings despite auto-focus",
			bell: &on,
			run: func(t *testing.T, m Model, _ *time.Time) int {
				m = m.FocusPendingInput()
				m, cmd := m.Update(reviewGate("a"))
				if m.focus != focusGate {
					t.Fatalf("focus = %v, want auto-focused focusGate", m.focus)
				}
				return countBells(t, cmd)
			},
			want: 1,
		},
		{
			name: "burst of three gates rings once",
			bell: &on,
			run: func(t *testing.T, m Model, _ *time.Time) int {
				n := 0
				for _, ev := range []EngineEventMsg{reviewGate("a"), questionGate("b"), reviewGate("c")} {
					var cmd tea.Cmd
					m, cmd = m.Update(ev)
					n += countBells(t, cmd)
				}
				if len(m.inputQueue) != 3 {
					t.Fatalf("queue len = %d, want 3", len(m.inputQueue))
				}
				return n
			},
			want: 1,
		},
		{
			name: "refill inside cooldown is silent",
			bell: &on,
			run: func(t *testing.T, m Model, now *time.Time) int {
				m, cmd := m.Update(reviewGate("a"))
				n := countBells(t, cmd)
				m.inputQueue = nil
				*now = now.Add(gateBellCooldown - time.Second)
				_, cmd = m.Update(questionGate("b"))
				return n + countBells(t, cmd)
			},
			want: 1,
		},
		{
			name: "refill after cooldown rings again",
			bell: &on,
			run: func(t *testing.T, m Model, now *time.Time) int {
				m, cmd := m.Update(reviewGate("a"))
				n := countBells(t, cmd)
				m.inputQueue = nil
				*now = now.Add(gateBellCooldown)
				_, cmd = m.Update(questionGate("b"))
				return n + countBells(t, cmd)
			},
			want: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := pinBellClock(t)
			m := newBellMonitor(t, tt.bell)
			if got := tt.run(t, m, now); got != tt.want {
				t.Fatalf("bells = %d, want %d", got, tt.want)
			}
		})
	}
}

var bellKey = tea.KeyPressMsg{Code: 'B', Text: "B"}

// bellHelp returns the bell binding's help label from the palette catalog,
// or "" when absent.
func bellHelp(m Model) string {
	for _, sec := range m.PaletteSections() {
		for _, c := range palette.FromBindings(sec.Title, sec.Bindings) {
			if c.Binding == "B" {
				return c.Title
			}
		}
	}
	return ""
}

func TestBellToggle(t *testing.T) {
	on := true
	tests := []struct {
		name  string
		focus focusRegion
	}{
		{"steps", focusSteps},
		{"transcript", focusTranscript},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pinBellClock(t)
			m := newBellMonitor(t, &on)
			m.focus = tt.focus
			if got := bellHelp(m); got != "bell: on" {
				t.Fatalf("initial help = %q, want %q", got, "bell: on")
			}

			m, _ = m.Update(bellKey)
			if m.bellEnabled {
				t.Fatal("B did not turn the bell off")
			}
			if got := bellHelp(m); got != "bell: off" {
				t.Fatalf("help after toggle = %q, want %q", got, "bell: off")
			}
			if _, cmd := m.Update(reviewGate("a")); countBells(t, cmd) != 0 {
				t.Fatal("gate rang after the bell was toggled off")
			}

			m, _ = m.Update(bellKey)
			if !m.bellEnabled {
				t.Fatal("second B did not turn the bell back on")
			}
		})
	}
}

func TestBellToggleIgnoredWhileTyping(t *testing.T) {
	on := true
	m := newBellMonitor(t, &on)
	m, _ = m.Update(EngineEventMsg{Event: engine.PromptRequest{
		RunID: "run-1", StepID: "a", Label: "First", As: "first",
	}})
	m.focus = focusGate
	m.loadActiveTextarea()
	if !m.textareaActive() {
		t.Fatal("precondition: prompt textarea should be capturing text")
	}

	m, _ = m.Update(bellKey)
	if !m.bellEnabled {
		t.Fatal("B typed into the gate textarea toggled the bell")
	}
	if got := m.promptTextarea.Value(); !strings.Contains(got, "B") {
		t.Fatalf("textarea value = %q, want the typed B", got)
	}
}

// TestBellHelpVisibleInSimpleHelp checks the `?` overlay source (root renders
// HelpSections), so the toggle is discoverable without the palette too.
func TestBellHelpVisibleInSimpleHelp(t *testing.T) {
	for _, f := range []focusRegion{focusSteps, focusTranscript} {
		m := newBellMonitor(t, nil)
		m.focus = f
		found := false
		for _, sec := range m.HelpSections() {
			for _, b := range sec.Bindings {
				if b.Help().Key == "B" && b.Help().Desc == "bell: off" {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("focus %v: simple-mode help missing B bell: off", f)
		}
	}
}
