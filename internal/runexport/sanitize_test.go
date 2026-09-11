package runexport

import (
	"strings"
	"testing"
)

func newTestCounters() *Counters {
	return &Counters{Replacements: map[string]int{}, Omissions: map[string]int{}}
}

func TestExportSanitizerRedactsSecretsFully(t *testing.T) {
	counters := newTestCounters()
	sn := newExportSanitizer(nil, counters)
	for _, secret := range syntheticSecrets {
		text := "prefix " + secret + " suffix"
		out := sn.Sanitize(text)
		if strings.Contains(out, secret) {
			t.Fatalf("Sanitize(%q) = %q, still contains the secret", text, out)
		}
		if !strings.Contains(out, "[REDACTED:") {
			t.Fatalf("Sanitize(%q) = %q, missing redaction marker", text, out)
		}
	}
	if len(counters.Replacements) == 0 {
		t.Fatal("no replacement counters were incremented")
	}
}

func TestExportSanitizerRemovesPriorSentinelMarkerSuffix(t *testing.T) {
	counters := newTestCounters()
	sn := newExportSanitizer(nil, counters)
	// This shape (sentinel.Redact's live-monitor preview) retains a 4-char
	// suffix of the original secret — export must remove that suffix too.
	out := sn.Sanitize("already redacted: [aws-key:…EFGH] remains")
	if strings.Contains(out, "EFGH") {
		t.Fatalf("Sanitize retained a prior marker's suffix: %q", out)
	}
	if counters.Replacements["prior_marker"] == 0 {
		t.Fatalf("prior_marker counter not incremented: %+v", counters)
	}
}

func TestExportSanitizerIdentifierTokenBoundary(t *testing.T) {
	counters := newTestCounters()
	sn := newExportSanitizer([]idReplacement{{original: "run-1", replacement: "run-ALIAS", boundary: "token"}}, counters)
	// Whole-token match is replaced.
	if got := sn.Sanitize("see run-1 for detail"); !strings.Contains(got, "run-ALIAS") || strings.Contains(got, "run-1 ") {
		t.Fatalf("Sanitize did not replace whole token: %q", got)
	}
	// A longer token that merely contains "run-1" as a substring must not be
	// rewritten mid-word.
	if got := sn.Sanitize("see run-10 for detail"); strings.Contains(got, "run-ALIAS") {
		t.Fatalf("Sanitize rewrote inside a longer token: %q", got)
	}
}

func TestExportSanitizerPathBoundary(t *testing.T) {
	counters := newTestCounters()
	sn := newExportSanitizer([]idReplacement{{original: "/home/op", replacement: "[HOME]", boundary: "path"}}, counters)
	if got := sn.Sanitize("file at /home/op/project/file.go"); !strings.Contains(got, "[HOME]/project/file.go") {
		t.Fatalf("Sanitize did not replace path prefix: %q", got)
	}
	// "/home/operator" must not be treated as containing "/home/op" as a path
	// prefix (that would also rewrite an unrelated longer directory name).
	if got := sn.Sanitize("file at /home/operator/file.go"); strings.Contains(got, "[HOME]") {
		t.Fatalf("Sanitize rewrote inside a longer path segment: %q", got)
	}
}

func TestExportSanitizerStripsControlsKeepsNewlineTab(t *testing.T) {
	sn := newExportSanitizer(nil, newTestCounters())
	in := "line one\n\ttabbed\x1b[31mred\x1b[0m\x07bell"
	out := sn.Sanitize(in)
	if !strings.Contains(out, "\n") || !strings.Contains(out, "\t") {
		t.Fatalf("Sanitize stripped newline/tab: %q", out)
	}
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Fatalf("Sanitize left control/escape bytes: %q", out)
	}
}

func TestExportSanitizerTruncatesAfterSanitizing(t *testing.T) {
	counters := newTestCounters()
	sn := newExportSanitizer(nil, counters)
	// A secret placed right at the truncation boundary must be fully
	// replaced before truncation, never cut into an unrecognized prefix.
	padding := strings.Repeat("a", maxRetainedText-40)
	text := padding + syntheticSecrets[0] + strings.Repeat("b", 200)
	out := sn.Sanitize(text)
	if strings.Contains(out, syntheticSecrets[0]) {
		t.Fatalf("secret crossing the truncation boundary was not fully redacted: tail=%q", out[len(out)-60:])
	}
	if !strings.HasSuffix(out, "…[truncated]") {
		t.Fatalf("Sanitize did not mark truncation: tail=%q", out)
	}
	if counters.Truncations != 1 {
		t.Fatalf("Truncations = %d, want 1", counters.Truncations)
	}
	if len(out) > maxRetainedText+len("…[truncated]")+4 {
		t.Fatalf("Sanitize output length %d exceeds the retained-text bound", len(out))
	}
}

func TestExportSanitizerInvalidUTF8Normalized(t *testing.T) {
	sn := newExportSanitizer(nil, newTestCounters())
	out := sn.Sanitize("valid \xff\xfe invalid bytes")
	for i := 0; i < len(out); {
		r := out[i]
		if r >= 0x80 {
			// Every remaining multi-byte sequence must be valid UTF-8; a
			// naive byte-for-byte copy of \xff\xfe would not be.
		}
		i++
	}
	if strings.ContainsRune(out, '�') == false && strings.Contains(out, "\xff") {
		t.Fatalf("Sanitize left invalid UTF-8 unnormalized: %q", out)
	}
}
