package review

import (
	"fmt"
	"sort"
	"strings"
)

func RenderFeedback(session Session, sub Submission) string {
	docs := make(map[string]Document, len(session.Documents))
	order := map[string]int{}
	for i, d := range session.Documents {
		docs[d.ID] = d
		order[d.ID] = i
	}
	comments := append([]Comment(nil), sub.Comments...)
	sort.SliceStable(comments, func(i, j int) bool {
		a, b := comments[i], comments[j]
		if order[a.Anchor.DocumentID] != order[b.Anchor.DocumentID] {
			return order[a.Anchor.DocumentID] < order[b.Anchor.DocumentID]
		}
		if a.Anchor.StartLine != b.Anchor.StartLine {
			return a.Anchor.StartLine < b.Anchor.StartLine
		}
		return a.Anchor.EndLine < b.Anchor.EndLine
	})
	var b strings.Builder
	fmt.Fprintf(&b, "# Review: %s\n\n- Verdict: `%s`\n- Round: `%s`\n- Documents reviewed: %d/%d\n", sub.StepID, sub.Verdict, sub.RoundID, len(sub.Reviewed), len(session.Documents))
	if strings.TrimSpace(sub.Summary) != "" {
		fmt.Fprintf(&b, "\n## Summary\n\n%s\n", strings.TrimSpace(sub.Summary))
	}
	for _, c := range comments {
		d := docs[c.Anchor.DocumentID]
		fmt.Fprintf(&b, "\n## %s\n\n### Lines %d-%d · %s · %s\n\n", d.Label, c.Anchor.StartLine, c.Anchor.EndLine, c.Kind, c.ID)
		for i, line := range strings.Split(c.Anchor.Quote, "\n") {
			fmt.Fprintf(&b, "> %d | %s\n", c.Anchor.StartLine+i, line)
		}
		fmt.Fprintf(&b, "\n%s\n", strings.TrimSpace(c.Body))
		if c.Kind == KindSuggestion {
			fmt.Fprintf(&b, "\n#### Suggested replacement\n\n```text\n%s\n```\n", c.Replacement)
		}
	}
	return b.String()
}
