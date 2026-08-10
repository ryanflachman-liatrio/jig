//go:build !unix

package runner

import "os/exec"

// configureProcessGroup is a no-op on non-Unix platforms; the default
// exec.CommandContext cancellation (SIGKILL to the process) is used as-is.
func configureProcessGroup(cmd *exec.Cmd) {}
