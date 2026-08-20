package harness

import "fmt"

// For returns the Harness that drives (backend, transport). Unknown pairs fail
// fast rather than falling back.
//
//   - "claude" / ""  + "sdk" / "" → ClaudeHarness (direct SDK)
//   - "claude" / ""  + "acp"      → AcpHarness (Claude via Zed ACP adapter)
//   - "cursor"       + "acp" / "" → AcpHarness (Cursor IDE via ACP)
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
		switch transport {
		case "acp", "":
			return NewAcpHarness(), nil
		default:
			return nil, fmt.Errorf("unknown transport %q for backend %q (want %q)", transport, backend, "acp")
		}
	default:
		return nil, fmt.Errorf("unknown backend %q (want %q or %q)", backend, "claude", "cursor")
	}
}
