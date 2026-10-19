package runner

import (
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/toolcall"
)

func TestRedactToolSummary(t *testing.T) {
	const secret = "synthetic-secret-value"
	activity := &toolcall.Activity{Summary: "Check " + secret}
	got := redactToolActivity(engine.StepRequest{Secrets: map[string]string{"token": secret}}, activity)
	if got.Summary == activity.Summary || activity.Summary != "Check "+secret {
		t.Fatalf("summary was not redacted independently: got %q, source %q", got.Summary, activity.Summary)
	}
}
