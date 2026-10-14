//go:build !unix

package processgroup

import (
	"fmt"
	"os/exec"
)

func Kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
func Confirm(*exec.Cmd) error { return fmt.Errorf("managed process group confirmation unsupported") }
