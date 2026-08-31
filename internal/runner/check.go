package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/workflow"
)

// CheckExecutor runs quality tools under the check evidence protocol. Process
// status is deliberately not a verdict: tools report pass, fail, or error in
// their versioned findings file, while an inapplicable check is skipped by the
// scheduler before this executor is invoked.
type CheckExecutor struct{ command *CommandExecutor }

func NewCheckExecutor(cwd string) *CheckExecutor {
	return &CheckExecutor{command: NewCommandExecutor(cwd)}
}

type checkFinding struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type checkFindingsDocument struct {
	SchemaVersion int            `json:"schema_version"`
	Outcome       string         `json:"outcome"`
	Findings      []checkFinding `json:"findings"`
}

func (e *CheckExecutor) Execute(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (*step.Result, error) {
	if err := requiredToolsAvailable(req.Step); err != nil {
		return e.protocolError(req, err.Error(), true), nil
	}

	result, err := e.command.Execute(ctx, req, rep)
	if err != nil {
		return e.protocolError(req, fmt.Sprintf("execute check command: %v", err), true), nil
	}
	if result == nil {
		return e.protocolError(req, "check executor returned no result", true), nil
	}
	if ctx.Err() != nil {
		return e.protocolError(req, fmt.Sprintf("check command did not complete: %v", ctx.Err()), false), nil
	}

	data, err := os.ReadFile(filepath.Join(e.executionDir(req), req.Step.Findings.File))
	if err != nil {
		return e.protocolError(req, fmt.Sprintf("read check findings %q: %v", req.Step.Findings.File, err), false), nil
	}
	data = []byte(redactSecrets(req, string(data)))
	doc, err := parseCheckFindings(data)
	if err != nil {
		return e.protocolError(req, fmt.Sprintf("invalid check findings %q: %v", req.Step.Findings.File, err), false), nil
	}
	artifacts, err := snapshotCheckArtifacts(req, e.executionDir(req))
	if err != nil {
		return e.protocolError(req, err.Error(), false), nil
	}

	result.Status = step.StatusSucceeded
	result.Verdict = doc.Outcome
	result.Err = ""
	result.Structured = append(result.Structured[:0], data...)
	result.Artifacts = artifacts
	if path := writeCheckFindings(req, data); path != "" {
		result.OutputPath = path
	}
	return result, nil
}

func snapshotCheckArtifacts(req engine.StepRequest, executionDir string) (map[string]string, error) {
	if req.Step == nil || req.Step.Findings == nil || len(req.Step.Findings.Artifacts) == 0 || req.TranscriptPath == "" {
		return nil, nil
	}
	dir := filepath.Join(filepath.Dir(req.TranscriptPath), "evidence")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create check artifact directory: %w", err)
	}
	artifacts := make(map[string]string, len(req.Step.Findings.Artifacts))
	for name, source := range req.Step.Findings.Artifacts {
		data, err := os.ReadFile(filepath.Join(executionDir, source))
		if err != nil {
			return nil, fmt.Errorf("read check artifact %q: %w", name, err)
		}
		path := filepath.Join(dir, fmt.Sprintf("iteration-%03d-attempt-%03d-artifact-%s", req.Iteration, req.Attempt, name))
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return nil, fmt.Errorf("snapshot check artifact %q: %w", name, err)
		}
		artifacts[name] = path
	}
	return artifacts, nil
}

func requiredToolsAvailable(st *workflow.Step) error {
	if st == nil || st.Findings == nil {
		return fmt.Errorf("check has no findings interface")
	}
	for _, tool := range st.Findings.RequiredTools {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("required tool %q is unavailable: %v", tool, err)
		}
	}
	return nil
}

func (e *CheckExecutor) executionDir(req engine.StepRequest) string {
	if req.Worktree != "" {
		return req.Worktree
	}
	return e.command.cwd
}

func parseCheckFindings(data []byte) (checkFindingsDocument, error) {
	var doc checkFindingsDocument
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return checkFindingsDocument{}, err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return checkFindingsDocument{}, fmt.Errorf("must contain exactly one JSON object")
	}
	if doc.SchemaVersion != workflow.CheckFindingsSchemaVersion {
		return checkFindingsDocument{}, fmt.Errorf("schema_version = %d, want %d", doc.SchemaVersion, workflow.CheckFindingsSchemaVersion)
	}
	switch doc.Outcome {
	case "pass", "fail", "error":
	default:
		return checkFindingsDocument{}, fmt.Errorf("outcome %q is invalid; checks may only report pass, fail, or error", doc.Outcome)
	}
	if doc.Findings == nil {
		return checkFindingsDocument{}, fmt.Errorf("findings must be an array")
	}
	for i, f := range doc.Findings {
		if strings.TrimSpace(f.ID) == "" || strings.TrimSpace(f.Message) == "" {
			return checkFindingsDocument{}, fmt.Errorf("findings[%d] requires id and message", i)
		}
		switch f.Severity {
		case "info", "warning", "error":
		default:
			return checkFindingsDocument{}, fmt.Errorf("findings[%d].severity %q is invalid", i, f.Severity)
		}
	}
	return doc, nil
}

// protocolError is a completed check outcome rather than an engine crash. It
// retains independent log and findings files whenever persistence is enabled.
func (e *CheckExecutor) protocolError(req engine.StepRequest, detail string, writeLog bool) *step.Result {
	logPath := ""
	if writeLog {
		logPath = writeCheckEvidence(req, detail+"\n")
	}
	doc := checkFindingsDocument{
		SchemaVersion: workflow.CheckFindingsSchemaVersion,
		Outcome:       "error",
		Findings: []checkFinding{{
			ID:       "check-protocol",
			Severity: "error",
			Message:  detail,
		}},
	}
	data, _ := json.Marshal(doc)
	path := writeCheckFindings(req, data)
	if path == "" {
		path = logPath
	}
	return &step.Result{
		Status:     step.StatusSucceeded,
		Verdict:    "error",
		Err:        detail,
		OutputPath: path,
		Structured: data,
	}
}

// writeCheckFindings copies tool output to a run-owned name containing both
// iteration and attempt, so a later loop can never overwrite audit evidence.
func writeCheckFindings(req engine.StepRequest, data []byte) string {
	if req.Step == nil || req.Step.Type != workflow.StepCheck || req.TranscriptPath == "" {
		return ""
	}
	dir := filepath.Join(filepath.Dir(req.TranscriptPath), "evidence")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(dir, fmt.Sprintf("iteration-%03d-attempt-%03d.findings.json", req.Iteration, req.Attempt))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return ""
	}
	return path
}
