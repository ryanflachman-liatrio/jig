//go:build unix

package runner

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup makes cmd the leader of a new process group and rewrites
// its cancellation to kill that whole group, not just the shell leader.
//
// A command step runs as `sh -c <expr>`, and <expr> routinely spawns children
// (pipelines, `&` background jobs, sub-processes). The default
// exec.CommandContext cancel sends SIGKILL to sh's PID alone, orphaning those
// children — they survive a Run.Stop / run cancel and can keep the step's output
// pipe open, wedging cmd.Wait. Setpgid puts sh (and, by inheritance, everything
// it spawns) in a group whose id equals sh's pid, so a single SIGKILL to the
// negated pid reaps the entire tree.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid targets the process group led by cmd.Process.Pid.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
