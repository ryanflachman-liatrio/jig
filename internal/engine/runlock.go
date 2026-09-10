package engine

import (
	"fmt"
	"os"
	"syscall"

	"jig/internal/datastore"
)

// RunLease is the scheduler ownership descriptor. Keeping its file open is
// essential: advisory locks are released when that descriptor is closed.
type RunLease struct {
	file *os.File
	dev  uint64
	ino  uint64
}

// AcquireRunLease acquires the same non-blocking ownership lease used by
// Start and Resume. It creates an absent coordination file but never truncates
// an existing one.
func AcquireRunLease(runDir string) (*RunLease, error) {
	if runDir == "" {
		return nil, nil
	}
	f, err := os.OpenFile(datastore.SchedulerLockPath(runDir), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("run already has a live scheduler")
	}
	info, err := f.Stat()
	if err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, fmt.Errorf("inspect scheduler lock: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, fmt.Errorf("inspect scheduler lock identity")
	}
	return &RunLease{file: f, dev: uint64(stat.Dev), ino: stat.Ino}, nil
}

// Identity returns the stable filesystem identity captured at acquisition.
func (l *RunLease) Identity() (dev, ino uint64, ok bool) {
	if l == nil || l.file == nil {
		return 0, 0, false
	}
	return l.dev, l.ino, true
}

// Close releases ownership. It is safe to call repeatedly.
func (l *RunLease) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	unlockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	closeErr := f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

// RunLockState reports whether another process owns the scheduler lock. A
// missing lock is free; probing never creates it or retains a lease.
func RunLockState(runDir string) (bool, error) {
	if runDir == "" {
		return false, fmt.Errorf("engine: persistence required to inspect a run lock")
	}
	info, err := os.Stat(runDir)
	if err != nil {
		return false, fmt.Errorf("engine: inspect run directory: %w", err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("engine: run path is not a directory")
	}
	f, err := os.Open(datastore.SchedulerLockPath(runDir))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("engine: open scheduler lock: %w", err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return true, nil
		}
		return false, fmt.Errorf("engine: probe scheduler lock: %w", err)
	}
	return false, syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
