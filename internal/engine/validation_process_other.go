//go:build !unix

package engine

import "os/exec"

// Other platforms retain exec.CommandContext's direct-process cancellation.
func configureValidationProcess(cmd *exec.Cmd) {}
