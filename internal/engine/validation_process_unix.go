//go:build unix

package engine

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Cancel the shell and its pipelines/background children as one process group.
func configureValidationProcess(cmd *exec.Cmd) func() error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	// WaitDelay can return after an already-exited shell without invoking
	// Cancel. Reclaim background children before releasing the workspace too.
	return func() error {
		err := cmd.Cancel()
		if errors.Is(err, os.ErrProcessDone) {
			return nil // the shell and all same-group children already exited
		}
		return err
	}
}

func openValidationOutput(path string) (*os.File, error) {
	// Opening a FIFO with os.Open would block before we can inspect its mode.
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
