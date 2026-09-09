package headless

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"jig/internal/engine"
	"jig/internal/workflow"
)

// writer emits progress to stderr and the final payload to stdout per OutputMode.
type writer struct {
	mode   OutputMode
	quiet  bool
	stdout io.Writer
	stderr io.Writer
}

func newWriter(opts Options) *writer {
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	mode := opts.Output
	if mode == "" {
		mode = OutputText
	}
	return &writer{mode: mode, quiet: opts.Quiet, stdout: stdout, stderr: stderr}
}

func (w *writer) progress(format string, args ...any) {
	if w.quiet {
		return
	}
	fmt.Fprintf(w.stderr, format+"\n", args...)
}

func (w *writer) warn(format string, args ...any) {
	fmt.Fprintf(w.stderr, "warning: "+format+"\n", args...)
}

func (w *writer) errf(format string, args ...any) {
	fmt.Fprintf(w.stderr, "error: "+format+"\n", args...)
}

func (w *writer) runID(id string) {
	// Early run id always goes to stderr — even under --quiet / json — for forensics.
	fmt.Fprintf(w.stderr, "run_id: %s\n", id)
}

func (w *writer) event(ev engine.Event) {
	switch e := ev.(type) {
	case engine.StepStatus:
		w.progress("%s %s", e.StepID, e.To)
	case engine.RunStarted:
		w.progress("started %s (%d steps)", e.Workflow, len(e.Steps))
	case engine.FanOutExpanded:
		// One concise expansion line per the plan ("Headless" operator-surface
		// section); ordinary child StepStatus/gate events follow through the
		// normal case above using their full runtime instance id.
		w.progress("%s: expanded to %d item(s)", e.FamilyID, len(e.Instances))
	}
	if w.mode == OutputJSONL {
		w.jsonlEvent(ev)
	}
}

func (w *writer) jsonlEvent(ev engine.Event) {
	type line struct {
		Type string `json:"type"`
		Data any    `json:"data"`
	}
	b, err := json.Marshal(line{Type: eventTypeName(ev), Data: ev})
	if err != nil {
		return
	}
	fmt.Fprintf(w.stdout, "%s\n", b)
}

func eventTypeName(ev engine.Event) string {
	switch ev.(type) {
	case engine.RunStarted:
		return "run_started"
	case engine.RunFinished:
		return "run_finished"
	case engine.StepStatus:
		return "step_status"
	case engine.ReviewRequest:
		return "review_request"
	case engine.PromptRequest:
		return "prompt_request"
	case engine.InputRequest:
		return "input_request"
	case engine.AgentQuestion:
		return "agent_question"
	case engine.RecoveryRequest:
		return "recovery_request"
	case engine.IntegrationConflictRequest:
		return "integration_conflict_request"
	case engine.FinalMergeRequest:
		return "final_merge_request"
	case engine.RunError:
		return "run_error"
	case engine.FanOutExpanded:
		return "fanout_expanded"
	default:
		return fmt.Sprintf("%T", ev)
	}
}

func (w *writer) finish(env Envelope) {
	switch w.mode {
	case OutputJSON:
		enc := json.NewEncoder(w.stdout)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(env)
	case OutputJSONL:
		type finalLine struct {
			Type string   `json:"type"`
			Data Envelope `json:"data"`
		}
		enc := json.NewEncoder(w.stdout)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(finalLine{Type: "result", Data: env})
	default:
		status := "ok"
		if !env.OK {
			status = "failed"
		}
		if env.Error != nil {
			status = env.Error.Code
		}
		fmt.Fprintf(w.stdout, "%s run_id=%s status=%s\n", env.Workflow, env.RunID, status)
	}
}

func buildEnvelope(mgr *engine.Manager, wf *workflow.Workflow, snap engine.RunSnapshot, fin engine.RunFinished, terminal error) Envelope {
	env := Envelope{
		OK:           terminal == nil && !fin.Failed,
		RunID:        snap.ID,
		Workflow:     wf.Meta.Name,
		Failed:       fin.Failed,
		TotalCostUSD: snap.TotalCostUSD,
		TotalTokens:  snap.TotalTokens,
		RunDir:       mgr.RunDir(snap.ID),
	}
	if env.RunDir == "" && mgr.Root() != "" {
		env.RunDir = filepath.Join(mgr.Root(), "runs", snap.ID)
	}
	if terminal != nil {
		env.OK = false
		env.Error = errorInfo(terminal)
	} else if fin.Failed {
		env.OK = false
		env.Error = &ErrorInfo{Code: "run_failed", Message: "run finished with failures"}
	}
	return env
}

func errorInfo(err error) *ErrorInfo {
	var g *GateError
	if errors.As(err, &g) {
		return &ErrorInfo{Code: g.Code, Message: g.Message, StepID: g.StepID}
	}
	var t *TimeoutError
	if errors.As(err, &t) {
		return &ErrorInfo{Code: "timeout", Message: t.Error()}
	}
	if isInterrupted(err) {
		return &ErrorInfo{Code: "interrupted", Message: err.Error()}
	}
	return &ErrorInfo{Code: "error", Message: err.Error()}
}
