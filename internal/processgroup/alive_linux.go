package processgroup

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func groupAlive(pgid int) (bool, error) {
	if err := syscall.Kill(-pgid, 0); err == syscall.ESRCH {
		return false, nil
	} else if err != nil {
		return false, err
	}
	dirs, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	for _, d := range dirs {
		if _, err := strconv.Atoi(d.Name()); err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", d.Name(), "stat"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		end := strings.LastIndexByte(string(raw), ')')
		if end < 0 {
			continue
		}
		fields := strings.Fields(string(raw[end+1:]))
		if len(fields) < 3 {
			continue
		}
		group, err := strconv.Atoi(fields[2])
		if err != nil {
			return false, err
		}
		if group == pgid && fields[0] != "Z" && fields[0] != "X" {
			return true, nil
		}
	}
	return false, nil
}
