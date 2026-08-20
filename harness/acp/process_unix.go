//go:build unix

package acp

import (
	"os/exec"
	"syscall"
)

// configureProcess puts cmd in its own process group and wires cmd.Cancel to
// kill that entire group. The ACP adapter is launched as
// "npx -y @zed-industries/claude-code-acp@latest", which spawns a Node.js
// child process. The default Kill() only signals the npx PID, leaving Node.js
// alive with the stderr pipe write-end open. That prevents the copy goroutine
// started by cmd.Stderr (a non-*os.File writer) from ever getting EOF, which
// in turn wedges cmd.Wait() indefinitely. Killing the process group reaps the
// entire tree so cmd.Wait() can complete.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return killProcess(cmd)
	}
}

// killProcess sends SIGKILL to the process group led by cmd.Process, killing
// npx and every child it has spawned (including the Node.js adapter).
func killProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
