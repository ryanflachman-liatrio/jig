package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"text/tabwriter"
	"time"

	"jig/internal/ops"
)

func runStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	root := fs.String("root", ".jig", "persistence root")
	output := fs.String("output", "text", "output format: text|json")
	fs.StringVar(output, "o", "text", "short for --output")
	reordered, err := reorderArgs(args, map[string]bool{"root": true, "output": true, "o": true}, 1)
	if err != nil || fs.Parse(reordered) != nil {
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		return 2
	}
	if *output != "text" && *output != "json" {
		fmt.Fprintf(os.Stderr, "error: invalid --output %q (want text|json)\n", *output)
		return 2
	}
	if len(fs.Args()) > 1 {
		fmt.Fprintln(os.Stderr, "usage: jig status [RUN_ID] [--root PATH] [--output text|json]")
		return 2
	}
	if len(fs.Args()) == 1 {
		report, inspectErr := ops.InspectRun(*root, fs.Args()[0])
		if *output == "json" {
			_ = json.NewEncoder(os.Stdout).Encode(report)
		} else {
			renderStatusDetail(report)
		}
		if inspectErr != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", inspectErr)
			return 1
		}
		return 0
	}
	reports, listErr := ops.ListRuns(*root)
	if *output == "json" {
		_ = json.NewEncoder(os.Stdout).Encode(reports)
	} else {
		renderStatusList(reports)
	}
	if listErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", listErr)
		return 1
	}
	return 0
}

func renderStatusList(reports []ops.RunReport) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "RUN ID\tWORKFLOW\tSTATE\tSTEPS\tUPDATED\tCOST\tTOKENS")
	for _, report := range reports {
		completed := 0
		for _, step := range report.Steps {
			switch step.Status {
			case "succeeded", "failed", "skipped":
				completed++
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d/%d\t%s\t%.4f\t%d\n", report.RunID, report.Workflow, report.State, completed, len(report.Steps), formatTime(report.UpdatedAt), report.TotalCostUSD, report.TotalTokens)
	}
	_ = w.Flush()
}

func renderStatusDetail(report ops.RunReport) {
	fmt.Printf("run_id: %s\nworkflow: %s\nstate: %s\nlock_held: %t\nreopenable: %t\nstarted_at: %s\nupdated_at: %s\nfinished_at: %s\ncost_usd: %.4f\ntokens: %d\n",
		report.RunID, report.Workflow, report.State, report.LockHeld, report.Reopenable, formatTime(report.StartedAt), formatTime(report.UpdatedAt), formatTime(report.FinishedAt), report.TotalCostUSD, report.TotalTokens)
	for _, wait := range report.WaitingOn {
		fmt.Printf("waiting_on: %s %s\n", wait.StepID, wait.Kind)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "STEP\tSTATUS\tATTEMPT\tITER\tGEN\tCOST\tTOKENS\tERROR")
	for _, step := range report.Steps {
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t%.4f\t%d\t%s\n", step.ID, step.Status, step.Attempt, step.Iteration, step.Generation, step.CostUSD, step.Tokens, step.Error)
	}
	_ = w.Flush()
}

func formatTime(ts *time.Time) string {
	if ts == nil {
		return "-"
	}
	return ts.UTC().Format(time.RFC3339)
}

func runLogs(args []string) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	root := fs.String("root", ".jig", "persistence root")
	stepID := fs.String("step", "", "restrict output to one step")
	tail := fs.Int("tail", 0, "newest N entries (default 200)")
	all := fs.Bool("all", false, "read the complete transcript")
	follow := fs.Bool("follow", false, "follow appended transcript entries")
	fs.BoolVar(follow, "f", false, "short for --follow")
	includeThinking := fs.Bool("include-thinking", false, "include persisted thinking blocks")
	output := fs.String("output", "text", "output format: text|jsonl")
	fs.StringVar(output, "o", "text", "short for --output")
	takes := map[string]bool{"root": true, "step": true, "tail": true, "output": true, "o": true}
	reordered, err := reorderArgs(args, takes, 1)
	if err != nil || fs.Parse(reordered) != nil {
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		return 2
	}
	if len(fs.Args()) != 1 {
		fmt.Fprintln(os.Stderr, "usage: jig logs RUN_ID [--step ID] [--tail N|--all] [--follow] [--output text|jsonl]")
		return 2
	}
	if *all && flagWasSet(fs, "tail") {
		fmt.Fprintln(os.Stderr, "error: --all and --tail are mutually exclusive")
		return 2
	}
	if *tail < 0 || (*output != "text" && *output != "jsonl") {
		fmt.Fprintln(os.Stderr, "error: --tail must be non-negative and --output must be text|jsonl")
		return 2
	}
	opts := ops.LogOptions{StepID: *stepID, Tail: *tail, All: *all, IncludeThinking: *includeThinking}
	emit := func(item ops.LogEntry) error {
		if *output == "jsonl" {
			return json.NewEncoder(os.Stdout).Encode(item)
		}
		_, err := fmt.Fprint(os.Stdout, ops.RenderLogText(item, *includeThinking))
		return err
	}
	if !*follow {
		batch, readErr := ops.ReadLogs(*root, fs.Args()[0], opts)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", readErr)
			return 1
		}
		for _, item := range batch.Entries {
			if err := emit(item); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
		}
		if batch.Omitted > 0 {
			fmt.Fprintf(os.Stderr, "notice: %d older entries omitted\n", batch.Omitted)
		}
		return 0
	}
	if report, inspectErr := ops.InspectRun(*root, fs.Args()[0]); inspectErr == nil && report.Reopenable {
		fmt.Fprintln(os.Stderr, "notice: following an inactive unfinished run; waiting for new records or a signal")
	}
	opts.OnOmitted = func(count int) { fmt.Fprintf(os.Stderr, "notice: %d older entries omitted\n", count) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	var signalCode atomic.Int32
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case sig := <-sigCh:
			if sig == syscall.SIGTERM {
				signalCode.Store(143)
			} else {
				signalCode.Store(130)
			}
			cancel()
		case <-done:
		}
	}()
	_, followErr := ops.FollowLogs(ctx, *root, fs.Args()[0], opts, 300*time.Millisecond, emit)
	if code := signalCode.Load(); code != 0 {
		return int(code)
	}
	if followErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", followErr)
		return 1
	}
	return 0
}

func runDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	root := fs.String("root", ".jig", "persistence root")
	ci := fs.Bool("ci", false, "enforce unattended execution safety")
	output := fs.String("output", "text", "output format: text|json")
	fs.StringVar(output, "o", "text", "short for --output")
	reordered, err := reorderArgs(args, map[string]bool{"root": true, "output": true, "o": true}, 1)
	if err != nil || fs.Parse(reordered) != nil {
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		return 2
	}
	if len(fs.Args()) > 1 || (*ci && len(fs.Args()) != 1) || (*output != "text" && *output != "json") {
		fmt.Fprintln(os.Stderr, "usage: jig doctor [WORKFLOW.toml] [--ci] [--root PATH] [--output text|json]")
		return 2
	}
	path := ""
	if len(fs.Args()) == 1 {
		path = fs.Args()[0]
	}
	report := ops.Doctor(ops.DoctorOptions{Root: *root, WorkflowPath: path, CI: *ci})
	if *output == "json" {
		_ = json.NewEncoder(os.Stdout).Encode(report)
	} else {
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "STATUS\tCHECK\tSCOPE\tMESSAGE\tREMEDIATION")
		for _, check := range report.Checks {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", check.Status, check.ID, check.Scope, check.Message, check.Remediation)
		}
		_ = w.Flush()
	}
	if !report.OK {
		return 1
	}
	return 0
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

func reorderArgs(args []string, takesValue map[string]bool, maxPositionals int) ([]string, error) {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name = name[:eq]
			} else if takesValue[name] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	if len(positional) > maxPositionals {
		return nil, fmt.Errorf("unexpected argument %q", positional[maxPositionals])
	}
	return append(flags, positional...), nil
}

func printHelp() {
	printHelpTo(os.Stdout)
}

func printHelpTo(w io.Writer) {
	fmt.Fprintln(w, `usage: jig <command> [arguments]

Commands:
  init                      scaffold a valid workflow
  validate WORKFLOW.toml    validate a workflow
  run WORKFLOW.toml         run a workflow headlessly
  status [RUN_ID]           list or inspect persisted runs
  logs RUN_ID               read or follow step transcripts
  doctor [WORKFLOW.toml]    check local and CI readiness
  resume RUN_ID             reopen an unfinished run
  reset RUN_ID --to STEP    preview or apply reset-to-step
  prune                     remove old finished runs

Run jig with no arguments to open the TUI.`)
}
