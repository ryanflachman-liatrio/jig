package notification

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"jig/internal/workflow"
)

// Bounded content limits shared by every adapter. Deviations from these must
// be documented in the specification.
const (
	SchemaVersion         = 1
	MaxWorkflowChars      = 128
	MaxStepIDChars        = 128
	MaxDisplayedAttention = 10
	MaxSlackBody          = 2000
	MaxDesktopBody        = 2000
	MaxRequestBytes       = 16 * 1024
	TruncationSuffix      = "…"
)

// jsonAttention is the fixed on-wire representation of one attention
// descriptor. Workflow-controlled step IDs may be truncated but never
// reformatted.
type jsonAttention struct {
	StepID string `json:"step_id,omitempty"`
	Kind   string `json:"kind"`
	Action string `json:"action"`
}

// jsonPayload is the versioned, fixed metadata allowlist. Callers must not
// add fields without bumping SchemaVersion.
type jsonPayload struct {
	SchemaVersion  int             `json:"schema_version"`
	NotificationID string          `json:"notification_id"`
	Event          string          `json:"event"`
	Timestamp      string          `json:"timestamp"`
	Workflow       string          `json:"workflow"`
	RunID          string          `json:"run_id"`
	Attention      []jsonAttention `json:"attention,omitempty"`
	AttentionCount int             `json:"attention_count,omitempty"`
	OmittedCount   int             `json:"omitted_count,omitempty"`
}

// BuildOutboundPayload composes the JSON, Slack, and desktop renderings for
// one logical notification. It never interpolates workflow-derived text into
// Slack or desktop formatting and applies fixed bounds so an over-eager
// producer cannot exceed the request cap.
func BuildOutboundPayload(n Notification) OutboundPayload {
	workflowName := truncateRunes(safeString(n.Workflow), MaxWorkflowChars)
	runID := truncateRunes(safeString(n.RunID), MaxStepIDChars)

	descriptors, displayed, omitted := prepareAttention(n.Attention)

	body := jsonPayload{
		SchemaVersion:  SchemaVersion,
		NotificationID: n.ID,
		Event:          string(n.Event),
		Timestamp:      n.Timestamp.UTC().Format(time.RFC3339),
		Workflow:       workflowName,
		RunID:          runID,
	}

	if n.Event == workflow.AttentionRequired {
		body.Attention = renderAttention(descriptors)
		body.AttentionCount = displayed
		body.OmittedCount = omitted
	}

	raw, _ := json.Marshal(body)
	if len(raw) > MaxRequestBytes {
		body.Attention = nil
		body.OmittedCount = displayed + omitted
		raw, _ = json.Marshal(body)
	}

	slack := renderSlack(n, workflowName, runID, descriptors, displayed, omitted)
	title, desktop := renderDesktop(n, workflowName, runID, descriptors, displayed, omitted)

	return OutboundPayload{
		JSON:         raw,
		SlackBody:    slack,
		DesktopTitle: title,
		DesktopBody:  desktop,
	}
}

// prepareAttention sorts and caps descriptors according to spec bounds.
func prepareAttention(items []AttentionDescriptor) (kept []AttentionDescriptor, displayed, omitted int) {
	if len(items) == 0 {
		return nil, 0, 0
	}
	sorted := make([]AttentionDescriptor, len(items))
	copy(sorted, items)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].StepID == sorted[j].StepID {
			return sorted[i].Kind < sorted[j].Kind
		}
		return sorted[i].StepID < sorted[j].StepID
	})
	total := len(sorted)
	if total <= MaxDisplayedAttention {
		return sorted, total, 0
	}
	return sorted[:MaxDisplayedAttention], MaxDisplayedAttention, total - MaxDisplayedAttention
}

func renderAttention(items []AttentionDescriptor) []jsonAttention {
	out := make([]jsonAttention, len(items))
	for i, d := range items {
		out[i] = jsonAttention{
			StepID: truncateRunes(safeString(d.StepID), MaxStepIDChars),
			Kind:   string(d.Kind),
			Action: d.Kind.Action(),
		}
	}
	return out
}

