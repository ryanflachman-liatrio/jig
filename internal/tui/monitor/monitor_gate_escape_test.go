package monitor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/interaction"
)

func gateHelpContains(m Model, key, desc string) bool {
	for _, binding := range m.gateHelpSection().Bindings {
		help := binding.Help()
		if binding.Enabled() && help.Key == key && help.Desc == desc {
			return true
		}
	}
	return false
}

func TestGateEscapeBlursTopLevelQuestionPhases(t *testing.T) {
	selectField := selectQuestion(
		"pick", "", "Pick one", false,
		interaction.QuestionOption{Value: "one", Label: "One"},
	)
	textField := interaction.QuestionField{
		ID: "answer", Prompt: "Answer", Kind: interaction.FieldText, Required: true,
	}

	tests := []struct {
		name  string
		field interaction.QuestionField
		setup func(Model) Model
	}{
		{name: "select", field: selectField},
		{name: "text", field: textField},
		{
			name:  "review",
			field: selectField,
			setup: func(m Model) Model {
				m, _ = m.Update(key("enter"))
				return m
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newMonitorWithSteps(t)
			m, _ = m.Update(EngineEventMsg{Event: questionEvent("run-1", "a", "q1", tc.field)})
			m.focus = focusGate
			if tc.setup != nil {
				m = tc.setup(m)
			}

			if !gateHelpContains(m, "esc", "blur") {
				t.Fatalf("gate help did not advertise esc blur: %+v", m.gateHelpSection().Bindings)
			}
			if gateHelpContains(m, "esc", "cancel") {
				t.Fatal("gate help advertised esc cancel")
			}

			got, cmd := m.Update(key("esc"))
			if cmd != nil {
				t.Fatal("esc emitted a response command")
			}
			if got.focus != focusSteps {
				t.Fatalf("focus after esc = %v, want Steps", got.focus)
			}
			if len(got.inputQueue) != 1 {
				t.Fatalf("queue length after esc = %d, want 1", len(got.inputQueue))
			}
			if _, done := got.inputQueue[0].question.Response(); done {
				t.Fatal("esc resolved the question")
			}
		})
	}
}

func TestGateQuestionCustomEscapeBackPreservesDraft(t *testing.T) {
	field := selectQuestion(
		"pick", "", "Pick one", false,
		interaction.QuestionOption{Value: "one", Label: "One"},
	)
	field.AllowCustom = true

	m := newMonitorWithSteps(t)
	m, _ = m.Update(EngineEventMsg{Event: questionEvent("run-1", "a", "q1", field)})
	m.focus = focusGate
	m, _ = m.Update(key("down"))
	m, _ = m.Update(key("enter"))
	for _, r := range "custom" {
		m, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	if !gateHelpContains(m, "esc", "back") {
		t.Fatalf("custom question help did not advertise esc back: %+v", m.gateHelpSection().Bindings)
	}
	if bar := ansiStrip(m.inputBarView()); !strings.Contains(bar, "esc to back") {
		t.Fatalf("custom question input bar = %q, want esc to back", bar)
	}

	m, cmd := m.Update(key("esc"))
	if cmd != nil {
		t.Fatal("esc emitted a response command")
	}
	if m.focus != focusGate {
		t.Fatalf("focus after inner back = %v, want Gate", m.focus)
	}
	if len(m.inputQueue) != 1 || m.inputQueue[0].question.HasInnerBack() {
		t.Fatal("esc did not return the custom editor to its option list")
	}
	if !gateHelpContains(m, "esc", "blur") {
		t.Fatal("option list did not restore esc blur help")
	}

	m, _ = m.Update(key("enter"))
	if got := m.inputQueue[0].question.View(); !strings.Contains(got, "custom") {
		t.Fatalf("custom draft was not restored:\n%s", got)
	}
}

func TestGateTextQuestionRequiresExplicitCancellation(t *testing.T) {
	field := interaction.QuestionField{
		ID: "answer", Prompt: "Answer", Kind: interaction.FieldText, Required: true,
	}
	m := newMonitorWithSteps(t)
	m, _ = m.Update(EngineEventMsg{Event: questionEvent("run-1", "a", "q1", field)})
	m.focus = focusGate

	m, _ = m.Update(key("q"))
	if len(m.inputQueue) != 1 {
		t.Fatal("typing q removed the pending question")
	}
	if !gateHelpContains(m, "ctrl+g", "cancel") {
		t.Fatalf("text question help did not advertise explicit cancellation: %+v", m.gateHelpSection().Bindings)
	}

	m, cmd := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+g produced no cancellation command")
	}
	response, ok := cmd().(AgentQuestionResponseMsg)
	if !ok || response.Response.Action != interaction.ActionCancel {
		t.Fatalf("ctrl+g response = %+v (%T), want question cancel", response, response)
	}
	if len(m.inputQueue) != 0 {
		t.Fatal("ctrl+g did not remove the cancelled question")
	}
}

func TestGateComposeEscapeBackPreservesDraft(t *testing.T) {
	tests := []struct {
		name    string
		enqueue func(Model) Model
		openKey string
	}{
		{
			name: "recovery guidance",
			enqueue: func(m Model) Model {
				m, _ = m.Update(EngineEventMsg{Event: engine.RecoveryRequest{
					RunID: "run-1", StepID: "a", Err: "failed", CanResume: true,
				}})
				return m
			},
			openKey: "g",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.enqueue(newMonitorWithSteps(t))
			m.focus = focusGate
			m, _ = m.Update(key(tc.openKey))
			m.promptTextarea.SetValue("keep this draft")

			if !gateHelpContains(m, "esc", "back") {
				t.Fatalf("compose help did not advertise esc back: %+v", m.gateHelpSection().Bindings)
			}
			if bar := ansiStrip(m.inputBarView()); !strings.Contains(bar, "esc to back") {
				t.Fatalf("compose input bar = %q, want esc to back", bar)
			}

			m, cmd := m.Update(key("esc"))
			if cmd != nil {
				t.Fatal("esc emitted a response command")
			}
			entry, ok := m.activeEntry()
			if !ok || entry.composing {
				t.Fatal("esc did not return compose mode to its action menu")
			}
			if m.focus != focusGate {
				t.Fatalf("focus after compose back = %v, want Gate", m.focus)
			}
			if entry.draft != "keep this draft" {
				t.Fatalf("saved draft = %q, want preserved text", entry.draft)
			}

			m, _ = m.Update(key(tc.openKey))
			if got := m.promptTextarea.Value(); got != "keep this draft" {
				t.Fatalf("reopened draft = %q, want preserved text", got)
			}
		})
	}
}
