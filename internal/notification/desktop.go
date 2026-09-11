package notification

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// CommandRunner is the seam every desktop sender uses. Tests inject a fake
// runner instead of exec-ing a real helper.
type CommandRunner func(ctx context.Context, name string, args []string, env []string) error

// defaultCommandRunner runs a helper subprocess with the current environment.
// It never captures stderr because that output can contain notification
// content that must not be leaked into diagnostics or logs.
func defaultCommandRunner(ctx context.Context, name string, args []string, env []string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	return cmd.Run()
}

const desktopAttemptTimeout = 3 * time.Second

// DesktopSender is the local platform notification adapter. Selection is
// runtime-detected: macOS uses osascript, Linux uses notify-send, and other
// platforms are diagnosed as unsupported.
type DesktopSender struct {
	Runner   CommandRunner
	Environ  func() []string
	Platform string
	LookPath func(string) (string, error)
}

// NewDesktopSender returns a sender bound to the running platform.
func NewDesktopSender() *DesktopSender {
	return &DesktopSender{
		Runner:   defaultCommandRunner,
		Environ:  os.Environ,
		Platform: runtime.GOOS,
		LookPath: exec.LookPath,
	}
}

// Send dispatches one attempt via the platform helper. Errors are recorded
// as diagnostic reason codes, never raw helper output.
func (d *DesktopSender) Send(ctx context.Context, target Binding, payload OutboundPayload) SendResult {
	runner := d.Runner
	if runner == nil {
		runner = defaultCommandRunner
	}
	env := d.Environ
	if env == nil {
		env = os.Environ
	}
	look := d.LookPath
	if look == nil {
		look = exec.LookPath
	}
	platform := d.Platform
	if platform == "" {
		platform = runtime.GOOS
	}

	attemptCtx, cancel := context.WithTimeout(ctx, desktopAttemptTimeout)
	defer cancel()

	start := time.Now()
	switch platform {
	case "darwin":
		if _, err := look("/usr/bin/osascript"); err != nil {
			return SendResult{Reason: "desktop_helper_missing", Err: errors.New("helper missing"), Duration: time.Since(start)}
		}
		script := `on run argv
	display notification (item 2 of argv) with title (item 1 of argv)
end run`
		err := runner(attemptCtx, "/usr/bin/osascript", []string{"-e", script, payload.DesktopTitle, payload.DesktopBody}, env())
		return classifyDesktop(err, attemptCtx, time.Since(start))
	case "linux":
		if _, err := look("notify-send"); err != nil {
			return SendResult{Reason: "desktop_helper_missing", Err: errors.New("helper missing"), Duration: time.Since(start)}
		}
		hasSession := false
		for _, e := range env() {
			if len(e) > len("DBUS_SESSION_BUS_ADDRESS=") && e[:len("DBUS_SESSION_BUS_ADDRESS=")] == "DBUS_SESSION_BUS_ADDRESS=" {
				hasSession = true
				break
			}
		}
		if !hasSession {
			return SendResult{Reason: "desktop_session_missing", Err: errors.New("no dbus session"), Duration: time.Since(start)}
		}
		err := runner(attemptCtx, "notify-send", []string{"--app-name=jig", payload.DesktopTitle, payload.DesktopBody}, env())
		return classifyDesktop(err, attemptCtx, time.Since(start))
	default:
		return SendResult{Reason: "desktop_unsupported", Err: errors.New("desktop unsupported"), Duration: time.Since(start)}
	}
}

func classifyDesktop(err error, ctx context.Context, dur time.Duration) SendResult {
	if err == nil {
		return SendResult{Reason: "delivered", Duration: dur}
	}
	if ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return SendResult{Reason: "timeout", Err: errors.New("attempt timeout"), Duration: dur}
	}
	return SendResult{Reason: "desktop_submission_failed", Err: errors.New("submission failed"), Duration: dur}
}
