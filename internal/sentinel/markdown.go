package sentinel

import (
	"fmt"
	"sort"
	"strings"
)

// RenderMarkdown formats findings as a human-readable Markdown report, most
// severe first. It is the source for each step's security.md, so a reviewer
// can read the same findings.jsonl data without parsing JSON.
func RenderMarkdown(stepID string, findings []Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Security findings — %s\n\n", stepID)

	if len(findings) == 0 {
		b.WriteString("No security findings recorded for this step.\n")
		return b.String()
	}

	ordered := severityOrdered(findings)
	fmt.Fprintf(&b, "%d finding(s).\n\n", len(ordered))

	for _, f := range ordered {
		fmt.Fprintf(&b, "## [%s] %s\n\n", strings.ToUpper(string(f.Severity)), f.Monitor)
		fmt.Fprintf(&b, "- **Tier:** %s\n", f.Tier)
		fmt.Fprintf(&b, "- **Action:** %s\n", f.Action)
		fmt.Fprintf(&b, "- **Time:** %s\n", f.Ts.Format("2006-01-02 15:04:05 MST"))
		if f.Iteration != 0 {
			fmt.Fprintf(&b, "- **Iteration:** %d\n", f.Iteration)
		}
		if f.Evidence != "" {
			fmt.Fprintf(&b, "- **Evidence:** %s\n", f.Evidence)
		}
		fmt.Fprintf(&b, "- **Fingerprint:** %s\n", f.Fingerprint)
		b.WriteString("\n")
		fmt.Fprintf(&b, "%s\n\n", f.Detail)
	}

	return b.String()
}

var severityRank = map[Severity]int{
	SeverityCritical: 0,
	SeverityHigh:     1,
	SeverityMedium:   2,
	SeverityLow:      3,
}

// severityOrdered sorts by severity (critical first), then by time within a
// severity so a reviewer reads each band in the order it happened.
func severityOrdered(findings []Finding) []Finding {
	ordered := make([]Finding, len(findings))
	copy(ordered, findings)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if severityRank[a.Severity] != severityRank[b.Severity] {
			return severityRank[a.Severity] < severityRank[b.Severity]
		}
		return a.Ts.Before(b.Ts)
	})
	return ordered
}
