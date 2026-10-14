//go:build unix

package runner

import (
	"os"
	"syscall"
)

// Nonblocking open prevents a producer-controlled FIFO from defeating Stop or
// the deadline before the regular-file check. The descriptor is then bounded.
func openValidationOutput(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
