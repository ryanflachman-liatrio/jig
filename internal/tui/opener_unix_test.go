//go:build unix

package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

func TestOpenLocationLaunchRunningCancellation(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "ready")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateOpenLocationPath(fifo); err == nil {
		t.Fatal("accepted FIFO target")
	}
	// RDWR avoids blocking on open. The helper's write is the readiness signal;
	// no sleep is used to infer that the launcher is running.
	f, err := os.OpenFile(fifo, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ready := make(chan error, 1)
	go func() { var b [1]byte; _, err := io.ReadFull(f, b[:]); ready <- err }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	msg := openerPreparedMsg{id: 1, argv: openerHelperArgv(t, "wait", fifo)}
	done := make(chan shared.OpenLocationResult, 1)
	go func() { done <- launchOpenLocation(ctx, msg, nil)().(shared.OpenLocationResult) }()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("helper did not signal readiness")
	}
	cancel()
	select {
	case result := <-done:
		if !strings.Contains(result.Notice, "canceled") {
			t.Fatal(result.Notice)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("launcher was not canceled/reaped")
	}
}

func TestOpenLocationLaunchQuitReaps(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "ready")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(fifo, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ready := make(chan error, 1)
	go func() { var b [1]byte; _, err := io.ReadFull(f, b[:]); ready <- err }()
	m, req := openerRoot(t)
	m.tuiConfig.Opener.Argv = append(openerHelperArgv(t, "wait", fifo), "{path}")
	m, prepare := m.admitOpenLocation(req)
	m, launch := m.preparedOpenLocation(prepare().(openerPreparedMsg))
	reaped := m.opener.done
	result := make(chan shared.OpenLocationResult, 1)
	go func() { result <- launch().(shared.OpenLocationResult) }()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("helper not ready")
	}
	_, quit, _ := m.handleGlobalKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	finished := make(chan tea.Msg, 1)
	go func() { finished <- quit() }()
	select {
	case msg := <-finished:
		if _, ok := msg.(tea.QuitMsg); !ok {
			t.Fatalf("quit=%T", msg)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("quit did not reap")
	}
	select {
	case <-reaped:
	default:
		t.Fatal("quit returned before reaping")
	}
	select {
	case msg := <-result:
		if msg.Notice != "Open canceled" {
			t.Fatal(msg.Notice)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("missing completion")
	}
}
