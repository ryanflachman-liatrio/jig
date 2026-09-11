package monitor

import (
	"fmt"
	"path/filepath"
	"strings"

	keybind "charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"jig/internal/sentinel"
	"jig/internal/step"
	"jig/internal/tui/shared"
)

const securityMaxHeight = 5
const wideMonitorTitleMinWidth = 160

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
	if m.focus != focusGate || !m.hasGate() || m.reviewOpen {
		return base
	}

	available := max(m.height-layout.inputH-layout.statusH-layout.footerH, 0)
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
	if m.historical && m.interrupted && !m.hasGate() {
		return shared.Theme.Warning.Render("interrupted")
	}
	entry, ok := m.activeEntry()
	if !ok {
		if m.historical {
			return shared.Theme.Question.Render("paused")
		}
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
		return shared.Theme.Marker.Render("awaiting review" + queueSuffix)
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
	palette := shared.KeyPalette
	palette.SetEnabled(!m.CapturesText())
	bindings = append(bindings, palette)
	return shared.CompactHint(width, shared.MoreHelpBinding(m.CapturesText()), bindings...)
}

func (m Model) footerView() string {
	status := m.statusLabel()
	modeTag := "simple"
	if !m.simpleMode {
		modeTag = "advanced"
	}
	prefix := "  " + status + "  ·  "
	suffix := "  ·  " + modeTag
	hintBudget := max(m.width-lipgloss.Width(prefix)-lipgloss.Width(suffix), 0)
	hint := m.hintLabel(hintBudget)
	// Identity/cost live on the status line (Phase 1.1); the footer keeps state
	// words + keybindings only (C1). Quiet mode tag is a 2.3 affordance.
	f := shared.Theme.Footer
	if m.width > 0 {
		f = f.MaxWidth(m.width)
	}
	line := prefix + hint
	if hintBudget > 0 && lipgloss.Width(line)+lipgloss.Width(suffix) <= m.width {
		line += suffix
	} else if m.width > lipgloss.Width(prefix+modeTag) {
		// Prefer keeping the tag when CompactHint already spilled to "? more".
		line = prefix + hint
		if lipgloss.Width(line)+lipgloss.Width(suffix) <= m.width {
			line += suffix
		}
	}
	return f.Render(line)
}

// statusLineView is the Lazygit-style identity strip between panels/gate and the
// keybinding footer: run · workflow · step · tokens/cost (+ LIVE / N new).
// State words stay in the footer — never duplicate them here (C1).
func (m Model) statusLineView() string {
	parts := make([]string, 0, 6)
	if id := shortRunID(m.RunID); id != "" {
		parts = append(parts, id)
	}
	if m.workflow != "" {
		parts = append(parts, m.workflow)
	}
	if step := m.statusStepID(); step != "" {
		parts = append(parts, step)
	}
	if m.totalTokens > 0 {
		parts = append(parts, humanTokens(m.totalTokens)+" tok")
	}
	if m.totalCost > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f", m.totalCost))
	}
	if m.showsTranscriptFollow() {
		switch {
		case m.chatAutoScroll:
			parts = append(parts, "LIVE")
		case m.unseenChatEntries() > 0:
			parts = append(parts, fmt.Sprintf("%d new", m.unseenChatEntries()))
		}
	}
	// A18: badge the resolved exporter mode so the operator can see at a
	// glance whether OTel is publishing. "off" and unset both hide the badge
	// to match the off-by-default posture.
	if mode := m.telemetryMode; mode != "" && mode != "off" {
		parts = append(parts, "otel:"+mode)
	}
	line := " " + strings.Join(parts, " · ")
	line = truncateStatusLine(line, m.width)
	style := shared.Theme.StatusLine
	if m.width > 0 {
		style = style.MaxWidth(m.width)
	}
	return style.Render(line)
}

