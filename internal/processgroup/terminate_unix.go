//go:build unix

// Package processgroup owns bounded termination of managed host process groups.
// It does not certify descendants that detach from that group.
package processgroup

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// Kill requests termination. Wait and Confirm establish the final outcome.
func Kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return nil
	}
	if err != nil {
		// Some hosts reject signals to all-zombie groups. Only a successful
		// inspection proving no live members can forgive this signal error.
		alive, inspectErr := groupAlive(cmd.Process.Pid)
		if inspectErr == nil && !alive {
			return nil
		}
		return fmt.Errorf("kill managed group: %w", err)
	}
	return nil
}

// Confirm uses a cleanup deadline independent of an expired attempt context.
// Zombies cannot execute or hold workspace/pipe descriptors and are excluded.
func Confirm(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// A concurrent cancellation may already be killing the group. A signal
	// error alone cannot distinguish that transition from a live fenced group;
	// authoritative absence within the cleanup deadline resolves it.
	killErr := Kill(cmd)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		alive, err := groupAlive(cmd.Process.Pid)
		if err != nil {
			return errors.Join(killErr, err)
		}
		if !alive {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return errors.Join(killErr, ctx.Err())
		}
	}
}
