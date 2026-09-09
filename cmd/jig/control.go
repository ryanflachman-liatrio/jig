package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"jig/internal/headless"
	"jig/internal/ops"
)

type controlFlags struct {
	root         *string
	output       *string
	quiet        *bool
	timeout      *time.Duration
	ci           *bool
	approveMerge *bool
	discardMerge *bool
	onRecovery   *string
	onConflict   *string
}

func addControlFlags(fs *flag.FlagSet, recoveryDefault string) controlFlags {
	f := controlFlags{}
	f.root = fs.String("root", ".jig", "persistence root")
	f.output = fs.String("output", "text", "output format: text|json|jsonl")
	fs.StringVar(f.output, "o", "text", "short for --output")
	f.quiet = fs.Bool("quiet", false, "suppress progress lines on stderr")
	fs.BoolVar(f.quiet, "q", false, "short for --quiet")
	f.timeout = fs.Duration("timeout", 0, "wall-clock cancel; 0 = none")
	f.ci = fs.Bool("ci", false, "unattended preset")
	f.approveMerge = fs.Bool("approve-merge", false, "approve the final merge")
	f.discardMerge = fs.Bool("discard-merge", false, "discard the final merge")
	f.onRecovery = fs.String("on-recovery", recoveryDefault, "recovery action: abort|retry|resume|skip")
	f.onConflict = fs.String("on-conflict", headless.ConflictAbort, "integration conflict action: abort only")
	return f
}

var controlFlagsTakingValue = map[string]bool{
	"root": true, "output": true, "o": true, "timeout": true,
	"on-recovery": true, "on-conflict": true, "to": true,
}

func applyControlCI(fs *flag.FlagSet, f controlFlags) {
	if !*f.ci {
		return
	}
	if !flagWasSet(fs, "output") && !flagWasSet(fs, "o") {
		*f.output = "json"
	}
	if !flagWasSet(fs, "approve-merge") && !flagWasSet(fs, "discard-merge") {
		*f.discardMerge = true
	}
	if !flagWasSet(fs, "timeout") {
		*f.timeout = 45 * time.Minute
	}
	if !flagWasSet(fs, "on-conflict") {
		*f.onConflict = headless.ConflictAbort
	}
	fmt.Fprintf(os.Stderr, "ci: %s\n", headless.EffectiveCIFlags(headless.OutputMode(*f.output), *f.discardMerge, *f.onRecovery, *f.onConflict, f.timeout.String()))
}

func validateControlFlags(f controlFlags) (headless.OutputMode, error) {
	mode, err := parseOutputMode(*f.output)
	if err != nil {
		return "", err
	}
	if err := parseControlRecoveryAction(*f.onRecovery); err != nil {
		return "", err
	}
	if err := parseConflictAction(*f.onConflict); err != nil {
		return "", err
	}
	if *f.approveMerge && *f.discardMerge {
		return "", fmt.Errorf("--approve-merge and --discard-merge are mutually exclusive")
	}
	return mode, nil
}

func parseControlRecoveryAction(s string) error {
	switch s {
	case headless.RecoveryAbort, headless.RecoveryRetry, headless.RecoveryResume, headless.RecoverySkip:
		return nil
	default:
		return fmt.Errorf("invalid --on-recovery %q (want abort|retry|resume|skip)", s)
	}
}

func headlessOptions(f controlFlags, mode headless.OutputMode, mgrRoot string) headless.Options {
	return headless.Options{
		Manager: newManager(mgrRoot), Root: mgrRoot, Output: mode, Quiet: *f.quiet,
		Timeout: *f.timeout, ApproveMerge: *f.approveMerge, DiscardMerge: *f.discardMerge,
		OnRecovery: *f.onRecovery, OnConflict: *f.onConflict, CI: *f.ci,
		Stdout: os.Stdout, Stderr: os.Stderr,
	}
}

