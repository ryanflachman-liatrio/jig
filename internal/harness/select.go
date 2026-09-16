package harness

import "fmt"

// For returns the Harness that drives backend. Every backend is ACP-backed;
// there is no other transport to select.
func For(backend string) (Harness, error) {
	switch backend {
	case "claude", "":
		return NewAcpHarness(), nil
	case "cursor":
		return NewCursorHarness(), nil
	case "codex":
		return NewCodexHarness(), nil
	default:
		return nil, fmt.Errorf("unknown backend %q (want %q, %q, or %q)", backend, "claude", "cursor", "codex")
	}
}
