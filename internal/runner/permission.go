package runner

import (
	"fmt"
	"os"
	"sync"
	"time"

	"jig/internal/agentcfg"
	"jig/internal/engine"
	"jig/internal/harness"
	"jig/internal/operatorcfg"
	"jig/internal/sentinel"
)

// stepPermission is the permission callback installed on every agent step.
// The harness has already denied calls whose tool name it cannot resolve. The
// decision order is:
//
//  0. On a guarded step, deny a Bash or edit call whose input could not be
//     assembled, since the guard cannot inspect it.
//  1. Deny ExitPlanMode, so an agent never changes its own permission mode.
//  2. On Claude, deny a call outside a present tools list, inside
//     disallowed_tools, or outside the read-only set in plan mode.
//     AskUserQuestion is exempt: only ask_user governs it.
//  3. On a guarded step, return the Tier-1 guard's decision, recording a
//     finding for a denial.
//  4. Otherwise allow.
func stepPermission(req engine.StepRequest, sink *findingSink) harness.PermissionFn {
	claude, isClaude := req.Step.ResolvedAgent().(agentcfg.ClaudeAgent)
	guard := req.Guard
	return func(call harness.ToolCall) harness.Decision {
		if guard != nil && !call.InputResolved && guardInspectsInput(call.Name) {
			return harness.Decision{Reason: "unresolved tool input"}
		}
		if call.Name == "ExitPlanMode" {
			return harness.Decision{Reason: "agents may not change their permission mode"}
		}
		if isClaude && call.Name != agentcfg.AskUserQuestion && !claudePermits(claude, call.Name) {
			return harness.Decision{Reason: fmt.Sprintf("tool %q is not permitted for this step", call.Name)}
		}
		if guard == nil {
			return harness.Decision{Allow: true}
		}
		if req.NetworkRequest != nil && outboundToolCall(call.Name, call.Input) {
			req.NetworkRequest()
		}
		dec := guard.Check(call.Name, call.Input)
		if !dec.Allow {
			sink.recordGuard(call.Name, call.ID, dec)
		}
		return harness.Decision{Allow: dec.Allow, Reason: dec.Reason}
	}
}

// guardInspectsInput reports whether the Tier-1 rules need the call's input:
// shell commands and file edits.
func guardInspectsInput(name string) bool {
	switch name {
	case "Bash", "Edit", "Write", "MultiEdit":
		return true
	}
	return false
}

// claudePermits applies a Claude agent's tool settings as a second layer on
// top of the adapter. An explicit empty tools list permits nothing.
func claudePermits(a agentcfg.ClaudeAgent, name string) bool {
	if a.Tools != nil && !containsStr(a.Tools, name) {
		return false
	}
	if containsStr(a.DisallowedTools, name) {
		return false
	}
	if a.PermissionMode == "plan" && !containsStr(agentcfg.PlanReadOnlyTools, name) {
		return false
	}
	return true
}

// findingSink records a step's Tier-1 findings exactly once each: to the
// run's findings file (when the guard is on and persistence is enabled) and as
// a SecurityFinding event. Permission decisions run on the harness's request
// goroutines while captureStream runs on the step's, so it is safe for
// concurrent use, and it drops a repeated fingerprint.
type findingSink struct {
	req engine.StepRequest
	rep engine.Reporter

	mu   sync.Mutex
	fw   *sentinel.Writer
	seen map[string]bool
}

func newFindingSink(req engine.StepRequest, rep engine.Reporter) (*findingSink, error) {
	sink := &findingSink{req: req, rep: rep, seen: make(map[string]bool)}
	if req.Guard != nil && req.FindingsPath != "" {
		fw, err := sentinel.NewWriter(req.FindingsPath)
		if err != nil {
			return nil, err
		}
		sink.fw = fw
	}
	return sink, nil
}

func (s *findingSink) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fw != nil {
		_ = s.fw.Close()
		s.fw = nil
	}
}

// recordGuard records a guard denial of one tool call.
func (s *findingSink) recordGuard(tool, callID string, dec sentinel.Decision) {
	sev := sentinel.SeverityHigh
	if dec.Action == sentinel.ActionEscalated {
		sev = sentinel.SeverityCritical
	}
	s.record(dec.Monitor, "tool:"+tool+":"+callID, sev, dec.Action, dec.Reason)
}

func (s *findingSink) record(monitor, evidenceKey string, sev sentinel.Severity, action sentinel.Action, detail string) {
	fp := sentinel.NewFingerprint(s.req.Step.ID, monitor, evidenceKey)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen[fp] {
		return
	}
	s.seen[fp] = true
	if s.fw != nil {
		_ = s.fw.Append(sentinel.Finding{
			Ts:          time.Now().UTC(),
			RunID:       s.req.RunID,
			StepID:      s.req.Step.ID,
			Iteration:   s.req.Iteration,
			Tier:        sentinel.TierGuard,
			Monitor:     monitor,
			Severity:    sev,
			Action:      action,
			Detail:      detail,
			Evidence:    evidenceKey,
			Fingerprint: fp,
		})
	}
	s.rep.Finding(engine.SecurityFinding{
		RunID:       s.req.RunID,
		StepID:      s.req.Step.ID,
		Tier:        string(sentinel.TierGuard),
		Monitor:     monitor,
		Severity:    string(sev),
		Action:      string(action),
		Fingerprint: fp,
	})
}

// operatorAllowlistMonitor names the Tier-1 findings for operator-local
// auto-approval config.
const operatorAllowlistMonitor = "operator-allowlist"

// checkOperatorAllowlist inspects the operator-local Codex or Cursor config a
// guarded step would run under. Those sources auto-approve calls without a
// permission prompt, so the guard never sees them, and ACP mode does not
// override them. Blanket auto-approval fails the step (the returned message);
// each narrow entry is recorded as an observed finding and the step runs.
func checkOperatorAllowlist(req engine.StepRequest, cwd string, sink *findingSink) string {
	backend := req.Step.Backend
	findings, err := operatorcfg.Inspect(backend, req.RepoRoot, cwd, os.Getenv)
	if err != nil {
		return fmt.Sprintf("step is guarded and its operator config cannot be checked: %v", err)
	}
	for _, f := range findings {
		if f.Class == operatorcfg.Blanket {
			return fmt.Sprintf("step is guarded but %s auto-approves every %s tool call, so the Tier-1 guard never sees them; remove the setting, or set [step.security] tier1_enabled = false", f, backend)
		}
	}
	for _, f := range findings {
		sink.record(operatorAllowlistMonitor, "operator-config:"+f.Location(), sentinel.SeverityLow, sentinel.ActionObserved,
			fmt.Sprintf("%s auto-approves matching %s calls without a permission prompt, so the Tier-1 guard does not see them", f, backend))
	}
	return ""
}
