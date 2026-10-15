package sentinel

import (
	"fmt"
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

	fmt.Fprintf(&b, "%s\n\n", SecuritySummary(findings))
	for _, group := range GroupFindings(findings) {
		fmt.Fprintf(&b, "## %s\n\n**%s** · %d record(s).\n\n%s\n\n", group.Title, group.Kind, len(group.Findings), group.Explanation())
		for _, f := range group.Findings {
			fmt.Fprintf(&b, "### Step %s · %s\n\n", f.StepID, f.Monitor)
			if group.Kind == Detection {
				fmt.Fprintf(&b, "- **Severity:** %s\n- **Action:** %s\n", strings.ToUpper(string(f.Severity)), f.Action)
			}
			fmt.Fprintf(&b, "- **Tier:** %s\n", f.Tier)
			if !f.Ts.IsZero() {
				fmt.Fprintf(&b, "- **Time:** %s\n", f.Ts.Format("2006-01-02 15:04:05 MST"))
			}
			if f.Iteration != 0 {
				fmt.Fprintf(&b, "- **Iteration:** %d\n", f.Iteration)
			}
			if f.Evidence != "" {
				fmt.Fprintf(&b, "- **Evidence:** %s\n", f.Evidence)
			}
			fmt.Fprintf(&b, "- **Fingerprint:** %s\n\n%s\n\n", f.Fingerprint, f.Detail)
			if f.Failure != nil {
				d := f.Failure
				fmt.Fprintf(&b, "- **Backend / model:** %s / %s\n- **Stage / reason:** %s / %s\n- **Error type:** %s\n- **Attempts:** %d\n- **Elapsed / limit:** %dms / %dms\n\n**Next step:** %s\n\n", d.Backend, d.Model, d.Stage, d.Reason, d.ErrorType, d.Attempts, d.ElapsedMS, d.TimeoutMS, d.NextStep)
			}
		}
	}

	return b.String()
}

var severityRank = map[Severity]int{
	SeverityCritical: 0,
	SeverityHigh:     1,
	SeverityMedium:   2,
	SeverityLow:      3,
}
