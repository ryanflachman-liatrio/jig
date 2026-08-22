package question

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jig/internal/interaction"
)

func testRequest() interaction.QuestionRequest {
	return interaction.QuestionRequest{
		ID: "req-1",
		Fields: []interaction.QuestionField{
			{
				ID: "format", Header: "Format", Prompt: "Choose a format", Kind: interaction.FieldSingleSelect,
				Options:     []interaction.QuestionOption{{Value: "json", Label: "JSON"}, {Value: "text", Label: "Text"}},
				AllowCustom: true,
			},
			{
				ID: "features", Prompt: "Choose features", Kind: interaction.FieldMultiSelect,
				Options: []interaction.QuestionOption{{Value: "cache", Label: "Cache"}, {Value: "retry", Label: "Retry"}},
			},
		},
	}
}

func press(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case " ":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	default:
		return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
	}
}

func TestQuestionPanelReviewAndSubmit(t *testing.T) {
	m := New(testRequest()).Resize(60, 10)
	m, _ = m.Update(press("down"))
	m, _ = m.Update(press("enter"))
	m, _ = m.Update(press(" "))
	m, _ = m.Update(press("down"))
	m, _ = m.Update(press(" "))
	m, _ = m.Update(press("enter"))

	if !strings.Contains(m.View(), "Review answers") {
		t.Fatalf("View() did not enter review:\n%s", m.View())
	}
	m, _ = m.Update(press("enter"))
	resp, ok := m.Response()
	if !ok {
		t.Fatal("submit did not produce a response")
	}
	if resp.Action != interaction.ActionAccept {
		t.Fatalf("Action = %q, want accept", resp.Action)
	}
	if got := resp.Answers["format"].Values; len(got) != 1 || got[0] != "text" {
		t.Fatalf("format answer = %v, want [text]", got)
	}
	if got := resp.Answers["features"].Values; len(got) != 2 {
		t.Fatalf("features answer = %v, want two values", got)
	}
}

func TestQuestionPanelCustomAnswer(t *testing.T) {
	req := testRequest()
	req.Fields = req.Fields[:1]
	m := New(req)
	m, _ = m.Update(press("down"))
	m, _ = m.Update(press("down"))
	m, _ = m.Update(press("enter"))
	if !m.CapturesText() {
		t.Fatal("Other did not open a textarea")
	}
	for _, r := range "custom" {
		m, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m, _ = m.Update(press("enter"))
	m, _ = m.Update(press("enter"))
	resp, ok := m.Response()
	if !ok {
		t.Fatal("submit did not produce a response")
	}
	if got := resp.Answers["format"].Custom; got != "custom" {
		t.Fatalf("custom answer = %q, want custom", got)
	}
}

func TestQuestionPanelDeclineAndCancelAreDistinct(t *testing.T) {
	req := testRequest()
	req.Fields = req.Fields[:1]
	for _, tc := range []struct {
		key  string
		want interaction.ResponseAction
	}{
		{key: "d", want: interaction.ActionDecline},
		{key: "q", want: interaction.ActionCancel},
	} {
		t.Run(tc.key, func(t *testing.T) {
			m, _ := New(req).Update(press(tc.key))
			resp, ok := m.Response()
			if !ok || resp.Action != tc.want {
				t.Fatalf("response = %+v, %v; want %s", resp, ok, tc.want)
			}
		})
	}
}

func TestQuestionHelpBindingsFollowPhase(t *testing.T) {
	m := New(testRequest())
	if hint := m.Hint(); !strings.Contains(hint, "enter select") || !strings.Contains(hint, "q/esc cancel") {
		t.Fatalf("selection hint = %q", hint)
	}

	m, _ = m.Update(press("down"))
	m, _ = m.Update(press("down"))
	m, _ = m.Update(press("enter"))
	if hint := m.Hint(); !strings.Contains(hint, "enter submit") || !strings.Contains(hint, "ctrl+d decline") {
		t.Fatalf("text hint = %q", hint)
	}

	m = New(testRequest())
	m, _ = m.Update(press("enter"))
	if hint := m.Hint(); !strings.Contains(hint, "space toggle") || !strings.Contains(hint, "enter next") {
		t.Fatalf("multi-select hint = %q", hint)
	}
}
