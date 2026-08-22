package question

import (
	"fmt"
	"strings"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"jig/internal/interaction"
	"jig/internal/tui/shared"
)

type phase uint8

const (
	phaseField phase = iota
	phaseCustom
	phaseReview
)

type Model struct {
	request interaction.QuestionRequest
	answers map[string]interaction.Answer

	fieldIdx     int
	optionCursor int
	scrollOffset int
	selected     map[string]bool
	reviewCursor int
	phase        phase
	textarea     textarea.Model
	width        int
	height       int
	response     *interaction.QuestionResponse
}

func New(req interaction.QuestionRequest) Model {
	m := Model{
		request:  req,
		answers:  make(map[string]interaction.Answer),
		selected: make(map[string]bool),
		width:    80,
		height:   10,
	}
	m.loadField()
	return m
}

func (m Model) Request() interaction.QuestionRequest { return m.request }

func (m Model) Resize(width, height int) Model {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	old := m.textarea.Value()
	m.width, m.height = width, height
	if m.phase == phaseCustom || (m.phase == phaseField && m.currentField().Kind == interaction.FieldText) {
		m.buildTextarea()
		m.textarea.SetValue(old)
	}
	return m
}

func (m Model) CapturesText() bool {
	return m.phase == phaseCustom || (m.phase == phaseField && m.currentField().Kind == interaction.FieldText)
}

func (m Model) Response() (interaction.QuestionResponse, bool) {
	if m.response == nil {
		return interaction.QuestionResponse{}, false
	}
	return *m.response, true
}

func (m Model) Update(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.response != nil {
		return m, nil
	}
	if m.CapturesText() {
		return m.updateText(msg)
	}
	switch m.phase {
	case phaseReview:
		return m.updateReview(msg)
	default:
		return m.updateSelect(msg)
	}
}

func (m Model) updateText(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.phase == phaseCustom {
			m.phase = phaseField
			m.buildTextarea()
			return m, nil
		}
		return m.finish(interaction.ActionCancel), nil
	case "ctrl+d":
		return m.finish(interaction.ActionDecline), nil
	case "enter":
		value := strings.TrimSpace(m.textarea.Value())
		field := m.currentField()
		if value == "" && field.Required {
			return m, nil
		}
		if value == "" {
			delete(m.answers, field.ID)
		} else if m.phase == phaseCustom {
			m.answers[field.ID] = interaction.Answer{Custom: value}
		} else {
			m.answers[field.ID] = interaction.Answer{Values: []string{value}}
		}
		return m.advance(), nil
	}
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

func (m Model) updateSelect(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	field := m.currentField()
	count := len(field.Options)
	if field.AllowCustom {
		count++
	}
	switch msg.String() {
	case "q", "esc":
		return m.finish(interaction.ActionCancel), nil
	case "d":
		return m.finish(interaction.ActionDecline), nil
	case "up", "k":
		if m.optionCursor > 0 {
			m.optionCursor--
			m.ensureCursorVisible(count)
		}
	case "down", "j":
		if m.optionCursor < count-1 {
			m.optionCursor++
			m.ensureCursorVisible(count)
		}
	case "left", "b":
		m = m.previous()
	case " ", "space":
		if field.Kind == interaction.FieldMultiSelect && m.optionCursor < len(field.Options) {
			value := field.Options[m.optionCursor].Value
			m.selected[value] = !m.selected[value]
		}
	case "enter":
		if field.AllowCustom && m.optionCursor == len(field.Options) {
			m.phase = phaseCustom
			m.buildTextarea()
			return m, textarea.Blink
		}
		switch field.Kind {
		case interaction.FieldSingleSelect:
			if m.optionCursor < len(field.Options) {
				m.answers[field.ID] = interaction.Answer{Values: []string{field.Options[m.optionCursor].Value}}
				m = m.advance()
			}
		case interaction.FieldMultiSelect:
			var values []string
			for _, option := range field.Options {
				if m.selected[option.Value] {
					values = append(values, option.Value)
				}
			}
			if len(values) == 0 && field.Required {
				return m, nil
			}
			if len(values) == 0 {
				delete(m.answers, field.ID)
			} else {
				m.answers[field.ID] = interaction.Answer{Values: values}
			}
			m = m.advance()
		}
	}
	return m, nil
}

func (m Model) updateReview(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		return m.finish(interaction.ActionCancel), nil
	case "d":
		return m.finish(interaction.ActionDecline), nil
	case "up", "k":
		if m.reviewCursor > 0 {
			m.reviewCursor--
		}
	case "down", "j":
		if m.reviewCursor < len(m.request.Fields)-1 {
			m.reviewCursor++
		}
	case "left", "b":
		m.fieldIdx = len(m.request.Fields) - 1
		m.phase = phaseField
		m.loadField()
	case "e":
		m.fieldIdx = m.reviewCursor
		m.phase = phaseField
		m.loadField()
	case "enter", "s":
		return m.finish(interaction.ActionAccept), nil
	}
	return m, nil
}

func (m Model) advance() Model {
	m.fieldIdx++
	if m.fieldIdx >= len(m.request.Fields) {
		m.fieldIdx = len(m.request.Fields) - 1
		m.reviewCursor = 0
		m.phase = phaseReview
		m.textarea = textarea.Model{}
		return m
	}
	m.phase = phaseField
	m.loadField()
	return m
}

