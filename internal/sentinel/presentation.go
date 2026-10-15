package sentinel

import (
	"fmt"
	"sort"
	"strings"
)

type FindingKind string

const (
	Detection           FindingKind = "detection"
	CheckWarning        FindingKind = "check warning"
	ConfigurationNotice FindingKind = "configuration notice"
)

// Kind distinguishes detected activity from gaps in security coverage, including
// older persisted findings that predate the inspector.
func (f Finding) Kind() FindingKind {
	if f.Action == ActionBlocked || f.Action == ActionEscalated {
		return Detection
	}
	if f.Monitor == "operator-allowlist" {
		return ConfigurationNotice
	}
	switch f.Monitor {
	case "transcript-unavailable", "monitor-accounting-unknown", "monitor-state-unavailable", "budget-exhausted", "finding-persistence-failed", "findings-unavailable":
		return CheckWarning
	}
	if strings.HasPrefix(f.Monitor, "monitor-unavailable/") {
		return CheckWarning
	}
	return Detection
}

type FindingGroup struct {
	Key      string
	Kind     FindingKind
	Title    string
	Findings []Finding
}

func (g FindingGroup) Explanation() string {
	switch g.Kind {
	case ConfigurationNotice:
		return "These command approvals were saved in your backend configuration. Matching commands can run without asking jig's permission guard. This notice does not mean those commands ran or were unsafe. Review the rules listed below; remove only approvals you no longer want."
	case CheckWarning:
		return "A security check could not run reliably. This is a gap in coverage, not a detected attack. Other checks can continue. Unavailable classifiers stay disabled for this jig process; after fixing the cause, restart jig."
	default:
		return "A security rule flagged activity. Observed means recorded without blocking; blocked means the tool call was denied; escalated means a human recovery decision was requested. Review the evidence before deciding what to do."
	}
}

// GroupFindings changes presentation only: every original record stays available
// in its group and in findings.jsonl. Detections are never merged with notices.
func GroupFindings(findings []Finding) []FindingGroup {
	var groups []FindingGroup
	indices := make(map[string]int)
	for _, f := range findings {
		kind := f.Kind()
		key := string(kind) + "\x00" + f.Monitor
		if kind == Detection {
			key += "\x00" + f.StepID + "\x00" + string(f.Severity) + "\x00" + string(f.Action)
		}
		idx, ok := indices[key]
		if !ok {
			title := f.Monitor
			if kind == ConfigurationNotice {
				title = "Remembered command approvals"
			}
			if name, found := strings.CutPrefix(f.Monitor, "monitor-unavailable/"); found {
				switch name {
				case "prompt-injection":
					name = "Prompt injection"
				case "exfil-pattern":
					name = "Data exfiltration"
				case "stuck-loop":
					name = "Repeated activity"
				}
				title = name + " check unavailable"
			}
			idx = len(groups)
			indices[key] = idx
			groups = append(groups, FindingGroup{Key: key, Kind: kind, Title: title})
		}
		groups[idx].Findings = append(groups[idx].Findings, f)
	}
	rank := func(g FindingGroup) int {
		switch g.Kind {
		case Detection:
			return severityRank[g.Findings[0].Severity]
		case CheckWarning:
			return 4
		default:
			return 5
		}
	}
	sort.SliceStable(groups, func(i, j int) bool { return rank(groups[i]) < rank(groups[j]) })
	return groups
}

func SecuritySummary(findings []Finding) string {
	detections, warnings, notices := 0, 0, 0
	for _, group := range GroupFindings(findings) {
		switch group.Kind {
		case Detection:
			detections += len(group.Findings)
		case CheckWarning:
			warnings++
		case ConfigurationNotice:
			notices++
		}
	}
	count := func(n int, noun string) string {
		if n != 1 {
			noun += "s"
		}
		return fmt.Sprintf("%d %s", n, noun)
	}
	return count(detections, "detection") + " · " + count(warnings, "check warning") + " · " + count(notices, "notice")
}
