package monitor

import (
	"fmt"
	"path/filepath"
	"strings"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"jig/internal/sentinel"
	"jig/internal/tui/shared"
)

const securityMaxHeight = 5

// helpOverlay composites the help chat modal over the base layout using the
// same Compositor technique as RenderHelpOverlay. The modal takes 60% of the
// width and 80% of the height, centered.
func (m Model) helpOverlay(base string) string {
	boxW := m.width * 60 / 100
	boxH := m.height * 80 / 100
	if boxW < 40 {
		boxW = 40
	}
	if boxH < 10 {
		boxH = 10
	}
	box := m.helpModel.View(boxW, boxH, false)
	x := (m.width - lipgloss.Width(box)) / 2
	y := (m.height - lipgloss.Height(box)) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	comp := lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	)
	return lipgloss.NewCanvas(m.width, m.height).Compose(comp).Render()
}

func (m Model) gateOverlayView(base string, layout verticalLayout) string {
	if m.focus != focusGate || !m.hasGate() {
		return base
	}

	available := max(m.height-layout.inputH-layout.footerH, 0)
	gate := m.gateOverlay()
	overlayH := min(lipgloss.Height(gate), available)
	if overlayH < 1 {
		return base
	}
	overlay := fitBlock(gate, m.width, overlayH)
	y := available - overlayH
	comp := lipgloss.NewCompositor(
		lipgloss.NewLayer(base),
		lipgloss.NewLayer(overlay).Y(y).Z(1),
	)
	return lipgloss.NewCanvas(m.width, m.height).Compose(comp).Render()
}

func (m Model) securityViewHeight(available int) int {
	if len(m.secFindings) == 0 || available < 1 || m.width < 1 {
		return 0
	}
	return min(1+len(m.secFindings), securityMaxHeight, available)
}

// securityView renders a bounded summary rather than letting an arbitrarily
// large findings file displace the gate and footer.
func (m Model) securityView(height int) string {
	if len(m.secFindings) == 0 || height < 1 || m.width < 1 {
		return ""
	}

	header := fmt.Sprintf("Security findings (%d)", len(m.secFindings))
	lines := []string{shared.Theme.Security.Header.MaxWidth(m.width).Render(header)}

	visible := min(len(m.secFindings), height-1)
	overflow := len(m.secFindings) > visible
	if overflow && height > 1 {
		visible = max(height-2, 0)
	}
	for _, f := range m.secFindings[:visible] {
		sev := strings.ToUpper(string(f.Severity))
		label := "[" + sev + "] " + f.Monitor + ": "
		detail := f.Detail
		if detail == "" {
			detail = string(f.Action)
		}
		var row string
		switch f.Severity {
		case sentinel.SeverityCritical:
			row = shared.Theme.Security.CriticalRow.MaxWidth(m.width).Render(label + detail)
		case sentinel.SeverityHigh:
			row = shared.Theme.Security.HighRow.MaxWidth(m.width).Render(label + detail)
		case sentinel.SeverityMedium:
			row = shared.Theme.Security.MediumRow.MaxWidth(m.width).Render(label + detail)
		default:
			row = shared.Theme.Security.LowRow.MaxWidth(m.width).Render(label + detail)
		}
		lines = append(lines, row)
	}
	if overflow && height > 1 {
		remaining := len(m.secFindings) - visible
		more := fmt.Sprintf("… %d more finding", remaining)
		if remaining != 1 {
			more += "s"
		}
		lines = append(lines, shared.Theme.Security.LowRow.MaxWidth(m.width).Render(more))
	}
	return strings.Join(lines, "\n")
}

func fitBlock(s string, width, height int) string {
	if s == "" || width < 1 || height < 1 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "")
	}
	return strings.Join(lines, "\n")
}

