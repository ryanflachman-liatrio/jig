package processgroup

import (
	"fmt"
	"golang.org/x/sys/unix"
	"syscall"
)

func groupAlive(pgid int) (bool, error) {
	if err := syscall.Kill(-pgid, 0); err == syscall.ESRCH {
		return false, nil
	}
	// Darwin can reject signals to an all-zombie group. Inspect process state
	// rather than treating signal permission as proof of a live workload.
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pgid)
	if err != nil {
		if syscall.Kill(-pgid, 0) == syscall.ESRCH {
			return false, nil
		}
		return false, fmt.Errorf("probe managed group: %w", err)
	}
	for _, p := range procs {
		if p.Proc.P_stat != 5 {
			return true, nil
		}
	}
	return false, nil
}
