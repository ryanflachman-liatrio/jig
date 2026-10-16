package ops

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
)

func TestD04ReopenStatusPolicy(t *testing.T) {
	events := []engine.JournalRecord{{Event: engine.RunStarted{RunID: "r", Workflow: "wf", Steps: []string{"work"}}}, {Event: engine.RecoveryRequest{RunID: "r", StepID: "work"}}, {Event: engine.SandboxPolicyBlocked{RunID: "r", Reason: "sandbox_policy_denied", Epoch: 2}}}
	report := FoldStatus("r", events, false)
	if report.SandboxPolicyReason != "sandbox_policy_denied" || len(report.WaitingOn) != 2 {
		t.Fatalf("lost overlay %+v", report)
	}
	events = append(events, engine.JournalRecord{Event: engine.SandboxPolicyCleared{RunID: "r", Epoch: 2}})
	report = FoldStatus("r", events, false)
	if report.SandboxPolicyReason != "" || len(report.WaitingOn) != 1 || report.WaitingOn[0].Kind != "recovery" {
		t.Fatalf("grant removed recovery %+v", report)
	}
	events = append(events, engine.JournalRecord{Event: engine.SandboxPolicyBlocked{RunID: "r", Reason: "PRIVATE_FILE_PATH"}})
	report = FoldStatus("r", events, false)
	raw, _ := json.Marshal(report)
	if strings.Contains(string(raw), "PRIVATE") {
		t.Fatal("untyped journal reason disclosed")
	}
}
