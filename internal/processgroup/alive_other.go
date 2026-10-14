//go:build unix && !linux && !darwin

package processgroup

import "syscall"

func groupAlive(pgid int) (bool, error) {
	err := syscall.Kill(-pgid, 0)
	if err == syscall.ESRCH {
		return false, nil
	}
	return true, err
}
