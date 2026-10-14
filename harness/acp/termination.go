package acp

import (
	"context"
	"errors"
	"os/exec"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/processgroup"
)

// closeAdapterProcess keeps teardown finite even when signaling a managed
// process fails. The waiter owns cmd.Wait until the OS reports its termination;
// uncertainty retains the execution roots rather than authorizing replacement.
func closeAdapterProcess(cmd *exec.Cmd) (waitErr, closeErr error) {
	killErr := killProcess(cmd)
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	select {
	case waitErr = <-waited:
	case <-timer.C:
		return nil, &processgroup.UncertainError{Cause: errors.Join(killErr, context.DeadlineExceeded)}
	}
	var exited *exec.ExitError
	reaped := waitErr == nil || errors.As(waitErr, &exited) || (cmd.ProcessState != nil && (errors.Is(waitErr, context.Canceled) || errors.Is(waitErr, context.DeadlineExceeded)))
	if reaped {
		if err := processgroup.Confirm(cmd); err == nil {
			return waitErr, nil
		} else {
			return waitErr, &processgroup.UncertainError{Cause: err}
		}
	}
	return waitErr, &processgroup.UncertainError{Cause: errors.Join(killErr, waitErr)}
}
