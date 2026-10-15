package monitor

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	keybind "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ryanflachman-liatrio/jig/internal/sentinel"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

func securityText(text string) string {
	text = ansi.Strip(text)
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, text)
}

func (m Model) selectedSecurityGroup() (sentinel.FindingGroup, bool) {
	groups := sentinel.GroupFindings(m.secFindings)
	if len(groups) == 0 {
		return sentinel.FindingGroup{}, false
	}
	return groups[min(max(m.securityCursor, 0), len(groups)-1)], true
}

func (m Model) securityDetails() string {
	group, ok := m.selectedSecurityGroup()
	if !ok {
		if m.securityLoading {
			return "Loading saved security records" + shared.EllipsisGlyph
		}
		if m.securityLoadFailed {
			return "Saved security records could not be read. Check access to the run directory and findings.jsonl."
		}
		return "No security events recorded for this run."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s · %d record(s)\n\n%s\n", securityText(group.Title), group.Kind, len(group.Findings), group.Explanation())
	if m.RunDir != "" {
		fmt.Fprintf(&b, "\nSaved records: %s\n", filepath.Join(m.RunDir, "findings.jsonl"))
	}
	for i, f := range group.Findings {
		fmt.Fprintf(&b, "\n--- %d of %d · step %s ---\n", i+1, len(group.Findings), f.StepID)
		if group.Kind == sentinel.Detection {
			fmt.Fprintf(&b, "Severity: %s\nAction: %s\n", f.Severity, f.Action)
		}
		if !f.Ts.IsZero() {
			fmt.Fprintf(&b, "Time: %s\n", f.Ts.Local().Format("2006-01-02 15:04:05 MST"))
		}
		if f.Failure != nil {
			d := f.Failure
			fmt.Fprintf(&b, "Backend: %s\nModel: %s\nStage: %s\nReason: %s\nError type: %s\nAttempts: %d\nElapsed: %dms · limit: %dms\n\n%s\nNext step: %s\n", d.Backend, d.Model, d.Stage, d.Reason, d.ErrorType, d.Attempts, d.ElapsedMS, d.TimeoutMS, d.Summary, d.NextStep)
		} else if f.Detail != "" {
			b.WriteString(f.Detail + "\n")
		} else {
			b.WriteString("Details are unavailable without persisted findings.\n")
		}
		if f.Evidence != "" {
			fmt.Fprintf(&b, "Evidence: %s\n", f.Evidence)
		}
	}
	return securityText(b.String())
}

func (m Model) securityDetailLines() []string {
	return strings.Split(ansi.Hardwrap(m.securityDetails(), max(1, m.width), true), "\n")
}

func (m Model) updateSecurity(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch {
	case keybind.Matches(msg, m.keys.SecurityInspector):
		m.securityOpen = false
	case msg.String() == "esc" || msg.String() == "q":
		if m.securityDetail {
			m.securityDetail = false
			m.securityOffset = 0
		} else {
			m.securityOpen = false
		}
	case msg.String() == "enter" || msg.String() == "space":
		if len(m.secFindings) > 0 {
			m.securityDetail = !m.securityDetail
			m.securityOffset = 0
		}
	case msg.String() == "y":
		text := m.securityDetails()
		return m, func() tea.Msg {
			return shared.ClipboardRequest{Target: shared.ClipboardTarget{Surface: shared.ClipboardSurfaceSecurity, Label: "security details"}, Loader: func() shared.ClipboardPayload { return shared.ClipboardPayload{Payload: text} }}
		}
	case m.securityDetail:
		delta := 0
		switch msg.String() {
		case "j", "down":
			delta = 1
		case "k", "up":
			delta = -1
		case "pgdown", "ctrl+d":
			delta = max(1, m.height-4)
		case "pgup", "ctrl+u":
			delta = -max(1, m.height-4)
		case "home", "g":
			m.securityOffset = 0
		case "end", "G":
			m.securityOffset = len(m.securityDetailLines())
		}
		m.securityOffset = min(max(0, m.securityOffset+delta), max(0, len(m.securityDetailLines())-max(1, m.height-3)))
	default:
		groups := sentinel.GroupFindings(m.secFindings)
		switch msg.String() {
		case "j", "down":
			m.securityCursor++
		case "k", "up":
			m.securityCursor--
		case "home":
			m.securityCursor = 0
		case "end":
			m.securityCursor = len(groups) - 1
		}
		m.securityCursor = min(max(m.securityCursor, 0), max(len(groups)-1, 0))
	}
	return m, nil
}

// The inspector owns its keyboard surface without changing the run's focus,
// selected step, gate queue, or transcript scroll position.
func (m Model) securityInspectorView() string {
	rows := []string{shared.Theme.Help.Title.Render("Security inspector · " + securityText(m.RunID)), shared.Theme.Help.Desc.Render(sentinel.SecuritySummary(m.secFindings))}
	bodyH := max(0, m.height-3)
	hint := "↑/↓ select · enter details · y copy · esc close"
	if m.securityDetail {
		lines := m.securityDetailLines()
		offset := min(max(0, m.securityOffset), max(0, len(lines)-bodyH))
		rows = append(rows, lines[offset:min(len(lines), offset+bodyH)]...)
		hint = fmt.Sprintf("↑/↓ scroll · pgup/pgdown · y copy · esc back · %d/%d", min(offset+bodyH, len(lines)), len(lines))
	} else {
		groups := sentinel.GroupFindings(m.secFindings)
		if len(groups) == 0 {
			rows = append(rows, m.securityDetails())
		} else {
			listH := min(len(groups), max(1, bodyH/2))
			cursor := min(max(m.securityCursor, 0), len(groups)-1)
			start := max(0, cursor-listH+1)
			for i := start; i < min(len(groups), start+listH); i++ {
				g := groups[i]
				title := fmt.Sprintf("  %s · %s · %d record(s)", securityText(g.Title), g.Kind, len(g.Findings))
				if i == cursor {
					title = "> " + strings.TrimSpace(title)
					title = shared.Theme.Help.Title.Render(securityText(title))
				}
				rows = append(rows, title)
			}
			group := groups[cursor]
			preview := "\n" + group.Explanation()
			if group.Kind == sentinel.CheckWarning {
				preview += "\n\n" + group.Findings[0].Detail
			}
			previewLines := strings.Split(ansi.Hardwrap(securityText(preview), max(1, m.width), true), "\n")
			remaining := max(0, bodyH-listH)
			rows = append(rows, previewLines[:min(len(previewLines), remaining)]...)
		}
	}
	for len(rows) < m.height-1 {
		rows = append(rows, "")
	}
	if m.height > 2 {
		rows = append(rows, shared.Theme.Help.Desc.Render(hint))
	}
	return fitBlock(strings.Join(rows, "\n"), m.width, m.height)
}

type SecurityLoadedMsg struct {
	runID, runDir string
	findings      []sentinel.Finding
	err           error
}

// LoadSecurityCmd reads persisted findings off the UI thread when entering a run.
func (m Model) LoadSecurityCmd() tea.Cmd {
	if m.RunDir == "" {
		return nil
	}
	runID, runDir := m.RunID, m.RunDir
	return func() tea.Msg {
		findings, err := sentinel.ReadAll(filepath.Join(runDir, "findings.jsonl"))
		return SecurityLoadedMsg{runID: runID, runDir: runDir, findings: findings, err: err}
	}
}

func (m *Model) retainSecuritySelection(key string) {
	if key == "" {
		return
	}
	for i, group := range sentinel.GroupFindings(m.secFindings) {
		if group.Key == key {
			m.securityCursor = i
			return
		}
	}
}
