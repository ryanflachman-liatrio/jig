package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"jig/internal/headless"
	"jig/internal/scaffold"
)

var initPlan = scaffold.Plan

func runInit(args []string) int {
	return initMain(args, os.Stdout, os.Stderr)
}

func initMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	templateName := fs.String("template", "minimal", "scaffold template name")
	name := fs.String("name", "", "workflow name (default: target directory name)")
	targetDir := fs.String("dir", ".", "target project directory")
	force := fs.Bool("force", false, "overwrite colliding scaffold files")
	dryRun := fs.Bool("dry-run", false, "preview changes without writing (takes precedence over --force)")
	listTemplates := fs.Bool("list-templates", false, "list available scaffold templates")
	if err := fs.Parse(args); err != nil {
		return headless.ExitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: jig init [--template NAME] [--name NAME] [--dir PATH] [--force] [--dry-run] [--list-templates]")
		return headless.ExitUsage
	}

	// These flags are registered now so the CLI contract is discoverable; their
	// read-only behavior lands with collision safety in the next parent task.
	if *dryRun || *listTemplates {
		fmt.Fprintln(stderr, "error: preview and template listing require collision safety support")
		return headless.ExitUsage
	}
	if flagWasSet(fs, "name") {
		if _, err := scaffold.ValidateName(*name); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return headless.ExitUsage
		}
	}

	plan, err := initPlan(scaffold.Options{
		Dir:      *targetDir,
		Name:     *name,
		Template: *templateName,
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		if errors.Is(err, scaffold.ErrInvalidName) || errors.Is(err, scaffold.ErrUnknownTemplate) {
			return headless.ExitUsage
		}
		return headless.ExitFailed
	}

	result, err := plan.Apply(*force)
	printCreated(stdout, result)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return headless.ExitFailed
	}
	if err := scaffold.Verify(result); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return headless.ExitFailed
	}

	fmt.Fprintf(stdout, "next: jig validate %s\n", result.WorkflowPath)
	fmt.Fprintf(stdout, "next: jig run %s\n", result.WorkflowPath)
	fmt.Fprintln(stdout, "next: jig doctor")
	return headless.ExitOK
}

func printCreated(stdout io.Writer, result *scaffold.Result) {
	if result == nil {
		return
	}
	for _, file := range result.Files {
		fmt.Fprintf(stdout, "created %s\n", file.Path)
	}
}
