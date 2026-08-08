package sentinel

import (
	"strings"
	"testing"
	"time"
)

// TestRedactionFingerprint verifies Redact never leaks the raw value and that
// NewFingerprint deduplicates correctly.
func TestRedactionFingerprint(t *testing.T) {
	const rawKey = "AKIAIOSFODNN7EXAMPLE"
	redacted := Redact("aws-key", rawKey)

	if strings.Contains(redacted, rawKey) {
		t.Errorf("Redact leaked raw key: %q", redacted)
	}
	if !strings.Contains(redacted, "aws-key") {
		t.Errorf("Redact missing pattern name: %q", redacted)
	}
	// Should show last 4 chars of the raw key.
	if !strings.Contains(redacted, "MPLE") {
		t.Errorf("Redact missing last-4 suffix: %q", redacted)
	}

	// Same inputs → same fingerprint (dedup).
	fp1 := NewFingerprint("step-a", "secret-leak", "tool:Write:arg0")
	fp2 := NewFingerprint("step-a", "secret-leak", "tool:Write:arg0")
	if fp1 != fp2 {
		t.Errorf("identical inputs produced different fingerprints: %q vs %q", fp1, fp2)
	}

	// Different rule → different fingerprint.
	fp3 := NewFingerprint("step-a", "other-rule", "tool:Write:arg0")
	if fp1 == fp3 {
		t.Errorf("different rule produced same fingerprint: %q", fp1)
	}

	// Different step → different fingerprint.
	fp4 := NewFingerprint("step-b", "secret-leak", "tool:Write:arg0")
	if fp1 == fp4 {
		t.Errorf("different step produced same fingerprint: %q", fp1)
	}

	// Different evidence key → different fingerprint.
	fp5 := NewFingerprint("step-a", "secret-leak", "tool:Write:arg1")
	if fp1 == fp5 {
		t.Errorf("different evidence key produced same fingerprint: %q", fp1)
	}

	// Fingerprint is short (16 hex chars) and non-empty.
	if len(fp1) != 16 {
		t.Errorf("fingerprint length = %d, want 16: %q", len(fp1), fp1)
	}

	// Verify a Finding constructed from a match holds only the redacted form.
	f := Finding{
		Ts:          time.Now().UTC(),
		RunID:       "run1",
		StepID:      "step-a",
		Tier:        TierGuard,
		Monitor:     "secret-leak",
		Severity:    SeverityHigh,
		Action:      ActionBlocked,
		Detail:      Redact("aws-key", rawKey),
		Evidence:    "tool:Write:arg0",
		Fingerprint: fp1,
	}
	if strings.Contains(f.Detail, rawKey) {
		t.Errorf("Finding.Detail contains raw key: %q", f.Detail)
	}
	if !strings.Contains(f.Detail, "aws-key") {
		t.Errorf("Finding.Detail missing pattern name: %q", f.Detail)
	}
}