func runResume(args []string) int {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	f := addControlFlags(fs, "")
	reordered, err := reorderArgs(args, controlFlagsTakingValue, 1)
	if err != nil || fs.Parse(reordered) != nil {
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		return headless.ExitUsage
	}
	if len(fs.Args()) != 1 || !flagWasSet(fs, "on-recovery") || *f.onRecovery == "" {
		fmt.Fprintln(os.Stderr, "usage: jig resume RUN_ID --on-recovery abort|retry|resume|skip [flags]")
		return headless.ExitUsage
	}
	applyControlCI(fs, f)
	mode, err := validateControlFlags(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return headless.ExitUsage
	}
	ctx, signalCode, stop := controlSignalContext()
	defer stop()
	opts := headlessOptions(f, mode, *f.root)
	result := ops.ResumeRun(ctx, opts, fs.Args()[0])
	if result.Err != nil && result.Envelope.RunID == "" {
		fmt.Fprintf(os.Stderr, "error: %v\n", result.Err)
	}
	if code := signalCode.Load(); code != 0 && result.ExitCode == headless.ExitInterrupted {
		return int(code)
	}
	return result.ExitCode
}

func runReset(args []string) int {
	fs := flag.NewFlagSet("reset", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	f := addControlFlags(fs, headless.RecoveryAbort)
	target := fs.String("to", "", "step to reset")
	apply := fs.Bool("apply", false, "apply the reset and continue execution")
	reordered, err := reorderArgs(args, controlFlagsTakingValue, 1)
	if err != nil || fs.Parse(reordered) != nil {
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		return headless.ExitUsage
	}
	if len(fs.Args()) != 1 || *target == "" {
		fmt.Fprintln(os.Stderr, "usage: jig reset RUN_ID --to STEP [--apply --ci] [flags]")
		return headless.ExitUsage
	}
	if *apply && !*f.ci {
		fmt.Fprintln(os.Stderr, "error: applied reset requires both --apply and --ci")
		return headless.ExitUsage
	}
	if !*apply {
		if *f.output != "text" && *f.output != "json" {
			fmt.Fprintln(os.Stderr, "error: reset preview --output must be text|json")
			return headless.ExitUsage
		}
		preview, previewErr := ops.PreviewReset(*f.root, fs.Args()[0], *target)
		if previewErr != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", previewErr)
			return headless.ExitFailed
		}
		if *f.output == "json" {
			_ = json.NewEncoder(os.Stdout).Encode(preview)
		} else {
			ids := make([]string, 0, len(preview.Steps))
			for _, resetStep := range preview.Steps {
				ids = append(ids, fmt.Sprintf("%s (%s)", resetStep.ID, resetStep.Status))
			}
			fmt.Printf("would reset: %s\n", strings.Join(ids, ", "))
			for _, wait := range preview.OutsideParks {
				fmt.Printf("outside closure: %s (%s)\n", wait.StepID, wait.Kind)
			}
			fmt.Println("no changes made; add --apply --ci to continue")
		}
		return headless.ExitOK
	}
	applyControlCI(fs, f)
	mode, err := validateControlFlags(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return headless.ExitUsage
	}
	preview, err := ops.PreviewReset(*f.root, fs.Args()[0], *target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return headless.ExitFailed
	}
	ids := make([]string, 0, len(preview.Steps))
	for _, resetStep := range preview.Steps {
		ids = append(ids, resetStep.ID)
	}
	fmt.Fprintf(os.Stderr, "reset closure: %s\n", strings.Join(ids, ", "))

	ctx, signalCode, stop := controlSignalContext()
	defer stop()
	opts := headlessOptions(f, mode, *f.root)
	_, result := ops.ApplyReset(ctx, opts, fs.Args()[0], *target)
	if result.Err != nil && result.Envelope.RunID == "" {
		fmt.Fprintf(os.Stderr, "error: %v\n", result.Err)
	}
	if code := signalCode.Load(); code != 0 && result.ExitCode == headless.ExitInterrupted {
		return int(code)
	}
	return result.ExitCode
}

func controlSignalContext() (context.Context, *atomic.Int32, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	code := &atomic.Int32{}
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-sigCh:
			if sig == syscall.SIGTERM {
				code.Store(143)
			} else {
				code.Store(130)
			}
			cancel()
		case <-done:
		}
	}()
	return ctx, code, func() {
		close(done)
		signal.Stop(sigCh)
		cancel()
	}
}
