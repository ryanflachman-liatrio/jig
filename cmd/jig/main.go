package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"jig/internal/tui"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Detect the terminal's background once, here, before tea.NewProgram
	// takes over stdin in raw mode. Querying it later (e.g. from glamour's
	// WithAutoStyle on every resize) races with Bubble Tea's own input
	// reader and leaks the terminal's OSC response into the app as garbled
	// keystrokes. lipgloss.HasDarkBackground() is safe here: Bubble Tea's
	// own package init() already primes this exact cache before main runs.
	dark := lipgloss.HasDarkBackground()

	p := tea.NewProgram(tui.New(ctx, dark), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
		os.Exit(1)
	}
}
