package sentinel

// Decision is the guard's verdict on one tool call.
type Decision struct {
	Allow   bool
	Action  Action // ActionBlocked or ActionEscalated when !Allow
	Monitor string // rule name that triggered the decision (e.g. "secret-in-write")
	Reason  string // human-readable denial reason fed back to the agent
}

// Guard applies the Tier-1 rule set to every tool call before the SDK executes
// it. It is stateless and safe for concurrent callers; all rule logic lives in
// rules.go. A nil Guard means the firewall is inactive.
type Guard struct {
	allowlist []string // outbound host allowlist; empty = deny all external calls
}

// NewGuard returns a Guard configured with the given outbound host allowlist.
// Pass nil or an empty slice to deny all outbound tool calls detected by the
// non-allowlisted-host rule.
func NewGuard(allowlist []string) *Guard {
	return &Guard{allowlist: allowlist}
}

// Check applies all rules in priority order and returns the first non-allow
// decision. When every rule passes it returns Allow=true.
// It is safe to call concurrently from multiple goroutines.
func (g *Guard) Check(toolName string, input map[string]any) Decision {
	if d := checkSecretInWrite(toolName, input); !d.Allow {
		return d
	}
	// denied-shell (escalate) is checked before outbound-host (block) because
	// pipe-to-shell patterns (remote code execution) are more severe than an
	// outbound-host violation and should always escalate for human review.
	if d := checkDeniedShell(toolName, input); !d.Allow {
		return d
	}
	if d := g.checkOutboundHost(toolName, input); !d.Allow {
		return d
	}
	return Decision{Allow: true}
}
