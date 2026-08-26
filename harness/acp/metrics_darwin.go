//go:build darwin

package acp

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func sampleResources(rootPID int) ResourceSnapshot {
	s := ResourceSnapshot{Timestamp: time.Now().UTC(), RootPID: rootPID}
	if rootPID <= 0 {
		s.CaptureError = "process has not started"
		return s
	}
	pgid, err := syscall.Getpgid(rootPID)
	if err != nil {
		s.CaptureError = fmt.Sprintf("get process group: %v", err)
		return s
	}
	s.ProcessGroupID = pgid
	out, err := exec.Command("ps", "-axo", "pid=,pgid=,rss=").Output()
	if err != nil {
		s.CaptureError = fmt.Sprintf("list processes: %v", err)
		return s
	}
	var pids []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		linePGID, pgErr := strconv.Atoi(fields[1])
		rssKB, rssErr := strconv.ParseUint(fields[2], 10, 64)
		if pgErr != nil || rssErr != nil || linePGID != pgid {
			continue
		}
		s.ProcessGroupCount++
		s.RSSBytes += rssKB * 1024
		pids = append(pids, fields[0])
	}
	if len(pids) == 0 {
		return s
	}
	// lsof's field mode emits only descriptor identifiers, never the paths or
	// command arguments attached to those descriptors.
	fdOut, err := exec.Command("lsof", "-n", "-P", "-Ff", "-p", strings.Join(pids, ",")).Output()
	if err != nil {
		s.CaptureError = fmt.Sprintf("count file descriptors: %v", err)
		return s
	}
	for _, line := range strings.Split(string(fdOut), "\n") {
		if strings.HasPrefix(line, "f") {
			s.OpenFDCount++
		}
	}
	return s
}
