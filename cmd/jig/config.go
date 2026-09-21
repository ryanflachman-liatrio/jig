package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/BurntSushi/toml"
)

// runConfig dispatches `jig config <subcommand>`. Only "show" exists in v1
// (Spec 28 Non-Goals: no `config init`/generation command).
func runConfig(args []string) int {
	if len(args) == 0 || args[0] != "show" {
		fmt.Fprintln(os.Stderr, "usage: jig config show [--root PATH]")
		return 2
	}
	return configShow(args[1:], os.Stdout, os.Stderr)
}

// configShow prints the fully-merged effective configuration as flat TOML,
// so a value can be piped or diffed. No per-value layer annotation (default
// vs. user vs. project vs. flag) in v1 — see spec Design Considerations.
func configShow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("config show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".jig", "persistence root (default: .jig)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := loadEffectiveConfig(*root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	enc := toml.NewEncoder(stdout)
	if err := enc.Encode(cfg); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
