// Command spike drives a single ACP turn against a live
// `npx -y @zed-industries/claude-code-acp@latest` process and prints the
// captured event stream, used to generate this module's CLI-run-log and
// security proof artifacts.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	acp "jig/harness/acp"
)

func main() {
	allow := flag.Bool("allow", true, "allow (true) or deny (false) the tool-call permission request")
	prompt := flag.String("prompt", "Run `ls` in the current directory and tell me what you see.", "prompt to send for the single turn")
	timeout := flag.Duration("timeout", 90*time.Second, "overall timeout for the round-trip")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "getwd: %v\n", err)
		os.Exit(1)
	}

	decide := func(acpsdk.ToolCallUpdate) bool { return *allow }

	result, err := acp.Run(ctx, cwd, *prompt, decide)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		os.Exit(1)
	}

	if err := acp.WriteLog(os.Stdout, result); err != nil {
		fmt.Fprintf(os.Stderr, "write log: %v\n", err)
		os.Exit(1)
	}
}
