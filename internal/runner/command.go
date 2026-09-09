package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/transcript"
	"jig/internal/workflow"
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

	cmdStr, err := resolveCommand(req.Step.Run, req.Step.Script, req.RepoRoot)
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
	cmd.Env = append(os.Environ(), commandInputEnv(req.Inputs)...)
	cmd.Env = append(cmd.Env, commandSecretEnv(req.Secrets)...)
	cmd.Env = append(cmd.Env, commandFanOutEnv(req.FanOutItem)...)
	// Kill the whole process group (sh plus every child it spawns) on cancel, not
	// just the shell — otherwise pipeline/background children outlive a Stop and
	// can hold the output pipe open (see configureProcessGroup).
	configureProcessGroup(cmd)
	// Backstop: if a lingering child keeps the pipe's write-end open after the
	// process exits or is killed, Wait would otherwise block on the internal
	// output-copy goroutine forever. WaitDelay bounds that wait, then forcibly
	// closes the I/O so Wait always returns.
	cmd.WaitDelay = commandWaitDelay
	// A dispatch snapshot takes precedence over the executor's configured CWD.
	// The latter is the persistence-off fallback when no run-owned view exists.
	cwd := e.cwd
	if req.ExecutionDir != "" {
		cwd = req.ExecutionDir
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
				rep.Output(redactSecrets(req, chunk))
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
	output := redactSecrets(req, combined.String())
	writeCommandTranscript(req, rep, output)
	evidencePath := writeCheckEvidence(req, output)

	if waitErr != nil {
		return &step.Result{
			Status:     step.StatusFailed,
			OutputPath: evidencePath,
			Err:        waitErr.Error(),
			Duration:   duration,
		}, nil
	}
	return &step.Result{
		Status:     step.StatusSucceeded,
		OutputPath: evidencePath,
		Duration:   duration,
	}, nil
}

// normalizeEnvName upper-cases name and replaces every rune outside [A-Z0-9]
// with '_' — the single naming convention shared by declared secrets,
// declared artifact inputs, and fan-out item bindings, so a command step's
// environment always builds names the same way no matter which of the three
// produced them.
func normalizeEnvName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func commandSecretEnv(secrets map[string]string) []string {
	var env []string
	for name, value := range secrets {
		env = append(env, "JIG_SECRET_"+normalizeEnvName(name)+"="+value)
	}
	return env
}

// commandFanOutEnv exports a fan-out child's bound item and position metadata
// to a command step's environment (docs/plans/a8-dynamic-foreach-fan-out.md,
// "Runtime identity and item delivery"). item is nil for every ordinary step,
// in which case this returns nil and no JIG_FANOUT_*/extra JIG_INPUT_* vars
// are added — ordinary command steps are byte-for-byte unaffected.
// item.Item is already compact, canonical JSON (see engine.FanOutItem), so it
// is exported as-is rather than re-encoded.
func commandFanOutEnv(item *engine.FanOutItem) []string {
	if item == nil {
		return nil
	}
	return []string{
		"JIG_INPUT_" + normalizeEnvName(item.As) + "=" + string(item.Item),
		fmt.Sprintf("JIG_FANOUT_INDEX=%d", item.Index),
		fmt.Sprintf("JIG_FANOUT_TOTAL=%d", item.Total),
		"JIG_FANOUT_INSTANCE_ID=" + item.InstanceID,
	}
}

func redactSecrets(req engine.StepRequest, text string) string {
	for _, secret := range req.Secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
	}
	return text
}

// commandInputEnv exposes only named artifact inputs to shell commands. Their
// values are absolute paths beneath the run directory, never producer-worktree
// paths, so a consumer cannot accidentally depend on another isolated checkout.
func commandInputEnv(inputs []engine.ResolvedInput) []string {
	var env []string
	for _, input := range inputs {
		if input.Ref.Artifact == "" {
			continue
		}
		name := input.Ref.As
		if name == "" {
			name = input.Ref.Artifact
		}
		env = append(env, "JIG_INPUT_"+normalizeEnvName(name)+"="+input.Value)
	}
	return env
}

// writeCheckEvidence preserves each deterministic check attempt independently,
// rather than pointing later loop iterations at a shared .jig/*.txt file.
func writeCheckEvidence(req engine.StepRequest, output string) string {
	if req.Step == nil || req.Step.Type != workflow.StepCheck || req.TranscriptPath == "" {
		return ""
	}
	dir := filepath.Join(filepath.Dir(req.TranscriptPath), "evidence")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(dir, fmt.Sprintf("generation-%03d-iteration-%03d-attempt-%03d.log", req.Generation, req.Iteration, req.Attempt))
	if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
		return ""
	}
	return path
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
//
// A `script` is a file path resolved against repoRoot (the project root) — the
// same anchor the validator checked at load time, via workflow.ScriptPath — so
// the path that validated is the path that runs, regardless of the execution
// cwd (which stays the step worktree so the script operates on that code). A
// multi-line `script` is treated as an inline shell body, not a path. When
// repoRoot is "" (persistence-off / tests) the path is resolved against the cwd.
func resolveCommand(run, script, repoRoot string) (string, error) {
	if run != "" && script != "" {
		// Defensive: validator should have caught this, but fail loudly.
		return "", fmt.Errorf("step has both run and script set; only one is allowed")
	}
	if run != "" {
		return run, nil
	}
	if script != "" {
		// A multi-line value is an inline script body; a single-line value is a
		// path to a script file (resolved from the project root).
		if strings.Contains(script, "\n") {
			return script, nil
		}
		path := workflow.ScriptPath(repoRoot, script)
		body, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read script file %q: %w", path, err)
		}
		return string(body), nil
	}
	return "", fmt.Errorf("command step has neither run nor script")
}
