package headless

import (
	"fmt"
	"os/exec"
	"strings"

	"jig/internal/workflow"
)

// GateWarning describes a construct that will fail-closed if hit at runtime.
type GateWarning struct {
	StepID string
	Kind   string
	Detail string
}

// InventoryGates scans the workflow for headless-hostile constructs (D18:
// warn at start, fail when a gate fires).
func InventoryGates(wf *workflow.Workflow) []GateWarning {
	var out []GateWarning
	for i := range wf.Steps {
		s := &wf.Steps[i]
		switch s.Type {
		case workflow.StepReview:
			out = append(out, GateWarning{
				StepID: s.ID, Kind: "review",
				Detail: "type=review will fail-closed if reached",
			})
		}
		if s.BlockOn != "" {
			out = append(out, GateWarning{
				StepID: s.ID, Kind: "block_on",
				Detail: fmt.Sprintf("block_on=%q will fail-closed if true", s.BlockOn),
			})
		}
		if s.Profile == "@interactive" {
			out = append(out, GateWarning{
				StepID: s.ID, Kind: "interactive",
				Detail: `profile="@interactive" enables AskUserQuestion`,
			})
		}
		for _, in := range s.Inputs {
			if in.From == "user" {
				out = append(out, GateWarning{
					StepID: s.ID, Kind: "user_prompt",
					Detail: fmt.Sprintf("from=user input %q will fail-closed", in.As),
				})
			}
		}
	}
	return out
}

func inventoryGates(wf *workflow.Workflow) []GateWarning { return InventoryGates(wf) }

func (w *writer) emitGateWarnings(wf *workflow.Workflow, root string, hasMergePolicy bool) {
	for _, g := range inventoryGates(wf) {
		w.warn("step %q: %s", g.StepID, g.Detail)
	}
	// FinalMerge fires when persistence is on, cwd is a git repo, and the run
	// branch gains commits — remind operators before they hang waiting (D3).
	if root != "" && !hasMergePolicy && isGitRepo(".") {
		w.warn("git persistence is on; FinalMergeRequest will require --approve-merge, --discard-merge, or --ci")
	}
}

func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// EffectiveCIFlags is the documented --ci expansion printed once on stderr.
// recovery/conflict default to abort when empty (matches CLI defaults).
func EffectiveCIFlags(output OutputMode, discardMerge bool, onRecovery, onConflict, timeout string) string {
	parts := []string{"--ci"}
	if output != "" {
		parts = append(parts, "--output", string(output))
	}
	if discardMerge {
		parts = append(parts, "--discard-merge")
	}
	if onRecovery == "" {
		onRecovery = RecoveryAbort
	}
	if onConflict == "" {
		onConflict = ConflictAbort
	}
	parts = append(parts, "--on-recovery", onRecovery, "--on-conflict", onConflict)
	if timeout != "" {
		parts = append(parts, "--timeout", timeout)
	}
	return strings.Join(parts, " ")
}
