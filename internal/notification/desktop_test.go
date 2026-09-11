package notification

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"jig/internal/workflow"
)

func TestDesktopSenderMacOSInvokesOsascript(t *testing.T) {
	var captured atomic.Value
	sender := &DesktopSender{
		Platform: "darwin",
		LookPath: func(name string) (string, error) {
			if name != "/usr/bin/osascript" {
				t.Fatalf("unexpected helper: %s", name)
			}
			return name, nil
		},
		Environ: func() []string { return nil },
		Runner: func(ctx context.Context, name string, args []string, env []string) error {
			captured.Store(map[string]any{"name": name, "args": args})
			return nil
		},
	}
	target := NewBinding("desktop", Desktop, []workflow.NotificationEvent{workflow.AttentionRequired}, "", "")
	res := sender.Send(context.Background(), target, newTestPayload())
	if !res.Success() {
		t.Fatalf("expected delivered, got %+v", res)
	}
	got := captured.Load().(map[string]any)
	if got["name"] != "/usr/bin/osascript" {
		t.Fatalf("unexpected exec: %s", got["name"])
	}
}

func TestDesktopSenderLinuxRequiresSession(t *testing.T) {
	sender := &DesktopSender{
		Platform: "linux",
		LookPath: func(name string) (string, error) { return "notify-send", nil },
		Environ:  func() []string { return nil },
		Runner:   func(ctx context.Context, name string, args []string, env []string) error { return nil },
	}
	res := sender.Send(context.Background(), NewBinding("desktop", Desktop, nil, "", ""), newTestPayload())
	if res.Reason != "desktop_session_missing" {
		t.Fatalf("expected desktop_session_missing, got %+v", res)
	}
}

func TestDesktopSenderLinuxSuccess(t *testing.T) {
	sender := &DesktopSender{
		Platform: "linux",
		LookPath: func(name string) (string, error) { return "notify-send", nil },
		Environ:  func() []string { return []string{"DBUS_SESSION_BUS_ADDRESS=unix:path=/tmp/dbus"} },
		Runner:   func(ctx context.Context, name string, args []string, env []string) error { return nil },
	}
	res := sender.Send(context.Background(), NewBinding("desktop", Desktop, nil, "", ""), newTestPayload())
	if !res.Success() {
		t.Fatalf("expected delivered, got %+v", res)
	}
}

func TestDesktopSenderUnsupportedPlatform(t *testing.T) {
	sender := &DesktopSender{
		Platform: "windows",
		LookPath: func(name string) (string, error) { return "", errors.New("no") },
		Environ:  func() []string { return nil },
		Runner:   func(ctx context.Context, name string, args []string, env []string) error { return nil },
	}
	res := sender.Send(context.Background(), NewBinding("desktop", Desktop, nil, "", ""), newTestPayload())
	if res.Reason != "desktop_unsupported" {
		t.Fatalf("expected desktop_unsupported, got %+v", res)
	}
}

func TestDesktopSenderTimeout(t *testing.T) {
	sender := &DesktopSender{
		Platform: "linux",
		LookPath: func(name string) (string, error) { return "notify-send", nil },
		Environ:  func() []string { return []string{"DBUS_SESSION_BUS_ADDRESS=unix"} },
		Runner: func(ctx context.Context, name string, args []string, env []string) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	res := sender.Send(ctx, NewBinding("desktop", Desktop, nil, "", ""), newTestPayload())
	if res.Reason != "timeout" && res.Reason != "desktop_submission_failed" {
		t.Fatalf("expected timeout/failure classification, got %+v", res)
	}
}
