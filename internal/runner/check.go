package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"jig/internal/engine"
	"jig/internal/step"
)

// CheckExecutor makes command exit status part of a workflow's typed data
// plane. A failing quality command is not an engine crash: it succeeds as a
// completed check with verdict fail, allowing an explicit route or review gate
// to decide what happens next. A missing executable is an operational error,
// not a silently-skipped quality signal.
type CheckExecutor struct{ command *CommandExecutor }

func NewCheckExecutor(cwd string) *CheckExecutor {
	return &CheckExecutor{command: NewCommandExecutor(cwd)}
}

func (e *CheckExecutor) Execute(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (*step.Result, error) {
	result, err := e.command.Execute(ctx, req, rep)
	if err != nil {
		return result, err
	}
	if result == nil {
		return &step.Result{Status: step.StatusSucceeded, Verdict: "error", Err: "check executor returned no result"}, nil
	}
	if result.Status == step.StatusSucceeded {
		result.Verdict = "pass"
	} else if strings.Contains(result.Err, "exit status 127") {
		result.Verdict = "error"
	} else {
		result.Verdict = "fail"
	}
	// A check's outcome is typed evidence rather than an execution failure. The
	// original error text remains attached to the evidence/result for reviewers.
	result.Status = step.StatusSucceeded
	writeCheckFindings(result)
	return result, nil
}

// writeCheckFindings complements the human-readable command log with a stable
// JSON record that route consumers and external audit tooling can inspect.
func writeCheckFindings(result *step.Result) {
	if result == nil || result.OutputPath == "" {
		return
	}
	data, err := json.Marshal(struct {
		Outcome string `json:"outcome"`
		Error   string `json:"error,omitempty"`
		Log     string `json:"log"`
	}{Outcome: result.Verdict, Error: result.Err, Log: result.OutputPath})
	if err != nil {
		return
	}
	path := filepath.Join(filepath.Dir(result.OutputPath), "findings.json")
	if err := os.WriteFile(path, data, 0o644); err == nil {
		result.OutputPath = path
	}
}
