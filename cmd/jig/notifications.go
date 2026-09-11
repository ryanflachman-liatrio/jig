package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"jig/internal/notification"
	"jig/internal/workflow"
)

func runNotifications(args []string) int {
	return notificationsCheck(args, os.Stdout, os.Stderr, notification.Inspection{ResolveSecret: resolveNamedSecret})
}

func notificationsCheck(args []string, stdout, stderr io.Writer, deps notification.Inspection) int {
	const usage = "usage: jig notifications check <workflow.toml> [--root PATH]"
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	fs := flag.NewFlagSet("notifications check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".jig", "persistence root")
	reordered, err := reorderArgs(args[1:], map[string]bool{"root": true}, 1)
	if err != nil {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if err := fs.Parse(reordered); err != nil {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if len(fs.Args()) != 1 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	wf, err := workflow.Load(fs.Args()[0])
	if err != nil {
		// Invalid TOML may include inline credentials or control characters. Static
		// validation is available separately; readiness never echoes file contents.
		fmt.Fprintln(stderr, "workflow_invalid: workflow/profile validation failed; run jig validate for details")
		return 1
	}
	report := notification.Inspect(wf.NotificationPolicy(), *root, deps)
	report.Render(stdout)
	if report.Problem {
		return 1
	}
	return 0
}
