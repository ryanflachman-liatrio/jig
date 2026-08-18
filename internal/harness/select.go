package harness

import "fmt"

// For returns the Harness that drives (backend, transport). Today only Claude
// is implemented: "sdk" → ClaudeHarness, "acp" → AcpHarness (ACP→Claude via
// Zed's adapter). Unknown pairs fail fast rather than falling back.
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
	default:
		return nil, fmt.Errorf("unknown backend %q (want %q)", backend, "claude")
	}
}
