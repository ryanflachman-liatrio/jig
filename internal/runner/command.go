package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/transcript"
)

// CommandExecutor runs a workflow command step via the system shell.
// It implements engine.Executor for steps of type "command".
//
// Each step declares either Run (a single-line shell expression) or Script (a
// multi-line body or file path).  Both are handed to sh(1) so they can use
// pipes, redirections, and environment expansions exactly as a developer would
// type them at a terminal.
type CommandExecutor struct {
	// cwd is the working directory for all commands.  Empty string means the
	// process's current working directory, which is correct for `jig` invoked
	// from the repo root.  Phase 5 will set this per-worktree.
	cwd string
}

// commandWaitDelay bounds how long cmd.Wait blocks after the process exits (or
// is killed) for a lingering child that still holds the output pipe. After it
// elapses, os/exec forcibly closes the process I/O so Wait returns.
const commandWaitDelay = 10 * time.Second

// NewCommandExecutor returns a CommandExecutor whose commands run in cwd.
// Pass "" to inherit the process working directory.
func NewCommandExecutor(cwd string) *CommandExecutor {
	return &CommandExecutor{cwd: cwd}
}

// Execute runs the step's command (Run or Script) and streams output deltas to
// rep.  The returned Result reflects the process exit code: exit 0 →
// StatusSucceeded, any non-zero exit → StatusFailed.
func (e *CommandExecutor) Execute(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (*step.Result, error) {
	start := time.Now()

	cmdStr, err := resolveCommand(req.Step.Run, req.Step.Script)
	if err != nil {
		return &step.Result{
			Status:   step.StatusFailed,
			Err:      err.Error(),
			Duration: time.Since(start),
		}, nil
	}

	// Run via the POSIX shell so the command string can include pipes,
	// redirections, variable expansions, and other shell constructs.  Using
	// "sh -c" rather than parsing and splitting the command ourselves avoids
	// the many edge cases in shell tokenisation.
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	// Kill the whole process group (sh plus every child it spawns) on cancel, not
	// just the shell — otherwise pipeline/background children outlive a Stop and
	// can hold the output pipe open (see configureProcessGroup).
	configureProcessGroup(cmd)
	// Backstop: if a lingering child keeps the pipe's write-end open after the
	// process exits or is killed, Wait would otherwise block on the internal
	// output-copy goroutine forever. WaitDelay bounds that wait, then forcibly
	// closes the I/O so Wait always returns.
	cmd.WaitDelay = commandWaitDelay
	// Worktree takes precedence over the executor's static cwd (Phase 5).
	cwd := e.cwd
	if req.Worktree != "" {
		cwd = req.Worktree
	}
	if cwd != "" {
		cmd.Dir = cwd
	}

	// Pipe combined stdout+stderr so the TUI can stream both without ordering
	// surprises.  A separate goroutine forwards chunks to rep.Output so the
	// engine can display live progress before the process exits.
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		return &step.Result{
			Status:   step.StatusFailed,
			Err:      fmt.Sprintf("start command: %v", err),
			Duration: time.Since(start),
		}, nil
	}

	// drainDone signals that the reader goroutine has finished, so we only
	// read the final output after Wait returns.
	var combined strings.Builder
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		buf := make([]byte, 512)
		for {
			n, readErr := pr.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				combined.WriteString(chunk)
				rep.Output(chunk)
			}
			if readErr != nil {
				return
			}
		}
	}()

	waitErr := cmd.Wait()
	// Close the write-end so the reader goroutine sees EOF and exits.
	_ = pw.Close()
	<-drainDone

	duration := time.Since(start)

	// Persist the combined output so a command step has a navigable chain like an
	// agent step (Phase 6). The live rep.Output tail is ephemeral; this is the
	// durable record read by the monitor's chat view. Written on both success and
	// failure so a failed command's output survives.
	writeCommandTranscript(req, rep, combined.String())

	if waitErr != nil {
		return &step.Result{
			Status:   step.StatusFailed,
			Err:      waitErr.Error(),
			Duration: duration,
		}, nil
	}
	return &step.Result{
		Status:   step.StatusSucceeded,
		Duration: duration,
	}, nil
}

// writeCommandTranscript records a command step's combined stdout/stderr as a
// single system/text entry in the per-step transcript, then nudges the monitor
// via rep.Message so an open chat view re-reads. It is a no-op when persistence
// is off (empty TranscriptPath) or the command produced no output. The writer's
// byte cap truncates pathologically large output at write time.
func writeCommandTranscript(req engine.StepRequest, rep engine.Reporter, output string) {
	if req.TranscriptPath == "" || output == "" {
		return
	}
	w, err := transcript.Create(req.TranscriptPath)
	if err != nil {
		return
	}
	seq, appendErr := w.Append(transcript.Entry{
		Iteration: req.Iteration,
		Attempt:   req.Attempt,
		Role:      transcript.RoleSystem,
		Blocks:    []transcript.Block{{Type: transcript.BlockText, Text: output}},
	})
	if err := w.Close(); err != nil {
		return
	}
	if appendErr == nil {
		rep.Message(seq, req.Iteration)
	}
}

// resolveCommand returns the shell expression to pass to sh -c.
// Exactly one of run or script must be non-empty; the workflow validator
// enforces this at load time.
func resolveCommand(run, script string) (string, error) {
	if run != "" && script != "" {
		// Defensive: validator should have caught this, but fail loudly.
		return "", fmt.Errorf("step has both run and script set; only one is allowed")
	}
	if run != "" {
		return run, nil
	}
	if script != "" {
		// If script looks like a file path (starts with / or ./ or contains no
		// newline), try to read the file; otherwise treat it as an inline script body.
		if !strings.Contains(script, "\n") && (strings.HasPrefix(script, "/") || strings.HasPrefix(script, "./")) {
			body, err := os.ReadFile(script)
			if err != nil {
				return "", fmt.Errorf("read script file %q: %w", script, err)
			}
			return string(body), nil
		}
		return script, nil
	}
	return "", fmt.Errorf("command step has neither run nor script")
}
