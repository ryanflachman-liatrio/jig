package monitor

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"jig/internal/step"
	"jig/internal/tui/shared"
)

// listBodyHeaderLines is the number of lines listBody renders above the step
// table. The workflow/run header moved to the panel border, so the table now
// starts at line 0; this stays 0 to keep the ensureCursorVisible row math honest.
const listBodyHeaderLines = 0

// stepRowLines is how many rendered lines each step occupies in listBody (a
// status line plus a metadata line). ensureCursorVisible multiplies the cursor
// index by this to map a step to its first viewport row.
const stepRowLines = 2

// body returns the Steps-panel body. Retained for tests that assert list content
// directly; View() renders both panels. transcriptText returns the Transcript
// panel body.
func (m Model) body() string {
	return m.listBody()
}

func (m Model) listBody() string {
	var b strings.Builder

	if len(m.steps) == 0 {
		b.WriteString("  " + shared.Theme.Question.Render("Waiting for run to start…") + "\n")
		return b.String()
	}

	idWidth := 2
	for _, s := range m.steps {
		if len(s.id) > idWidth {
			idWidth = len(s.id)
		}
	}

	rows := m.visibleRows()

	for i, row := range rows {
		if row.isStepRow() {
			si, ok := m.index[row.stepID]
			if !ok {
				continue
			}
			s := m.steps[si]

			cursor := "  "
			if i == m.cursor {
				cursor = shared.Theme.SelectedBar.Render(shared.CursorBar) + " "
			}

			// Show expand/collapse affordance if there are any visible (non-errored) files.
			hasVisibleFiles := false
			for _, f := range m.stepFiles[s.id] {
				if f.err == nil {
					hasVisibleFiles = true
					break
				}
			}
			affordance := " "
			if hasVisibleFiles {
				if m.expanded[s.id] {
					affordance = shared.Theme.Step.Tree.ExpandAffordance.Render(shared.ExpandedMarker)
				} else {
					affordance = shared.Theme.Step.Tree.ExpandAffordance.Render(shared.CollapsedMarker)
				}
			}

			indicator, style := stepIndicator(s.status)

			// Line 1: status glyph, id, status text (+ policy-limit badge).
			head := fmt.Sprintf("%s%s%s  %s  %s",
				cursor,
				affordance,
				indicator,
				style.Render(shared.PadRight(s.id, idWidth)),
				statusStyle(s.status).Render(string(s.status)),
			)
			if label := subtypeBadgeLabel(s.subtype); label != "" {
				head += "  " + shared.Theme.Badge.Error.Render(label)
			}
			if i == m.cursor {
				b.WriteString(shared.Theme.SelectedLine.Render(head) + "\n")
			} else {
				b.WriteString(head + "\n")
			}

			// Line 2: dim metadata, indented under the id.
			var meta []string
			if t := stepTokensStr(s); t != "" {
				meta = append(meta, t)
			}
			if c := stepCostStr(s); c != "" {
				meta = append(meta, c)
			}
			meta = append(meta, stepDuration(s))
			if n := m.msgCount[s.id]; n > 0 {
				meta = append(meta, fmt.Sprintf("%d msg", n))
			}
			b.WriteString("     " + shared.Theme.Question.Render(strings.Join(meta, " · ")) + "\n")
		} else if row.isFileRow() {
			cursor := "  "
			if i == m.cursor {
				cursor = shared.Theme.SelectedBar.Render(shared.CursorBar) + " "
			}
			fileMarker := ""
			switch row.file.kind {
			case kindMarkdown:
				fileMarker = "md"
			case kindJSON:
				fileMarker = "json"
			default:
				fileMarker = "file"
			}
			line := fmt.Sprintf("%s  [%s] %s", cursor, fileMarker, row.file.displayLabel())
			if i == m.cursor {
				b.WriteString(shared.Theme.SelectedLine.Render(line) + "\n")
			} else {
				b.WriteString(shared.Theme.Step.Tree.FileRow.Render(line) + "\n")
			}
		}
	}

	// Run total, directly beneath the step table: summed tokens and cost across
	// every step. Shown once anything has been reported (a $0.00/0-token run
	// reports nothing, so the row stays hidden until there is something to total).
	if total := m.totalsStr(); total != "" {
		b.WriteString("  " + shared.Theme.SelectedLine.Render("Total") + "  " + total + "\n")
	}

	b.WriteString("\n")
	if m.done {
		if m.failed {
			b.WriteString("  " + shared.Theme.Error.Render(shared.IconError+" run failed") + "\n")
			m.writeFailureReasons(&b)
		} else {
			b.WriteString("  " + shared.Theme.Valid.Render(shared.IconSuccess+" run complete") + "\n")
		}
	}

	// Human-in-the-loop gates (review/input/question/prompt) render in the
	// focused overlay, not inline here — the gate remains a non-blocking focus
	// region (ADR 0002).

	// Streaming output: show last outputMaxLines lines for any running agent step.
	for _, s := range m.steps {
		if s.status != step.StatusRunning {
			continue
		}
		buf, ok := m.stepOutput[s.id]
		if !ok || buf.Len() == 0 {
			continue
		}
		lines := strings.Split(buf.String(), "\n")
		// Keep only the last outputMaxLines non-empty lines.
		var recent []string
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				recent = append(recent, l)
			}
		}
		if len(recent) > outputMaxLines {
			recent = recent[len(recent)-outputMaxLines:]
		}
		b.WriteString("\n  " + shared.Theme.Question.Render("▸ "+s.id) + "\n")
		for _, l := range recent {
			b.WriteString("    " + l + "\n")
		}
	}

	return b.String()
}

