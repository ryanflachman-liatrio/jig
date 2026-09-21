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

	tea "charm.land/bubbletea/v2"

	"jig/internal/config"
	"jig/internal/datastore"
	"jig/internal/tui"
	"jig/internal/tui/shared"
	"jig/internal/workflow"
)

func main() {
	// --ascii is a process-wide glyph-preset selector (omp slice 14). We
	// strip it from os.Args before the subcommand dispatcher so it works
	// as either a global flag (\`jig --ascii\`) or ahead of a subcommand
	// (\`jig --ascii run x.toml\`), and subcommand flag.Parse calls do
	// not see an unknown flag. Every rendered glyph after this point
	// consults the swapped vocabulary (shared.SetPreset), so downstream
	// code — TUI, headless prompts, capture — degrades in one place.
	applyGlobalPresetFlag()
	// --config substitutes the resolved user-level config layer (Spec 28
	// Unit 1). Parsed pre-dispatch, same style and same pass as --ascii, so
	// it works uniformly ahead of or after a subcommand and no subcommand's
	// flag.NewFlagSet has to redeclare it.
	applyGlobalConfigFlag()

	// Subcommands run and exit before the TUI takes over the terminal.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "init":
			os.Exit(runInit(os.Args[2:]))
		case "config":
			os.Exit(runConfig(os.Args[2:]))
		case "notifications":
			os.Exit(runNotifications(os.Args[2:]))
		case "validate":
			os.Exit(runValidate(os.Args[2:]))
		case "prune":
			os.Exit(runPrune(os.Args[2:]))
		case "run":
			os.Exit(runRun(os.Args[2:]))
		case "status":
			os.Exit(runStatus(os.Args[2:]))
		case "logs":
			os.Exit(runLogs(os.Args[2:]))
		case "doctor":
			os.Exit(runDoctor(os.Args[2:]))
		case "resume":
			os.Exit(runResume(os.Args[2:]))
		case "reset":
			os.Exit(runReset(os.Args[2:]))
		case "export":
			os.Exit(runExport(os.Args[2:]))
		case "mcp-serve":
			// Hidden: spawned by internal/helpchat as an AcpHarness MCP
			// server subprocess, never invoked directly by a user.
			os.Exit(runMcpServe(os.Args[2:]))
		case "help", "-h", "--help":
			printHelp()
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
			fmt.Fprintln(os.Stderr, "usage: jig <init|validate|run|status|logs|doctor|resume|reset|prune|export|notifications|config>")
			os.Exit(2)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tel := setupTelemetry(ctx, ".jig")
	defer tel.shutdown(context.Background())

	rt, err := newRuntime(".jig", tel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing jig: %v\n", err)
		os.Exit(1)
	}
	// Ordered shutdown: producers first (cancel context on receipt of a
	// signal via NotifyContext), then dispatcher drain. The 5-second cap is
	// enforced inside Runtime.Close.
	defer rt.Close(context.Background())
	tel.attach(ctx, rt.Manager)

	diagnostics := tui.DiagnosticsRendererFunc(func() string {
		if rt.Diagnostics == nil {
			return ""
		}
		return rt.Diagnostics.Render("")
	})
	// Alt screen and the background canvas are declared on the View in v2 (see
	// rootModel.View), not as program options here.
	p := tea.NewProgram(tui.New(ctx, rt.Manager,
		tui.WithDiagnostics(diagnostics),
		tui.WithStartHook(tel.registerRun),
		tui.WithTelemetryMode(tel.mode()),
	))
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

// applyGlobalPresetFlag scans os.Args for the --ascii / -ascii /
// --ascii=<bool> forms, strips the matched token when present, and
// switches the shared glyph vocabulary to PresetASCII (omp slice 14
// FR-14.3). Absent the flag the process keeps the default Unicode
// preset — SetPreset(PresetUnicode) is not called explicitly so this
// stays a no-op cost path.
//
// The flag is handled here, before flag.NewFlagSet in any subcommand,
// so subcommand parsers do not have to redeclare --ascii and never see
// it as an unknown flag. Recognized forms:
//
//	jig --ascii              (long)
//	jig -ascii               (short-style, matches Go's flag package)
//	jig --ascii=true         (explicit true)
//	jig --ascii=false        (explicit false, treated as absent)
//	jig --ascii <subcommand> (position between binary and subcommand)
//	jig <subcommand> --ascii (any position; the token is stripped
//	                          before the subcommand's flag.Parse runs)
//
// The literal token \`--\` terminates flag processing exactly as
// flag.Parse would: an \`--ascii\` after \`--\` is treated as a
// positional argument and left in place.
func applyGlobalPresetFlag() {
	if len(os.Args) < 2 {
		return
	}
	filtered := os.Args[:1]
	seen := false
	value := true
	stop := false
	for _, arg := range os.Args[1:] {
		if stop {
			filtered = append(filtered, arg)
			continue
		}
		if arg == "--" {
			stop = true
			filtered = append(filtered, arg)
			continue
		}
		switch arg {
		case "--ascii", "-ascii":
			seen = true
			value = true
			continue
		case "--ascii=true", "-ascii=true", "--ascii=1", "-ascii=1":
			seen = true
			value = true
			continue
		case "--ascii=false", "-ascii=false", "--ascii=0", "-ascii=0":
			seen = true
			value = false
			continue
		}
		filtered = append(filtered, arg)
	}
	if seen && value {
		shared.SetPreset(shared.PresetASCII)
	}
	os.Args = filtered
}

// globalConfigFlagPath holds the --config flag's resolved path (empty when
// absent), set once by applyGlobalConfigFlag before subcommand dispatch and
// read by loadEffectiveConfig.
var globalConfigFlagPath string

// applyGlobalConfigFlag scans os.Args for the --config <path> / -config
// <path> / --config=<path> / -config=<path> global flag, strips the
// matched tokens the same pre-dispatch way applyGlobalPresetFlag strips
// --ascii, and records the resolved path in globalConfigFlagPath. When
// absent, the empty value tells loadEffectiveConfig to resolve the default
// user-level path itself.
func applyGlobalConfigFlag() {
	if len(os.Args) < 2 {
		return
	}
	filtered := os.Args[:1]
	stop := false
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if stop {
			filtered = append(filtered, arg)
			continue
		}
		if arg == "--" {
			stop = true
			filtered = append(filtered, arg)
			continue
		}
		switch {
		case arg == "--config" || arg == "-config":
			if i+1 < len(os.Args) {
				globalConfigFlagPath = os.Args[i+1]
				i++
			}
			continue
		case strings.HasPrefix(arg, "--config="):
			globalConfigFlagPath = strings.TrimPrefix(arg, "--config=")
			continue
		case strings.HasPrefix(arg, "-config="):
			globalConfigFlagPath = strings.TrimPrefix(arg, "-config=")
			continue
		}
		filtered = append(filtered, arg)
	}
	os.Args = filtered
}

// loadEffectiveConfig resolves the fully-merged Config for root (the
// caller's own persistence root, including "" for persistence-off),
// substituting globalConfigFlagPath for the user-level layer when --config
// was passed. Every config-consuming entry point (bare jig, run,
// notifications, config show) calls this with the root it already owns
// rather than assuming a fixed path.
func loadEffectiveConfig(root string) (config.Config, error) {
	return config.Load(globalConfigFlagPath, root)
}
