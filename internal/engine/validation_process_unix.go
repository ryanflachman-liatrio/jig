//go:build unix

package engine

import (
	"os/exec"
	"syscall"
)

// Cancel the shell and its pipelines/background children as one process group.
func configureValidationProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