// statusStepID picks the step field for the status line (G4): pending gate's
// step if any, else the selected Steps cursor step, else a running step.
func (m Model) statusStepID() string {
	if entry, ok := m.activeEntry(); ok && entry.stepID != "" {
		return entry.stepID
	}
	if rows := m.visibleRows(); m.cursor >= 0 && m.cursor < len(rows) {
		if id := rows[m.cursor].stepID; id != "" {
			return id
		}
	}
	for _, s := range m.steps {
		if s.status == step.StatusRunning {
			return s.id
		}
	}
	if m.chatStep != "" {
		return m.chatStep
	}
	return ""
}

// truncateStatusLine drops cost first, then shortens workflow, then step; the
// run id is never dropped (1.1).
func truncateStatusLine(line string, width int) string {
	if width < 1 || lipgloss.Width(line) <= width {
		return line
	}
	parts := strings.Split(strings.TrimLeft(line, " "), " · ")
	if len(parts) == 0 {
		return ansi.Truncate(line, width, "…")
	}
	dropCost := func(p []string) []string {
		out := make([]string, 0, len(p))
		for _, part := range p {
			if strings.HasPrefix(part, "$") || strings.HasSuffix(part, " tok") {
				continue
			}
			out = append(out, part)
		}
		return out
	}
	parts = dropCost(parts)
	rebuild := func(p []string) string { return " " + strings.Join(p, " · ") }
	if lipgloss.Width(rebuild(parts)) <= width {
		return rebuild(parts)
	}
	// Shorten workflow (index 1 when run id is present).
	if len(parts) >= 2 {
		keep := lipgloss.Width(parts[0]) + 3 // " · "
		for i := 2; i < len(parts); i++ {
			keep += lipgloss.Width(parts[i]) + 3
		}
		budget := width - keep - 1 // leading space
		if budget > 1 {
			parts[1] = shared.TruncateTitle(parts[1], budget)
		} else if len(parts) > 2 {
			parts = append(parts[:1], parts[2:]...)
		}
	}
	if lipgloss.Width(rebuild(parts)) <= width {
		return rebuild(parts)
	}
	// Drop step (and later adornments) until run id fits.
	for len(parts) > 1 && lipgloss.Width(rebuild(parts)) > width {
		// Prefer dropping LIVE/N new, then rightmost identity fields after run id.
		parts = parts[:len(parts)-1]
	}
	return ansi.Truncate(rebuild(parts), width, "…")
}

type contentKind uint8

