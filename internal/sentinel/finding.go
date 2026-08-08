// Package sentinel defines the security layer's data model, persistence,
// and deterministic policy engine. It imports nothing from engine, runner, or
// tui — only stdlib and jig/internal/step (for Status), mirroring internal/transcript.
package sentinel

import (
	"crypto/sha256"
	"fmt"
	"time"
)

// Tier identifies which security layer produced a finding.
type Tier string

const (
	TierGuard   Tier = "guard"   // Tier-1 deterministic firewall
	TierMonitor Tier = "monitor" // Tier-2 Haiku fleet
)

// Severity is the finding's urgency level.
type Severity string

const (
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// Action records what the security layer did when it produced this finding.
type Action string

const (
	// ActionObserved means the layer noted the event but let it proceed.
	ActionObserved Action = "observed"
	// ActionBlocked means the layer denied a tool call inline; the agent received
	// the denial reason and may adapt (deny + continue).
	ActionBlocked Action = "blocked"
	// ActionEscalated means the layer denied the call and also parked the step
	// for a human recovery decision (deny + park).
	ActionEscalated Action = "escalated"
)

// Finding is one security event recorded by either tier. It never carries a raw
// secret: the Redact helper enforces this at construction.
type Finding struct {
	Ts        time.Time `json:"ts"`
	RunID     string    `json:"run_id"`
	StepID    string    `json:"step_id"`
	Iteration int       `json:"iteration"`
	Tier      Tier      `json:"tier"`
	// Monitor is the rule or monitor name that produced this finding
	// (e.g. "secret-leak", "prompt-injection").
	Monitor  string   `json:"monitor"`
	Severity Severity `json:"severity"`
	Action   Action   `json:"action"`
	// Detail is a human-readable description, safe for commit (no raw secrets).
	Detail string `json:"detail"`
	// Evidence is an optional locator (transcript seq / block index / tool name)
	// that points a reviewer to the raw context on disk.
	Evidence string `json:"evidence,omitempty"`
	// Fingerprint deduplicates identical repeats within a step (same rule, same
	// normalized evidence key). Different rules, different steps, or different
	// offending calls produce distinct fingerprints.
	Fingerprint string `json:"fingerprint"`
}

// Redact returns a safe, loggable representation of a secret match.
// It never returns the raw secret value — only the pattern name and a
// truncated suffix so a reviewer can correlate without reconstructing the value.
// This is the single construction site shared by the finding builder and the
// transcript-redaction filter (Unit 2), so detection and redaction stay in sync.
func Redact(patternName, rawMatch string) string {
	suffix := rawMatch
	if len(suffix) > 4 {
		suffix = suffix[len(suffix)-4:]
	}
	return fmt.Sprintf("[%s:…%s]", patternName, suffix)
}

// NewFingerprint builds a stable deduplication key for a finding.
// The composition is hash{stepID + monitor + evidenceKey} so:
//   - Identical repeats within a step collapse to one finding.
//   - A different rule, a different step, or a different offending call is a
//     fresh finding.
func NewFingerprint(stepID, monitor, evidenceKey string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s", stepID, monitor, evidenceKey)
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}
