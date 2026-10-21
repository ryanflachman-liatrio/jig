package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ryanflachman-liatrio/jig/internal/skills"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

const skillsUsage = "usage: jig skills migrate [--apply] [--root PATH] [--keep NAME,...] [EXTRA_PATH...]"

// runSkills dispatches `jig skills` subcommands.
func runSkills(args []string) int {
	if len(args) == 0 || args[0] != "migrate" {
		fmt.Fprintln(os.Stderr, skillsUsage)
		return 2
	}
	return runSkillsMigrate(args[1:], os.Stdout, os.Stderr)
}

// runSkillsMigrate moves skills referenced by jig workflows out of the shared
// .agents/skills tree into .agents/jig/skills and rewrites the references.
// It previews by default; --apply performs the change.
func runSkillsMigrate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("skills migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apply := fs.Bool("apply", false, "move skills and rewrite references (default: preview only)")
	root := fs.String("root", ".", "project root containing .agents/")
	keep := fs.String("keep", "", "comma-separated skill names to leave in .agents/skills")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var keepNames []string
	for _, name := range strings.Split(*keep, ",") {
		if name = strings.TrimSpace(name); name != "" {
			keepNames = append(keepNames, name)
		}
	}

	plan, err := skills.PlanMigration(skills.Options{Root: *root, Extra: fs.Args(), Keep: keepNames})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	relTo := func(p string) string {
		if r, err := filepath.Rel(plan.Root, p); err == nil {
			return r
		}
		return p
	}
	verb := "would move"
	if *apply {
		verb = "moved"
	}
	for _, mv := range plan.Moves {
		fmt.Fprintf(stdout, "%s %s -> %s\n", verb, relTo(mv.From), relTo(mv.To))
	}
	for _, rw := range plan.Rewrites {
		fmt.Fprintf(stdout, "  %s:%d  %q -> %q\n", relTo(rw.File), rw.Line, rw.Old, rw.New)
	}
	for _, name := range plan.Kept {
		fmt.Fprintf(stdout, "kept %s in %s\n", name, skills.SharedDir)
	}
	if len(plan.Moves) == 0 {
		fmt.Fprintln(stdout, "skills migrate: no shared skills referenced by jig workflows")
		return 0
	}
	if !*apply {
		fmt.Fprintf(stdout, "dry-run: %d skill(s), %d reference(s); rerun with --apply\n", len(plan.Moves), len(plan.Rewrites))
		return 0
	}
	if err := plan.Apply(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "skills migrate: moved %d skill(s), rewrote %d reference(s)\n", len(plan.Moves), len(plan.Rewrites))
	return 0
}

// sharedSkillWarnings lists agent steps whose skill lives in .agents/skills,
// where every coding agent discovers it, rather than jig's private tree.
func sharedSkillWarnings(workflowPath string, wf *workflow.Workflow) []string {
	base, err := filepath.Abs(filepath.Dir(workflowPath))
	if err != nil {
		return nil
	}
	var warnings []string
	for _, step := range wf.Steps {
		if step.Skill == "" {
			continue
		}
		dir := step.Skill
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(base, dir)
		}
		if skills.IsShared(dir) {
			warnings = append(warnings, fmt.Sprintf("warning: step %q skill %q is in %s and visible to every agent; run `jig skills migrate`", step.ID, step.Skill, skills.SharedDir))
		}
	}
	return warnings
}
