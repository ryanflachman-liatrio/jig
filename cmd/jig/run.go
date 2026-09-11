package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"jig/internal/headless"
)

// runRun is the non-interactive CI / scripting entry point (Spec 19).
// Fail-closed is inherent — there is no --fail-on-gate flag.
func runRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	root := fs.String("root", ".jig", "persistence root (default: .jig)")
	output := fs.String("output", "text", "output format: text|json|jsonl")
	fs.StringVar(output, "o", "text", "short for --output")
	quiet := fs.Bool("quiet", false, "suppress progress lines on stderr")
	fs.BoolVar(quiet, "q", false, "short for --quiet")
	timeout := fs.Duration("timeout", 0, "wall-clock cancel (e.g. 45m); 0 = none")
	ci := fs.Bool("ci", false, "unattended preset (json + discard-merge + abort policies + 45m timeout)")
	approveMerge := fs.Bool("approve-merge", false, "answer FinalMergeRequest with approve")
	discardMerge := fs.Bool("discard-merge", false, "answer FinalMergeRequest with discard")
	onRecovery := fs.String("on-recovery", headless.RecoveryAbort, "recovery action: abort|retry|skip (default abort)")
	onConflict := fs.String("on-conflict", headless.ConflictAbort, "integration conflict action: abort only (abort→recovery cascade)")

	// flag.Parse stops at the first non-flag; documented UX is
	// `jig run file.toml --ci`, so reorder flags ahead of the positional path.
	reordered, err := reorderRunArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return headless.ExitUsage
	}
	if err := fs.Parse(reordered); err != nil {
		return headless.ExitUsage
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: jig run <workflow.toml> [flags]")
		fmt.Fprintln(os.Stderr, "       jig run examples/headless-smoke.toml --ci")
		return headless.ExitUsage
	}

	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	if *ci {
		if !explicit["output"] && !explicit["o"] {
			*output = "json"
		}
		if !explicit["discard-merge"] && !explicit["approve-merge"] {
			*discardMerge = true
		}
		if !explicit["timeout"] {
			*timeout = 45 * time.Minute
		}
		if !explicit["on-recovery"] {
			*onRecovery = headless.RecoveryAbort
		}
		if !explicit["on-conflict"] {
			*onConflict = headless.ConflictAbort
		}
		fmt.Fprintf(os.Stderr, "ci: %s\n", headless.EffectiveCIFlags(
			headless.OutputMode(*output), *discardMerge, *onRecovery, *onConflict, timeout.String(),
		))
	}

	mode, err := parseOutputMode(*output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return headless.ExitUsage
	}
	if err := parseRecoveryAction(*onRecovery); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return headless.ExitUsage
	}
	if err := parseConflictAction(*onConflict); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return headless.ExitUsage
	}
	if *approveMerge && *discardMerge {
		fmt.Fprintln(os.Stderr, "error: --approve-merge and --discard-merge are mutually exclusive")
		return headless.ExitUsage
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var gotSignal os.Signal
	go func() {
		select {
		case sig := <-sigCh:
			gotSignal = sig
			cancel()
		case <-ctx.Done():
		}
	}()

	rt, err := NewRuntime(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return headless.ExitFailed
	}
	defer rt.Close(context.Background())
	result := headless.Run(ctx, headless.Options{
		WorkflowPath:      rest[0],
		Manager:           rt.Manager,
		Root:              *root,
		Output:            mode,
		Quiet:             *quiet,
		Timeout:           *timeout,
		ApproveMerge:      *approveMerge,
		DiscardMerge:      *discardMerge,
		OnRecovery:        *onRecovery,
		OnConflict:        *onConflict,
		CI:                *ci,
		Stdout:            os.Stdout,
		Stderr:            os.Stderr,
		Notifications:     rt,
	})
	rt.DrainDiagnosticsTo(os.Stderr)

	if result.ExitCode == headless.ExitInterrupted && gotSignal == syscall.SIGTERM {
		return 143
	}
	return result.ExitCode
}

func parseOutputMode(s string) (headless.OutputMode, error) {
	switch headless.OutputMode(s) {
	case headless.OutputText, headless.OutputJSON, headless.OutputJSONL:
		return headless.OutputMode(s), nil
	default:
		return "", fmt.Errorf("invalid --output %q (want text|json|jsonl)", s)
	}
}

func parseRecoveryAction(s string) error {
	switch s {
	case headless.RecoveryAbort, headless.RecoveryRetry, headless.RecoverySkip:
		return nil
	default:
		return fmt.Errorf("invalid --on-recovery %q (want abort|retry|skip)", s)
	}
}

func parseConflictAction(s string) error {
	switch s {
	case headless.ConflictAbort:
		return nil
	default:
		return fmt.Errorf("invalid --on-conflict %q (want abort; agent is not supported in headless)", s)
	}
}

// runFlagsTakingValue names flags that consume the next argv token. Keep in
// sync with runRun's FlagSet.
var runFlagsTakingValue = map[string]bool{
	"root": true, "output": true, "o": true, "timeout": true,
	"on-recovery": true, "on-conflict": true,
}

// reorderRunArgs moves the single positional workflow path to the end so
// flag.Parse sees flags first. Supports `jig run wf.toml --ci` and
// `jig run --ci wf.toml`.
func reorderRunArgs(args []string) ([]string, error) {
	var file string
	var flags []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest := args[i+1:]
			if len(rest) == 0 {
				break
			}
			if file != "" {
				return nil, fmt.Errorf("unexpected argument %q", rest[0])
			}
			file = rest[0]
			if len(rest) > 1 {
				return nil, fmt.Errorf("unexpected argument %q", rest[1])
			}
			break
		}
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name = name[:eq]
			} else if runFlagsTakingValue[name] && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		if file != "" {
			return nil, fmt.Errorf("unexpected argument %q", a)
		}
		file = a
	}
	if file == "" {
		return flags, nil
	}
	return append(flags, file), nil
}