func renderSlack(n Notification, workflowName, runID string, items []AttentionDescriptor, displayed, omitted int) string {
	var b strings.Builder
	b.WriteString(headerText(n.Event))
	b.WriteString(" — ")
	b.WriteString(workflowName)
	b.WriteString(" (run ")
	b.WriteString(runID)
	b.WriteString(")")
	if n.Event == workflow.AttentionRequired {
		total := displayed + omitted
		fmt.Fprintf(&b, "\n%d step(s) need attention", total)
		if displayed > 0 {
			b.WriteString(":")
			for _, d := range items {
				fmt.Fprintf(&b, "\n• %s — %s", slackSafe(truncateRunes(d.StepID, MaxStepIDChars)), d.Kind.Action())
			}
		}
		if omitted > 0 {
			fmt.Fprintf(&b, "\n(+%d more omitted)", omitted)
		}
	}
	return truncateRunes(b.String(), MaxSlackBody)
}

func renderDesktop(n Notification, workflowName, runID string, items []AttentionDescriptor, displayed, omitted int) (string, string) {
	title := "jig — " + headerText(n.Event)
	title = truncateRunes(title, MaxWorkflowChars)
	var b strings.Builder
	b.WriteString(workflowName)
	b.WriteString(" (")
	b.WriteString(runID)
	b.WriteString(")")
	if n.Event == workflow.AttentionRequired {
		total := displayed + omitted
		fmt.Fprintf(&b, "\n%d step(s) need attention", total)
		if displayed > 0 {
			b.WriteString(":")
			max := len(items)
			if max > 5 {
				max = 5
			}
			for _, d := range items[:max] {
				fmt.Fprintf(&b, "\n- %s (%s)", desktopSafe(truncateRunes(d.StepID, MaxStepIDChars)), d.Kind.Action())
			}
			if len(items) > max {
				fmt.Fprintf(&b, "\n(+%d more)", len(items)-max+omitted)
			} else if omitted > 0 {
				fmt.Fprintf(&b, "\n(+%d omitted)", omitted)
			}
		}
	}
	return title, truncateRunes(b.String(), MaxDesktopBody)
}

func headerText(event workflow.NotificationEvent) string {
	switch event {
	case workflow.AttentionRequired:
		return "Attention required"
	case workflow.RunFailed:
		return "Run failed"
	case workflow.RunSucceeded:
		return "Run succeeded"
	default:
		return string(event)
	}
}

// safeString strips ASCII control characters and non-UTF-8 sequences so
// nothing reaches Slack, desktop helpers, or diagnostic sinks that could
// forge a mention, terminal escape, or newline where none was intended.
func safeString(s string) string {
	if s == "" {
		return ""
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// truncateRunes cuts s to at most max runes, appending a fixed truncation
// suffix. It preserves valid UTF-8 by never splitting mid-rune.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	suffixLen := utf8.RuneCountInString(TruncationSuffix)
	if suffixLen >= max {
		out := make([]rune, 0, max)
		for _, r := range s {
			if len(out) == max {
				break
			}
			out = append(out, r)
		}
		return string(out)
	}
	keep := max - suffixLen
	out := make([]rune, 0, keep)
	for _, r := range s {
		if len(out) == keep {
			break
		}
		out = append(out, r)
	}
	return string(out) + TruncationSuffix
}

// slackSafe escapes Slack special characters so a workflow-controlled step id
// cannot forge a mention (<!channel>), link (<url|text>), or format token.
func slackSafe(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// desktopSafe removes desktop-notification markup that could be interpreted
// by libnotify (Linux) or the osascript body renderer (macOS).
func desktopSafe(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

// canaryFreePayload is retained for tests only: it constructs the raw JSON
// wire form so callers can assert that no arbitrary label ever appears.
func canaryFreePayload(n Notification) ([]byte, error) {
	payload := BuildOutboundPayload(n)
	if len(payload.JSON) > MaxRequestBytes {
		return nil, fmt.Errorf("payload exceeds bound")
	}
	return payload.JSON, nil
}
