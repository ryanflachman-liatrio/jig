//go:build darwin

package acp

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// P3 qualification probe: Darwin's kqueue must not be mistaken for FreeBSD's
// recursive process tracker. NOTE_TRACK has been unsupported since macOS 10.5.
// This tests the real host API without launching or observing unrelated jobs.
func TestP3DarwinRecursiveProcessTrackingUnavailable(t *testing.T) {
	kq, err := unix.Kqueue()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(kq) })
	change := unix.Kevent_t{
		Ident:  uint64(os.Getpid()),
		Filter: unix.EVFILT_PROC,
		Flags:  unix.EV_ADD,
		Fflags: unix.NOTE_EXIT | unix.NOTE_TRACK,
	}
	_, err = unix.Kevent(kq, []unix.Kevent_t{change}, nil, nil)
	if !errors.Is(err, unix.ENOTSUP) {
		t.Fatalf("recursive tracking result=%v; reassess host qualification", err)
	}
	t.Log(`{"recursive_process_tracking":"unsupported","error":"ENOTSUP","safe_root_reuse_authorized":false}`)
}