func joinVertical(parts ...string) string {
	visible := parts[:0]
	for _, part := range parts {
		if part != "" {
			visible = append(visible, part)
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, visible...)
}

// statusLabel computes the status text shown in the footer.
func (m Model) statusLabel() string {
	if m.done {
		if m.failed {
			return shared.Theme.Error.Render("failed")
		}
		return shared.Theme.Valid.Render("done")
	}
	entry, ok := m.activeEntry()
	if !ok {
		return shared.Theme.Running.Render("running")
	}
	n := len(m.inputQueue)
	queueSuffix := ""
	if n > 1 {
		queueSuffix = fmt.Sprintf(" (%d pending)", n)
	}
	switch entry.kind {
	case inputKindRequest:
		return shared.Theme.Marker.Render("awaiting agent input" + queueSuffix)
	case inputKindQuestion:
		return shared.Theme.Marker.Render("awaiting answer" + queueSuffix)
	case inputKindPrompt:
		return shared.Theme.Marker.Render("awaiting user input" + queueSuffix)
	case inputKindReview:
		if entry.composing {
			return shared.Theme.Marker.Render("composing message")
		}
		return shared.Theme.Marker.Render("awaiting review")
	case inputKindRecovery:
		if entry.composing {
			return shared.Theme.Marker.Render("composing guidance")
		}
		return shared.Theme.Error.Render("step failed — recovery" + queueSuffix)
	case inputKindIntegrationConflict:
		return shared.Theme.Error.Render("integration conflict" + queueSuffix)
	case inputKindFinalMerge:
		return shared.Theme.Marker.Render("awaiting final merge" + queueSuffix)
	case inputKindResetConfirm:
		return shared.Theme.Marker.Render("awaiting reset confirmation" + queueSuffix)
	case inputKindHelpFinalMerge:
		return shared.Theme.Marker.Render("awaiting merge approval" + queueSuffix)
	}
	return shared.Theme.Running.Render("running")
}

func (m Model) hintLabel(width int) string {
	bindings := m.compactHelpBindings()
	if m.hasGate() && m.focus != focusGate {
		gate := m.keys.FocusNext
		gate.SetHelp("tab", "gate")
		bindings = append([]keybind.Binding{gate}, bindings...)
	}
	return shared.CompactHint(width, shared.MoreHelpBinding(m.CapturesText()), bindings...)
}

func (m Model) footerView() string {
	status := m.statusLabel()
	prefix := "  " + status + "  ·  "
	hint := m.hintLabel(max(m.width-lipgloss.Width(prefix), 0))
	// The per-run cost/token total lives at the bottom of the Steps panel
	// (listBody's Total row), not the footer.
	f := shared.Theme.Footer
	if m.width > 0 {
		f = f.MaxWidth(m.width)
	}
	return f.Render(prefix + hint)
}

type contentKind uint8

const (
	contentTranscript contentKind = iota
	contentReview
	contentReviewDiff
	contentFile
)

type contentContext struct {
	kind   contentKind
	stepID string
	label  string
}

func (m Model) selectedContent() contentContext {
	if m.selKind == "file" && m.selFile != "" {
		stepID, file, ok := m.selectedOutputFile()
		if ok {
			return contentContext{kind: contentFile, stepID: stepID, label: file.displayLabel()}
		}
		return contentContext{kind: contentFile, stepID: m.chatStep, label: filepath.Base(m.selFile)}
	}

	if review, ok := m.reviews[m.chatStep]; ok && len(m.chatEntries) == 0 {
		if review.Diff != "" {
			return contentContext{kind: contentReviewDiff, stepID: m.chatStep, label: "Review diff"}
		}
		return contentContext{kind: contentReview, stepID: m.chatStep, label: "Review"}
	}

	label := "Transcript"
	if m.showsTranscriptFollow() {
		switch {
		case m.chatAutoScroll:
			label += " · LIVE"
		case m.unseenChatEntries() > 0:
			label += fmt.Sprintf(" · PAUSED · %d new", m.unseenChatEntries())
		default:
			label += " · PAUSED"
		}
	}
	return contentContext{kind: contentTranscript, stepID: m.chatStep, label: label}
}

func (m Model) selectedOutputFile() (string, outputFile, bool) {
	rows := m.visibleRows()
	if m.cursor >= 0 && m.cursor < len(rows) {
		row := rows[m.cursor]
		if row.file != nil && row.file.path == m.selFile {
			return row.stepID, *row.file, true
		}
	}
	for _, step := range m.steps {
		for _, file := range m.stepFiles[step.id] {
			if file.path == m.selFile {
				return step.id, file, true
			}
		}
	}
	return "", outputFile{}, false
}

func shortRunID(runID string) string {
	runes := []rune(runID)
	if len(runes) <= 8 {
		return runID
	}
	return string(runes[len(runes)-8:])
}

func (m Model) runIdentity() string {
	runID := shortRunID(m.RunID)
	switch {
	case runID != "" && m.workflow != "":
		return runID + " · " + m.workflow
	case runID != "":
		return runID
	case m.workflow != "":
		return m.workflow
	default:
		return "Run"
	}
}

func (m Model) transcriptPanelTitle() string {
	return m.selectedContent().label
}

func (m Model) stepsPanelTitleParts() []string {
	return []string{m.runIdentity(), "Steps"}
}

func (m Model) transcriptPanelTitleParts() []string {
	content := m.selectedContent()
	return []string{m.runIdentity(), content.stepID, content.label}
}

// View lays the monitor out as two side-by-side titled panels (Steps + the
// selected step's transcript) with the input bar and footer beneath. Below the
// narrow threshold only the focused panel renders full-width (Resolved
// Decision 14). Only the focused region's border is drawn primary.
func (m Model) View() string {
	if !m.ready {
		return "\n  Loading…\n"
	}
	if m.width < 1 || m.height < 1 {
		return ""
	}

	layout := m.verticalLayout()
	footer := fitBlock(m.footerView(), m.width, layout.footerH)
	inputBar := fitBlock(m.inputBarView(), m.width, layout.inputH)

	leftTitle := m.stepsPanelTitleParts()
	rightTitle := m.transcriptPanelTitleParts()

	var panels string
	if layout.panelH == 0 {
		panels = ""
	} else if m.narrow {
		// Single-panel fallback: render only the focused panel full-width.
		if m.focus == focusTranscript {
			panels = shared.BreadcrumbPanel(rightTitle, m.chatVP.View(), m.width, layout.panelH, true)
		} else {
			// Steps or Gate focus shows the Steps panel (the gate has its own strip).
			panels = shared.BreadcrumbPanel(leftTitle, m.vp.View(), m.width, layout.panelH, m.focus == focusSteps)
		}
	} else {
		stepsW, transcriptW, _ := panelSplit(m.width)
		left := shared.BreadcrumbPanel(leftTitle, m.vp.View(), stepsW, layout.panelH, m.focus == focusSteps)
		right := shared.BreadcrumbPanel(rightTitle, m.chatVP.View(), transcriptW, layout.panelH, m.focus == focusTranscript)
		panels = lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	}
	panels = fitBlock(panels, m.width, layout.panelH)

	sec := fitBlock(m.securityView(layout.securityH), m.width, layout.securityH)
	base := fitBlock(joinVertical(panels, sec, inputBar, footer), m.width, m.height)
	base = m.gateOverlayView(base, layout)
	if m.helpOpen {
		return m.helpOverlay(base)
	}
	return base
}
