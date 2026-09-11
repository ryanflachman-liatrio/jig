package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"jig/internal/runexport"
)

const exportUsage = `usage: jig export RUN_ID --destination PATH [--root PATH] [--include-text]

Exports a local, offline diagnostic ZIP archive for one inactive run.

Content modes:
  structural (default)   run/step summary, event timeline, and totals only;
                          no prompts, code, tool payloads, or raw identifiers.
  --include-text          adds transcript.jsonl: best-effort sanitized
                          conversation text. Sanitization is not a guarantee
                          of anonymity -- review the archive before sharing
                          it further.

Eligible run states: succeeded, failed, interrupted, paused, and orphaned
runs with at least one safely projectable journal or transcript record. A
run with a live scheduler (including one parked at a gate) is refused.

This command is local-only: it never uploads, prompts, or contacts a
backend or network service, and it never overwrites an existing destination.`

// runExport is the process entry point for "jig export".
func runExport(args []string) int {
	return exportMain(args, os.Stdout, os.Stderr)
}

// exportMain is the injected-writer core so tests can assert exact
// stdout/stderr/exit behavior without touching the real process streams.
func exportMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".jig", "persistence root")
	destination := fs.String("destination", "", "destination ZIP path (required)")
	includeText := fs.Bool("include-text", false, "add best-effort sanitized conversation text (residual risk; review before sharing)")

	reordered, err := reorderArgs(args, map[string]bool{"root": true, "destination": true}, 1)
	if err != nil {
		fmt.Fprintln(stderr, exportUsage)
		return 2
	}
	if err := fs.Parse(reordered); err != nil {
		fmt.Fprintln(stderr, exportUsage)
		return 2
	}
	if len(fs.Args()) != 1 || *destination == "" {
		fmt.Fprintln(stderr, exportUsage)
		return 2
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	var gotSignal os.Signal
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case sig := <-sigCh:
			gotSignal = sig
			cancel()
		case <-done:
		}
	}()

	result, exportErr := runexport.Export(ctx, runexport.Options{
		Root: *root, RunID: fs.Args()[0], Destination: *destination, IncludeText: *includeText,
	})
	if exportErr != nil {
		if gotSignal == syscall.SIGTERM {
			fmt.Fprintln(stderr, "export: canceled by SIGTERM")
			return 143
		}
		if gotSignal == os.Interrupt {
			fmt.Fprintln(stderr, "export: canceled by signal")
			return 130
		}
		if errors.Is(exportErr, runexport.ErrUsage) {
			fmt.Fprintf(stderr, "error: %v\n", exportErr)
			return 2
		}
		fmt.Fprintf(stderr, "error: %v\n", exportErr)
		return 1
	}
	for _, notice := range result.Notices {
		fmt.Fprintf(stderr, "notice: %s\n", notice)
	}
	fmt.Fprintln(stdout, result.Destination)
	return 0
}
