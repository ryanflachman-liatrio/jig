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

func openContainerInput(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}

func singleLinkFile(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}
