package harness

import (
	"fmt"
	"os"
)

// FromEnv selects a Harness based on the JIG_HARNESS environment variable:
// unset or "claude" (the default) returns ClaudeHarness; "acp" returns
// AcpHarness; any other value fails fast with a clear error rather than
// silently falling back to the default.
func FromEnv() (Harness, error) {
	switch v := os.Getenv("JIG_HARNESS"); v {
	case "", "claude":
		return NewClaudeHarness(), nil
	case "acp":
		return NewAcpHarness(), nil
	default:
		return nil, fmt.Errorf("unknown harness %q (want %q or %q)", v, "claude", "acp")
	}
}
