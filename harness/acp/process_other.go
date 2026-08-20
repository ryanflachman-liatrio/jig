//go:build !unix

package acp

import "os/exec"

// configureProcess is a no-op on non-Unix platforms.
func configureProcess(cmd *exec.Cmd) {}

// killProcess falls back to killing only the root process on non-Unix platforms.
func killProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
