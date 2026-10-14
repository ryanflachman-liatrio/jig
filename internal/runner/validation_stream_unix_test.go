//go:build unix

package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// This deliberately detached child is outside the host group guarantee. Its
// retained stream must still fence acceptance rather than hang indefinitely.
func TestD02DetachedOutputHelper(t *testing.T) {
	if os.Getenv("JIG_D02_DETACHED_HELPER") != "1" {
		return
	}
	if os.Getenv("JIG_D02_DETACHED_CHILD") == "1" {
		fmt.Fprintln(os.Stdout, "retained stream")
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestD02DetachedOutputHelper$")
	cmd.Env = append(os.Environ(), "JIG_D02_DETACHED_CHILD=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		os.Exit(2)
	}
	if err := os.WriteFile(os.Getenv("JIG_D02_PID_PATH"), []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
		_ = cmd.Process.Kill()
		os.Exit(3)
	}
	os.Exit(0)
}

func TestD02BlockedStreamFencesCommandAndCheck(t *testing.T) {
	for _, kind := range []workflow.StepType{workflow.StepCommand, workflow.StepCheck} {
		t.Run(string(kind), func(t *testing.T) {
			dir := t.TempDir()
			pidPath := filepath.Join(dir, "child.pid")
			t.Setenv("JIG_D02_DETACHED_HELPER", "1")
			t.Setenv("JIG_D02_PID_PATH", pidPath)
			t.Cleanup(func() {
				raw, err := os.ReadFile(pidPath)
				if err != nil {
					return
				}
				pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
				if err == nil {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			})
			req := engine.StepRequest{Step: &workflow.Step{ID: "worker", Type: kind, Run: fmt.Sprintf("%q -test.run=^TestD02DetachedOutputHelper$", os.Args[0]), Findings: &workflow.CheckFindings{File: "findings.json"}}, ExecutionDir: dir}
			var executor engine.Executor = NewCommandExecutor("")
			if kind == workflow.StepCheck {
				executor = NewCheckExecutor("")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			result, err := executor.Execute(ctx, req, &noopReporter{})
			if err != nil || result == nil || !result.ExecutionUncertain {
				t.Fatalf("blocked retained stream lost uncertainty: %+v %v", result, err)
			}
		})
	}
}
