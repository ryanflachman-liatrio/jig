package runs

import (
	"strings"
	"testing"
)

func TestEmptyRunsCTADoesNotMentionDetail(t *testing.T) {
	m := NewModel().WithWorkflowContext("hotfix", nil)
	m.width, m.height = 80, 24
	view := m.View()
	lower := strings.ToLower(view)
	if strings.Contains(lower, "workflow detail") || strings.Contains(view, "Press r in a workflow detail") {
		t.Fatalf("empty runs still points at Detail:\n%s", view)
	}
	if !strings.Contains(view, "r  start a run") {
		t.Fatalf("empty runs missing CTA:\n%s", view)
	}
	if !strings.Contains(view, "hotfix") {
		t.Fatalf("empty runs missing workflow name:\n%s", view)
	}
}
