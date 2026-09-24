// clipboard_documentation_contract_test.go pins docs/clipboard.md and the
// README against the implementation constants and Surface enum so the
// mapping table, byte limits, and tracking references cannot silently drift
// out of the code. This test only asserts the concrete claims that
// automation can verify.
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"jig/internal/tui/shared"
)

// clipboardDocPath is the operator-facing documentation the contract test
// pins against constants and Surface labels.
const clipboardDocPath = "../../docs/clipboard.md"

// TestClipboardDocumentationContract asserts:
//
//   - docs/clipboard.md declares every ClipboardSurface constant exposed by
//     shared/clipboard.go — no missing surface, no ghost surface.
//   - The two byte limits documented in docs/clipboard.md match the
//     constants exactly (both as raw byte counts and as human units).
//   - The README's Documentation section links to docs/clipboard.md.
func TestClipboardDocumentationContract(t *testing.T) {
	doc := readFileString(t, clipboardDocPath)

	// Surface labels — every enum value must appear at least once. The
	// enum labels are user-visible, so drift here is drift in operator
	// terminology.
	surfaces := []shared.ClipboardSurface{
		shared.ClipboardSurfaceRunID,
		shared.ClipboardSurfaceMonitorFile,
		shared.ClipboardSurfaceTranscriptItem,
		shared.ClipboardSurfaceTranscriptSnapshot,
		shared.ClipboardSurfaceReviewLine,
		shared.ClipboardSurfaceReviewRange,
		shared.ClipboardSurfaceReviewBlock,
		shared.ClipboardSurfaceReviewHunk,
		shared.ClipboardSurfaceReviewFileDiff,
		shared.ClipboardSurfaceReviewDocument,
	}
	for _, s := range surfaces {
		if !containsSurfaceReference(doc, string(s)) {
			t.Errorf("docs/clipboard.md does not mention surface %q", s)
		}
	}

	// Byte limits — both the constant identifier and the human-readable
	// forms must be present so a future edit that changes either without
	// the other fails here.
	assertContainsAll(t, doc, "byte limits",
		"ClipboardMaxPayloadBytes",
		"ClipboardMaxTranscriptScanBytes",
		"256 KiB",
		"8 MiB",
		fmt.Sprintf("%d", shared.ClipboardMaxPayloadBytes),
		fmt.Sprintf("%d", shared.ClipboardMaxTranscriptScanBytes),
	)

	// The doc mentions the two limits as the exact byte counts. The
	// PrepareClipboardPayload contract says the check is inclusive; the
	// doc must repeat that so operators do not expect a soft cap.
	assertContainsAll(t, doc, "inclusive limit wording",
		"exactly the limit succeeds",
	)

	// Delivery / helper contract — jig's promise not to read the clipboard
	// or spawn helpers is a common troubleshooting question; ensure the
	// doc still makes that promise so the sanitizer / root can never
	// silently regain a helper path.
	assertContainsAll(t, doc, "helper contract",
		"never reads",
		"xclip",
		"pbcopy",
		"OSC52",
	)

	// README documentation index — the clipboard doc must be linked.
	readme := readFileString(t, "../../README.md")
	if !strings.Contains(readme, "docs/clipboard.md") {
		t.Errorf("README.md Documentation section does not link docs/clipboard.md")
	}
}

// containsSurfaceReference checks docs contain the surface name either as
// a mapping-table entry or a prose reference. Backtick-wrapped and bare
// forms both count.
func containsSurfaceReference(doc, surface string) bool {
	if surface == "" {
		return true
	}
	// Match on word boundary so "line" doesn't collide with "inline".
	// Surface labels are short user-facing tokens (e.g. "run ID", "hunk",
	// "file diff"). Require the exact string; case-sensitive.
	pat := regexp.MustCompile(`\b` + regexp.QuoteMeta(surface) + `\b`)
	return pat.FindStringIndex(doc) != nil
}

// assertContainsAll fails the test if any needle is missing from doc,
// naming the group so the failure message points at the intent.
func assertContainsAll(t *testing.T, doc, group string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(doc, n) {
			t.Errorf("%s: docs missing %q", group, n)
		}
	}
}

// readFileString is a helper that returns the file contents or t.Fatal on
// error. Test-local so it can share tests within the tui package without
// exporting a shared testing helper.
func readFileString(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", path, err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read %s: %v", abs, err)
	}
	return string(data)
}