func (m Model) previous() Model {
	if m.fieldIdx == 0 {
		return m
	}
	m.fieldIdx--
	m.phase = phaseField
	m.loadField()
	return m
}

func (m *Model) loadField() {
	m.optionCursor = 0
	m.scrollOffset = 0
	m.selected = make(map[string]bool)
	field := m.currentField()
	if answer, ok := m.answers[field.ID]; ok {
		for _, value := range answer.Values {
			m.selected[value] = true
		}
		if field.Kind == interaction.FieldSingleSelect && len(answer.Values) > 0 {
			for i, option := range field.Options {
				if option.Value == answer.Values[0] {
					m.optionCursor = i
					break
				}
			}
		}
	}
	m.buildTextarea()
}

func (m *Model) buildTextarea() {
	field := m.currentField()
	placeholder := field.Prompt
	if m.phase == phaseCustom {
		placeholder = "Other answer…"
	}
	rows := 3
	if m.height < rows+3 {
		rows = 1
	}
	m.textarea = shared.NewInputTextarea(placeholder, m.width, rows, shared.WithoutBorder())
	if answer, ok := m.answers[field.ID]; ok {
		if m.phase == phaseCustom {
			m.textarea.SetValue(answer.Custom)
		} else if field.Kind == interaction.FieldText && len(answer.Values) > 0 {
			m.textarea.SetValue(answer.Values[0])
		}
	}
}

func (m Model) finish(action interaction.ResponseAction) Model {
	answers := make(map[string]interaction.Answer)
	if action == interaction.ActionAccept {
		for key, answer := range m.answers {
			answers[key] = interaction.Answer{
				Values: append([]string(nil), answer.Values...),
				Custom: answer.Custom,
			}
		}
	}
	resp := interaction.QuestionResponse{RequestID: m.request.ID, Action: action, Answers: answers}
	if err := resp.Validate(m.request); err != nil {
		return m
	}
	m.response = &resp
	return m
}

func (m Model) currentField() interaction.QuestionField {
	if len(m.request.Fields) == 0 {
		return interaction.QuestionField{}
	}
	idx := m.fieldIdx
	if idx < 0 {
		idx = 0
	}
	if idx >= len(m.request.Fields) {
		idx = len(m.request.Fields) - 1
	}
	return m.request.Fields[idx]
}

func (m *Model) ensureCursorVisible(count int) {
	visible := m.optionRows()
	if m.optionCursor < m.scrollOffset {
		m.scrollOffset = m.optionCursor
	}
	if m.optionCursor >= m.scrollOffset+visible {
		m.scrollOffset = m.optionCursor - visible + 1
	}
	maxOffset := count - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.scrollOffset > maxOffset {
		m.scrollOffset = maxOffset
	}
}

func (m Model) optionRows() int {
	rows := m.height - 5
	if rows < 1 {
		return 1
	}
	return rows
}

func (m Model) answerText(field interaction.QuestionField) string {
	answer, ok := m.answers[field.ID]
	if !ok {
		return "(skipped)"
	}
	if answer.Custom != "" {
		return answer.Custom
	}
	labels := make([]string, 0, len(answer.Values))
	for _, value := range answer.Values {
		label := value
		for _, option := range field.Options {
			if option.Value == value {
				label = option.Label
				break
			}
		}
		labels = append(labels, label)
	}
	return strings.Join(labels, ", ")
}

func (m Model) Hint() string {
	return shared.HintString(m.HelpBindings()...)
}

func (m Model) HelpBindings() []keybind.Binding {
	binding := func(keys []string, key, desc string) keybind.Binding {
		return keybind.NewBinding(keybind.WithKeys(keys...), keybind.WithHelp(key, desc))
	}
	if m.CapturesText() {
		return []keybind.Binding{
			binding([]string{"enter"}, "enter", "submit"),
			binding([]string{"ctrl+d"}, "ctrl+d", "decline"),
			binding([]string{"esc"}, "esc", "back/cancel"),
		}
	}

	navigate := binding([]string{"up", "down", "j", "k"}, "↑/↓/j/k", "navigate")
	previous := binding([]string{"left", "b"}, "←/b", "previous")
	decline := binding([]string{"d"}, "d", "decline")
	cancel := binding([]string{"q", "esc"}, "q/esc", "cancel")
	if m.phase == phaseReview {
		return []keybind.Binding{
			binding([]string{"enter", "s"}, "enter/s", "submit"),
			navigate,
			binding([]string{"e"}, "e", "edit"),
			previous,
			decline,
			cancel,
		}
	}

	selectBinding := binding([]string{"enter"}, "enter", "select")
	bindings := []keybind.Binding{selectBinding, navigate}
	if m.currentField().Kind == interaction.FieldMultiSelect {
		bindings[0].SetHelp("enter", "next")
		bindings = append(bindings, binding([]string{" ", "space"}, "space", "toggle"))
	}
	return append(bindings, previous, decline, cancel)
}

func (m Model) String() string {
	return fmt.Sprintf("question %s field %d", m.request.ID, m.fieldIdx)
}