const (
	contentTranscript contentKind = iota
	contentReview
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

	if _, ok := m.reviews[m.chatStep]; ok && len(m.chatEntries) == 0 {
		return contentContext{kind: contentReview, stepID: m.chatStep, label: "Review"}
	}

	// LIVE / N new live on the status line (1.1 / G5); panel leaf stays the
	// role word so focus badges can replace it cleanly (1.2).
	return contentContext{kind: contentTranscript, stepID: m.chatStep, label: "Transcript"}
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

// badgeFocus is the region that should show a [NAME] badge. None while the
// help-agent modal owns chrome; when review is open the review panel owns the
// badge (even though keys still route through the gate). Otherwise exactly the
// focused region.
func (m Model) badgeFocus() focusRegion {
	if m.helpOpen {
		return focusRegion(-1) // no badge
	}
	if m.reviewOpen {
		return focusRegion(-2) // review panel badge; not a focusRegion value
	}
	return m.focus
}

const focusReviewBadge focusRegion = -2

func (m Model) stepsPanelTitleParts() []string {
	leaf := shared.FocusTitle("Steps", m.badgeFocus() == focusSteps)
	// 2.2: titles prefer shortRun · leaf; workflow lives on the status line.
	// When the Steps panel is very wide, include workflow as an optional middle.
	runID := shortRunID(m.RunID)
	if runID == "" {
		runID = m.runIdentity()
	}
	parts := []string{runID}
	if m.workflow != "" && m.width >= wideMonitorTitleMinWidth && m.workflow != runID {
		parts = append(parts, m.workflow)
	}
	parts = append(parts, leaf)
	return parts
}

func (m Model) transcriptPanelTitleParts() []string {
	content := m.selectedContent()
	focused := m.badgeFocus() == focusTranscript
	leaf := content.label
	if content.kind == contentTranscript {
		leaf = shared.FocusTitle("Transcript", focused)
	}
	// 2.2: stepID · [TRANSCRIPT]; drop run identity (status owns it).
	parts := make([]string, 0, 3)
	if content.stepID != "" {
		parts = append(parts, content.stepID)
	}
	parts = append(parts, leaf)
	// LIVE only when it fits after the badge (optional trailing crumb).
	if content.kind == contentTranscript && m.showsTranscriptFollow() && m.chatAutoScroll {
		candidate := append(append([]string{}, parts...), "LIVE")
		titleW := shared.PanelTitleBudget(m.transcriptOuterWidth())
		if lipgloss.Width(shared.BreadcrumbTitle(candidate, titleW)) <= titleW {
			parts = candidate
		}
	}
	return parts
}

func (m Model) transcriptOuterWidth() int {
	if m.narrow {
		return m.width
	}
	_, transcriptW, _ := panelSplit(m.width)
	return transcriptW
}

// reviewPanelTitle is a ·-joined Monitor chrome title for the open workspace
// (not breadcrumb ›), matching TARGET / 2.1 frames.
func (m Model) reviewPanelTitle() string {
	focused := m.badgeFocus() == focusReviewBadge
	parts := []string{shared.FocusTitle("Review", focused)}
	if entry, ok := m.activeEntry(); ok && entry.workspace != nil {
		parts = append(parts, entry.workspace.TitleSegments()...)
	}
	return strings.Join(parts, " · ")
}

func (m Model) reviewPanelBody() string {
	entry, ok := m.activeEntry()
	if !ok || entry.workspace == nil {
		return ""
	}
	return entry.workspace.EmbeddedView()
}

// View lays the monitor out as two side-by-side titled panels (Steps + the
// selected step's transcript) with the input bar, status line, and footer
// beneath. Below the narrow threshold only the focused panel renders
// full-width (Resolved Decision 14). Only the focused region's border is drawn
// primary.
//
// An open review workspace is a focused mode of the content panel. The status,
// gate bar, and footer preserve run context while the document workspace uses
// the complete panel width.
func (m Model) View() string {
	if !m.ready {
		return shared.RenderEmptyState(shared.EmptyState{
			Title: "Loading run…",
			Body:  "Waiting for the run monitor to finish sizing.",
		})
	}
	if m.width < 1 || m.height < 1 {
		return ""
	}

	layout := m.verticalLayout()
	footer := fitBlock(m.footerView(), m.width, layout.footerH)
	status := fitBlock(m.statusLineView(), m.width, layout.statusH)
	inputBar := fitBlock(m.inputBarView(), m.width, layout.inputH)

	var panels string
	if m.reviewOpen {
		panels = m.reviewPanelsView(layout)
	} else if layout.panelH == 0 {
		panels = ""
	} else {
		leftTitle := m.stepsPanelTitleParts()
		rightTitle := m.transcriptPanelTitleParts()
		if m.narrow {
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
	}
	panels = fitBlock(panels, m.width, layout.panelH)

	sec := fitBlock(m.securityView(layout.securityH), m.width, layout.securityH)
	base := fitBlock(joinVertical(panels, sec, inputBar, status, footer), m.width, m.height)
	base = m.gateOverlayView(base, layout)
	if m.helpOpen {
		return m.helpOverlay(base)
	}
	if m.showDiagnostics {
		return m.diagnosticsOverlay(base)
	}
	return base
}

func (m Model) reviewPanelsView(layout verticalLayout) string {
	if layout.panelH == 0 {
		return ""
	}
	body := m.reviewPanelBody()
	title := m.reviewPanelTitle()
	focused := m.badgeFocus() == focusReviewBadge
	hFrame, vFrame := shared.PanelFrame()
	innerW := max(m.width-hFrame, 1)
	innerH := max(layout.panelH-vFrame, 1)
	return shared.Panel(title, fitBlock(body, innerW, innerH), m.width, layout.panelH, focused)
}
