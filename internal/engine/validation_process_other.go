//go:build !unix

package engine

import (
	"os"
	"os/exec"
)

// Other platforms retain exec.CommandContext's direct-process cancellation.
func configureValidationProcess(cmd *exec.Cmd) func() error { return func() error { return nil } }

func openValidationOutput(path string) (*os.File, error) { return os.Open(path) }
