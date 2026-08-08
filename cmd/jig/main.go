package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/runner"
	"jig/internal/sentinel"
	"jig/internal/tui"
	"jig/internal/workflow"
)

func main() {
	// Subcommands run and exit before the TUI takes over the terminal.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "validate":
			os.Exit(runValidate(os.Args[2:]))
		case "prune":
			os.Exit(runPrune(os.Args[2:]))
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Route by step type. CommandExecutor and AgentExecutor run real work;
	// review steps are handled inline by the scheduler (no executor needed, but
	// the mux falls back to FakeExecutor if somehow dispatched).
	mux := runner.NewMux()
	mux.Register(workflow.StepCommand, runner.NewCommandExecutor(""))
	mux.Register(workflow.StepAgent, runner.NewAgentExecutor())
	mux.Register(workflow.StepReview, runner.NewFakeExecutor(nil, runner.FakeOutcome{}))
	mgr := engine.NewManager(mux, ".jig")

	// Register Tier-2 monitor agents. Look for .md files in the well-known
	// monitors directory beside the examples; skip silently if absent so the
	// binary works outside the repo tree.
	if monitors := discoverMonitors("examples/agents/monitors"); len(monitors) > 0 {
		mgr.SetMonitors(monitors)
	}

	// Alt screen and the background canvas are declared on the View in v2 (see
	// rootModel.View), not as program options here.
	p := tea.NewProgram(tui.New(ctx, mgr))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
		os.Exit(1)
	}
}

// discoverMonitors returns MonitorDef entries for every .md file found in dir.
// Each file's base name (without extension) becomes the monitor name. Files that
// fail to stat are silently skipped so the binary remains usable outside the repo.
func discoverMonitors(dir string) []sentinel.MonitorDef {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	adapter := runner.NewMonitorAdapter()
	var defs []sentinel.MonitorDef
	for _, e := range entries {
		if e.IsDir() || len(e.Name()) < 4 || e.Name()[len(e.Name())-3:] != ".md" {
			continue
		}
		name := e.Name()[:len(e.Name())-3] // strip .md
		defs = append(defs, sentinel.MonitorDef{
			File:       dir + "/" + e.Name(),
			Monitor:    name,
			Dispatcher: adapter,
		})
	}
	return defs
}

// runValidate parses and validates a workflow file, printing a summary. It
// returns a process exit code so main can Exit before the TUI initializes.
func runValidate(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: jig validate <workflow.toml>")
		return 2
	}
	wf, err := workflow.Load(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("ok: %q v%s — %d step(s)\n", wf.Meta.Name, wf.Meta.Version, len(wf.Steps))
	return 0
}

// runPrune is the housekeeping path for transcript/run retention (Phase 7). It
// removes finished run directories under .jig/ according to --keep-last and/or
// --max-age, never touching a run that has not reached a terminal RunFinished
// event. With neither flag set it prunes nothing and prints usage — retention
// is opt-in and conservative by design.
func runPrune(args []string) int {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	keepLast := fs.Int("keep-last", 0, "keep the N most-recently-finished runs; 0 disables the count rule")
	maxAge := fs.Duration("max-age", 0, "remove finished runs older than this (e.g. 168h); 0 disables the age rule")
	dryRun := fs.Bool("dry-run", false, "report what would be pruned without deleting")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *keepLast <= 0 && *maxAge <= 0 {
		fmt.Fprintln(os.Stderr, "usage: jig prune [--keep-last N] [--max-age DURATION] [--dry-run]")
		fmt.Fprintln(os.Stderr, "at least one of --keep-last or --max-age is required")
		return 2
	}

	policy := datastore.RetentionPolicy{MaxAge: *maxAge, KeepLast: *keepLast}
	if *dryRun {
		ids, err := datastore.Prunable(".jig", policy, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if len(ids) == 0 {
			fmt.Println("dry-run: no runs would be removed")
			return 0
		}
		for _, id := range ids {
			fmt.Printf("would prune %s\n", id)
		}
		fmt.Printf("dry-run: %d run(s) would be removed\n", len(ids))
		return 0
	}

	pruned, err := datastore.Prune(".jig", policy, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(pruned) == 0 {
		fmt.Println("prune: no runs removed")
		return 0
	}
	for _, id := range pruned {
		fmt.Printf("pruned %s\n", id)
	}
	fmt.Printf("prune: removed %d run(s)\n", len(pruned))
	return 0
}
