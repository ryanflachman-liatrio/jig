package harness

import "fmt"

// For returns the Harness that drives (backend, transport). Claude supports
// "sdk" and "acp" transports; Cursor always uses ACP.
func For(backend, transport string) (Harness, error) {
	switch backend {
	case "claude", "":
		switch transport {
		case "sdk", "":
			return NewClaudeHarness(), nil
		case "acp":
			return NewAcpHarness(), nil
		default:
			return nil, fmt.Errorf("unknown transport %q for backend %q (want %q or %q)", transport, backend, "sdk", "acp")
		}
	case "cursor":
		return NewCursorHarness(), nil
	default:
		return nil, fmt.Errorf("unknown backend %q (want %q or %q)", backend, "claude", "cursor")
	}
}
