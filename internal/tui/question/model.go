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
	request      interaction.QuestionRequest
	answers      map[string]interaction.Answer
	customDrafts map[string]string

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

	// stacked and its fields back Option B: when the whole request's fields
	// fit the panel together, they're all shown at once instead of paged
	// one-at-a-time (see stackedEligible, updateStacked, stackedLines).
	stacked         bool
	focusFieldIdx   int
	stackedCursor   map[string]int
	stackedSelected map[string]map[string]bool
	// customFromStacked marks a phaseCustom detour started from the stacked
	// view (updateStacked's "o" key) so its enter/esc return to the stacked
	// view instead of advance()-ing to the next field like the paginated flow.
	customFromStacked bool
}

// New builds a panel for req. Every select-kind field always lets the user
// type an answer other than the presented options — the panel doesn't defer
// to the request's AllowCustom flag, since a human should never be stuck
// picking the closest-but-wrong option. req.Fields is copied before this
// normalization so the caller's original request is left untouched.
func New(req interaction.QuestionRequest) Model {
	req.Fields = append([]interaction.QuestionField(nil), req.Fields...)
	for i := range req.Fields {
		if req.Fields[i].Kind != interaction.FieldText {
			req.Fields[i].AllowCustom = true
		}
	}
	m := Model{
		request:      req,
		answers:      make(map[string]interaction.Answer),
		customDrafts: make(map[string]string),
		selected:     make(map[string]bool),
		width:        80,
		height:       10,
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
	m.stacked = stackedEligible(m.request, height)
	if m.stacked && m.stackedCursor == nil {
		m.stackedCursor = make(map[string]int)
		m.stackedSelected = make(map[string]map[string]bool)
	}
	if m.phase == phaseCustom || (m.phase == phaseField && m.currentField().Kind == interaction.FieldText) {
		m.buildTextarea()
		m.textarea.SetValue(old)
	}
	return m
}

// stackedEligible reports whether every field of req can be shown together
// (Option B) within height rows: no free-text fields (those need a full
// textarea/phase of their own — a custom answer for a select field instead
// makes a temporary, self-returning detour into that same phase, see
// updateStacked's "o" handling), and the combined prompt + option rows fit
// the height the host actually gave this panel — so eligibility
// self-adjusts to whatever gateBodyHeight() budgets instead of duplicating
// that constant here.
func stackedEligible(req interaction.QuestionRequest, height int) bool {
	if len(req.Fields) < 2 {
		return false
	}
	rows := 1 // "Answer all N" header line
	for _, field := range req.Fields {
		if field.Kind == interaction.FieldText || len(field.Options) == 0 {
			return false
		}
		rows += 2 + len(field.Options) // blank spacer + prompt line + one row per option
	}
	return rows <= height
}

func (m Model) CapturesText() bool {
	return m.phase == phaseCustom || (m.phase == phaseField && m.currentField().Kind == interaction.FieldText)
}

func (m Model) HasInnerBack() bool {
	return m.phase == phaseCustom
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
	switch {
	case m.phase == phaseReview:
		return m.updateReview(msg)
	case m.stacked:
		return m.updateStacked(msg)
	default:
		return m.updateSelect(msg)
	}
}

func (m Model) updateText(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.phase == phaseCustom {
			m.customDrafts[m.currentField().ID] = m.textarea.Value()
			m.phase = phaseField
			m.customFromStacked = false
			m.buildTextarea()
		}
		return m, nil
	case "ctrl+g":
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
			delete(m.customDrafts, field.ID)
		} else {
			m.answers[field.ID] = interaction.Answer{Values: []string{value}}
		}
		if m.customFromStacked {
			m.customFromStacked = false
			m.phase = phaseField
			return m, nil
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
	case "q":
		return m.finish(interaction.ActionCancel), nil
	case "esc":
		return m, nil
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
	case "b":
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

// updateStacked handles input while every field is shown together (Option
// B). Arrow keys move the option cursor within the focused field; tab/
// shift+tab move focus between fields; space toggles a multi-select option
// under the cursor; enter validates and submits every field's answer at
// once, derived from stackedCursor/stackedSelected by stackedAnswers.
func (m Model) updateStacked(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	fields := m.request.Fields
	field := fields[m.focusFieldIdx]
	cursor := m.stackedCursor[field.ID]
	switch msg.String() {
	case "q":
		return m.finish(interaction.ActionCancel), nil
	case "esc":
		return m, nil
	case "d":
		return m.finish(interaction.ActionDecline), nil
	case "up", "k":
		if cursor > 0 {
			m.stackedCursor[field.ID] = cursor - 1
		}
	case "down", "j":
		if cursor < len(field.Options)-1 {
			m.stackedCursor[field.ID] = cursor + 1
		}
	case "tab":
		m.focusFieldIdx = (m.focusFieldIdx + 1) % len(fields)
	case "shift+tab":
		m.focusFieldIdx = (m.focusFieldIdx - 1 + len(fields)) % len(fields)
	case " ", "space":
		if field.Kind == interaction.FieldMultiSelect {
			if m.stackedSelected[field.ID] == nil {
				m.stackedSelected[field.ID] = make(map[string]bool)
			}
			value := field.Options[cursor].Value
			m.stackedSelected[field.ID][value] = !m.stackedSelected[field.ID][value]
		}
	case "o":
		// Detour into a one-off custom-answer textarea for the focused field;
		// enter/esc there return to this stacked view (see customFromStacked).
		m.fieldIdx = m.focusFieldIdx
		m.phase = phaseCustom
		m.customFromStacked = true
		m.buildTextarea()
		return m, textarea.Blink
	case "enter":
		if answers, ok := m.stackedAnswers(); ok {
			m.answers = answers
			return m.finish(interaction.ActionAccept), nil
		}
	}
	return m, nil
}

// stackedAnswers derives the answer set for every field from the live
// stacked cursor/selection state. A single-select field's answer is
// whichever option its cursor currently sits on (defaulting to the first
// option — there is no separate "committed" state in the stacked view,
// unlike the paginated one-question-at-a-time flow), unless the field has a
// recorded custom override (the "o" detour in updateStacked), which always
// wins. It reports ok=false if a required field has no answer, mirroring
// updateSelect's enter no-op.
func (m Model) stackedAnswers() (map[string]interaction.Answer, bool) {
	answers := make(map[string]interaction.Answer, len(m.request.Fields))
	for _, field := range m.request.Fields {
		if answer, ok := m.answers[field.ID]; ok && answer.Custom != "" {
			answers[field.ID] = answer
			continue
		}
		switch field.Kind {
		case interaction.FieldSingleSelect:
			idx := m.stackedCursor[field.ID]
			if idx < 0 || idx >= len(field.Options) {
				if field.Required {
					return nil, false
				}
				continue
			}
			answers[field.ID] = interaction.Answer{Values: []string{field.Options[idx].Value}}
		case interaction.FieldMultiSelect:
			var values []string
			for _, option := range field.Options {
				if m.stackedSelected[field.ID][option.Value] {
					values = append(values, option.Value)
				}
			}
			if len(values) == 0 {
				if field.Required {
					return nil, false
				}
				continue
			}
			answers[field.ID] = interaction.Answer{Values: values}
		}
	}
	return answers, true
}

// stackedHasMultiSelect reports whether the request has any multi-select
// field, so HelpBindings only advertises "space toggle" when it does
// something.
func stackedHasMultiSelect(req interaction.QuestionRequest) bool {
	for _, field := range req.Fields {
		if field.Kind == interaction.FieldMultiSelect {
			return true
		}
	}
	return false
}

func (m Model) updateReview(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m.finish(interaction.ActionCancel), nil
	case "esc":
		return m, nil
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
	case "b":
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
		placeholder = "Other answer" + shared.EllipsisGlyph
	}
	rows := 3
	if m.height < rows+3 {
		rows = 1
	}
	m.textarea = shared.NewInputTextarea(placeholder, m.width, rows, shared.WithoutBorder())
	if m.phase == phaseCustom {
		if draft, ok := m.customDrafts[field.ID]; ok {
			m.textarea.SetValue(draft)
			return
		}
	}
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
		bindings := []keybind.Binding{
			binding([]string{"enter"}, "enter", "submit"),
			binding([]string{"ctrl+d"}, "ctrl+d", "decline"),
			binding([]string{"ctrl+g"}, "ctrl+g", "cancel"),
		}
		if m.HasInnerBack() {
			bindings = append(bindings, binding([]string{"esc"}, "esc", "back"))
		}
		return bindings
	}

	navigate := binding([]string{"up", "down", "j", "k"}, "↑/↓/j/k", "navigate")
	previous := binding([]string{"b"}, "b", "previous")
	decline := binding([]string{"d"}, "d", "decline")
	cancel := binding([]string{"q"}, "q", "cancel")
	if m.stacked {
		bindings := []keybind.Binding{
			binding([]string{"enter"}, "enter", "confirm all"),
			binding([]string{"tab", "shift+tab"}, "tab", "next field"),
			navigate,
			binding([]string{"o"}, "o", "type answer"),
		}
		if stackedHasMultiSelect(m.request) {
			bindings = append(bindings, binding([]string{" ", "space"}, "space", "toggle"))
		}
		return append(bindings, decline, cancel)
	}
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