// writeFailureReasons appends the human-readable "why" behind a failed run: the
// engine-level error (if any), then each failed step's reason. Without this the
// summary only says "✗ run failed" — the reason lives in the events but was
// never rendered. Reasons wrap to the view width so a long shell error or gate
// detail stays readable.
func (m Model) writeFailureReasons(b *strings.Builder) {
	wrapW := m.width - 6
	if wrapW < 20 {
		wrapW = 20
	}
	wrap := lipgloss.NewStyle().Width(wrapW)

	if m.runErr != "" {
		b.WriteString("    " + shared.Theme.Error.Render(wrap.Render("engine: "+m.runErr)) + "\n")
	}
	for _, s := range m.steps {
		if s.status != step.StatusFailed || s.err == "" {
			continue
		}
		b.WriteString("    " + shared.Theme.Error.Render(wrap.Render(s.id+": "+s.err)) + "\n")
	}
}

func stepIndicator(s step.Status) (string, lipgloss.Style) {
	switch s {
	case step.StatusPending:
		return "○", shared.Theme.Question
	case step.StatusRunning:
		return "●", shared.Theme.Running
	case step.StatusSucceeded:
		return "✓", shared.Theme.Valid
	case step.StatusFailed:
		return "✗", shared.Theme.Error
	case step.StatusSkipped:
		return "—", shared.Theme.Question
	case step.StatusValidating:
		return "⇢", shared.Theme.Question
	case step.StatusAwaitingReview:
		return "?", shared.Theme.Marker
	case step.StatusNeedsInput:
		return "⊙", shared.Theme.Marker
	case step.StatusAwaitingRecovery:
		return "⚠", shared.Theme.Error
	default:
		return "·", shared.Theme.Question
	}
}

func statusStyle(s step.Status) lipgloss.Style {
	switch s {
	case step.StatusRunning:
		return shared.Theme.Running
	case step.StatusSucceeded:
		return shared.Theme.Valid
	case step.StatusFailed:
		return shared.Theme.Error
	default:
		return shared.Theme.Question
	}
}

// subtypeBadgeLabel returns a compact label for policy-limit failure subtypes so
// operators can distinguish "hit turn limit" from an API error at a glance.
// Returns "" for subtypes that don't warrant a special annotation.
func subtypeBadgeLabel(subtype string) string {
	switch subtype {
	case "error_max_turns":
		return "max turns"
	case "error_max_budget_usd":
		return "budget"
	default:
		return ""
	}
}

func stepDuration(s monitorStep) string {
	if s.start.IsZero() {
		return "—"
	}
	end := s.end
	running := end.IsZero()
	if running {
		end = time.Now()
	}
	d := end.Sub(s.start)
	// A still-running step is refreshed once a second by the live-clock tick, so
	// round its display to whole seconds — millisecond digits would only ever show
	// the arbitrary sub-second remainder at tick time and jitter distractingly.
	// Completed steps keep millisecond precision (their duration is final).
	if running {
		d = d.Round(time.Second)
	} else {
		d = d.Round(time.Millisecond)
	}
	return d.String()
}

// stepCostStr returns a human-readable cost string for a step, or "" when cost
// is unknown (nil pointer — the step is still running or the SDK didn't report).
func stepCostStr(s monitorStep) string {
	if s.cost == nil {
		return ""
	}
	return fmt.Sprintf("$%.4f", *s.cost)
}

// stepTokensStr returns a compact token string for a step, or "" when no tokens
// are known yet (still running, or a command/review step with no usage).
func stepTokensStr(s monitorStep) string {
	if s.tokens == 0 {
		return ""
	}
	return humanTokens(s.tokens) + " tok"
}

// humanTokens formats a token count compactly: raw below 1k, then k/M with one
// decimal so a step's context size reads at a glance without column drift.
func humanTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// recomputeTotals sums the per-step (already cumulative) cost and token figures
// into the run totals. Each step's figure is the engine's cumulative spend
// across all its attempts, so the sum is the run's true total — inclusive of
// resets, retries, and loop re-runs. Called on every StepStatus.
func (m *Model) recomputeTotals() {
	var cost float64
	var tokens int
	for _, s := range m.steps {
		if s.cost != nil {
			cost += *s.cost
		}
		tokens += s.tokens
	}
	m.totalCost = cost
	m.totalTokens = tokens
}

// totalsStr renders the run's summed tokens and cost as a single styled string,
// or "" when neither has been reported yet. Used for the Steps-panel total row.
func (m Model) totalsStr() string {
	var parts []string
	if m.totalTokens > 0 {
		parts = append(parts, humanTokens(m.totalTokens)+" tok")
	}
	if m.totalCost > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f", m.totalCost))
	}
	if len(parts) == 0 {
		return ""
	}
	return shared.Theme.Question.Render(strings.Join(parts, "  "))
}
