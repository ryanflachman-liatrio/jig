//go:build !unix

package runner

import (
	"github.com/ryanflachman-liatrio/jig/internal/processgroup"
	"os/exec"
)

// configureProcessGroup is a no-op on non-Unix platforms; the default
// exec.CommandContext cancellation (SIGKILL to the process) is used as-is.
func configureProcessGroup(cmd *exec.Cmd) {}

func finishProcessGroup(cmd *exec.Cmd) error { return processgroup.Confirm(cmd) }
