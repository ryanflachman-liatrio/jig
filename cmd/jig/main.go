package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"jig/internal/engine"
	"jig/internal/runner"
	"jig/internal/tui"
	"jig/internal/workflow"
)

func main() {
	// Subcommands run and exit before the TUI takes over the terminal.
	if len(os.Args) > 1 && os.Args[1] == "validate" {
		os.Exit(runValidate(os.Args[2:]))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Detect the terminal's background once, here, before tea.NewProgram
	// takes over stdin in raw mode. Querying it later (e.g. from glamour's
	// WithAutoStyle on every resize) races with Bubble Tea's own input
	// reader and leaks the terminal's OSC response into the app as garbled
	// keystrokes. lipgloss.HasDarkBackground() is safe here: Bubble Tea's
	// own package init() already primes this exact cache before main runs.
	dark := lipgloss.HasDarkBackground()

	// Phase 1: dry-run mode — all steps succeed after a visible delay.
	// Phase 4 wires in AgentExecutor; Phase 2 wires in CommandExecutor.
	exec := runner.NewFakeExecutor(nil, runner.FakeOutcome{})
	mgr := engine.NewManager(exec, ".jig")

	p := tea.NewProgram(tui.New(ctx, dark, mgr), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
		os.Exit(1)
	}
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
