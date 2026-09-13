package shared

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTruncationMoreItems(t *testing.T) {
	tests := []struct {
		name             string
		n                int
		singular, plural string
		want             string
	}{
		{"zero uses plural", 0, "line", "lines", "… 0 more lines"},
		{"one uses singular", 1, "line", "lines", "… 1 more line"},
		{"two uses plural", 2, "line", "lines", "… 2 more lines"},
		{"large uses plural", 240, "line", "lines", "… 240 more lines"},
		{"file plural", 3, "file", "files", "… 3 more files"},
		{"match singular", 1, "match", "matches", "… 1 more match"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MoreItems(tt.n, tt.singular, tt.plural)
			if got != tt.want {
				t.Fatalf("MoreItems(%d, %q, %q) = %q, want %q", tt.n, tt.singular, tt.plural, got, tt.want)
			}
		})
	}
}

func TestTruncationEarlierItems(t *testing.T) {
	tests := []struct {
		name             string
		n                int
		singular, plural string
		want             string
	}{
		{"zero uses plural", 0, "line", "lines", "… 0 earlier lines"},
		{"one uses singular", 1, "line", "lines", "… 1 earlier line"},
		{"two uses plural", 2, "line", "lines", "… 2 earlier lines"},
		{"large uses plural", 240, "line", "lines", "… 240 earlier lines"},
		{"file plural", 3, "file", "files", "… 3 earlier files"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EarlierItems(tt.n, tt.singular, tt.plural)
			if got != tt.want {
				t.Fatalf("EarlierItems(%d, %q, %q) = %q, want %q", tt.n, tt.singular, tt.plural, got, tt.want)
			}
		})
	}
}

func TestTruncationExpandHint(t *testing.T) {
	tests := []struct {
		name     string
		expanded bool
		hasMore  bool
		keyHelp  string
		want     string
	}{
		{"expanded short-circuits", true, true, "enter", ""},
		{"no more short-circuits", false, false, "enter", ""},
		{"empty key short-circuits", false, true, "", ""},
		{"whitespace key short-circuits", false, true, "  ", ""},
		{"expanded + no more still empty", true, false, "enter", ""},
		{"default enter binding", false, true, "enter", "[enter: Expand]"},
		{"rebind ctrl+o", false, true, "ctrl+o", "[ctrl+o: Expand]"},
		{"multi-char key text", false, true, "shift+enter", "[shift+enter: Expand]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExpandHint(tt.expanded, tt.hasMore, tt.keyHelp)
			if got != tt.want {
				t.Fatalf("ExpandHint(%v, %v, %q) = %q, want %q", tt.expanded, tt.hasMore, tt.keyHelp, got, tt.want)
			}
		})
	}
}

func TestTruncationHintLine(t *testing.T) {
	tests := []struct {
		name       string
		more, hint string
		want       string
	}{
		{"both empty", "", "", ""},
		{"only more", "… 4 more lines", "", "… 4 more lines"},
		{"only hint", "", "[enter: Expand]", "[enter: Expand]"},
		{"both joined by single space", "… 4 more lines", "[enter: Expand]", "… 4 more lines [enter: Expand]"},
		{"earlier + hint joined by single space", "… 3 earlier lines", "[enter: Expand]", "… 3 earlier lines [enter: Expand]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HintLine(tt.more, tt.hint)
			if got != tt.want {
				t.Fatalf("HintLine(%q, %q) = %q, want %q", tt.more, tt.hint, got, tt.want)
			}
			if strings.HasPrefix(got, " ") || strings.HasSuffix(got, " ") {
				t.Fatalf("HintLine result %q has leading or trailing space", got)
			}
			if strings.Contains(got, "  ") {
				t.Fatalf("HintLine result %q contains a double space", got)
			}
		})
	}
}

func TestTruncationCaptureTruncatedHint(t *testing.T) {
	got := CaptureTruncatedHint()
	want := "… capture truncated at write"
	if got != want {
		t.Fatalf("CaptureTruncatedHint() = %q, want %q", got, want)
	}
}

// TestDiffClampedHint locks the slice-07 wording for a diff over
// write-time-clamped content. Callers style the returned string once
// through Theme.Chat.Hint.Render; the helper itself remains
// presentation-agnostic (no lipgloss import at package scope).
func TestDiffClampedHint(t *testing.T) {
	got := DiffClampedHint()
	want := "… content clamped at write; diff may be incomplete"
	if got != want {
		t.Fatalf("DiffClampedHint() = %q, want %q", got, want)
	}
	if strings.HasPrefix(got, " ") || strings.HasSuffix(got, " ") {
		t.Fatalf("DiffClampedHint() has leading or trailing whitespace: %q", got)
	}
}

// TestDiffUnavailableHint locks the slice-07 wording for the
// resulting-source fallback when computation was skipped or failed.
func TestDiffUnavailableHint(t *testing.T) {
	got := DiffUnavailableHint()
	want := "… diff unavailable; showing resulting source"
	if got != want {
		t.Fatalf("DiffUnavailableHint() = %q, want %q", got, want)
	}
	if strings.HasPrefix(got, " ") || strings.HasSuffix(got, " ") {
		t.Fatalf("DiffUnavailableHint() has leading or trailing whitespace: %q", got)
	}
}

// TestTruncationSharedGallery writes a deterministic capture of the
// helper outputs for representative inputs. It runs only when
// JIG_UI_SNAPSHOT_DIR is set, matching the pattern established by
// TestStatusLineGallery so the gallery lives beside its production
// helper without inflating ordinary test runs.
func TestTruncationSharedGallery(t *testing.T) {
	dir := os.Getenv("JIG_UI_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set JIG_UI_SNAPSHOT_DIR to capture the truncation-vocabulary gallery")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	fmt.Fprintln(&b, "# Slice 06 shared truncation-vocabulary gallery")
	fmt.Fprintln(&b, "#")
	fmt.Fprintln(&b, "# Generated by TestTruncationSharedGallery in")
	fmt.Fprintln(&b, "# internal/tui/shared/truncation_test.go. Each row shows the exact")
	fmt.Fprintln(&b, "# helper output; ExpandHint short-circuits render as empty strings.")
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "MoreItems(1,\"line\",\"lines\")")
	fmt.Fprintf(&b, "  -> %q\n", MoreItems(1, "line", "lines"))
	fmt.Fprintln(&b, "MoreItems(2,\"line\",\"lines\")")
	fmt.Fprintf(&b, "  -> %q\n", MoreItems(2, "line", "lines"))
	fmt.Fprintln(&b, "MoreItems(240,\"line\",\"lines\")")
	fmt.Fprintf(&b, "  -> %q\n", MoreItems(240, "line", "lines"))
	fmt.Fprintln(&b, "MoreItems(0,\"file\",\"files\")")
	fmt.Fprintf(&b, "  -> %q\n", MoreItems(0, "file", "files"))
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "EarlierItems(1,\"line\",\"lines\")")
	fmt.Fprintf(&b, "  -> %q\n", EarlierItems(1, "line", "lines"))
	fmt.Fprintln(&b, "EarlierItems(3,\"file\",\"files\")")
	fmt.Fprintf(&b, "  -> %q\n", EarlierItems(3, "file", "files"))
	fmt.Fprintln(&b, "EarlierItems(240,\"line\",\"lines\")")
	fmt.Fprintf(&b, "  -> %q\n", EarlierItems(240, "line", "lines"))
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "ExpandHint(false,true,\"enter\")")
	fmt.Fprintf(&b, "  -> %q\n", ExpandHint(false, true, "enter"))
	fmt.Fprintln(&b, "ExpandHint(true,true,\"enter\")     (expanded -> empty)")
	fmt.Fprintf(&b, "  -> %q\n", ExpandHint(true, true, "enter"))
	fmt.Fprintln(&b, "ExpandHint(false,false,\"enter\")   (nothing hidden -> empty)")
	fmt.Fprintf(&b, "  -> %q\n", ExpandHint(false, false, "enter"))
	fmt.Fprintln(&b, "ExpandHint(false,true,\" \")        (whitespace key -> empty)")
	fmt.Fprintf(&b, "  -> %q\n", ExpandHint(false, true, " "))
	fmt.Fprintln(&b, "ExpandHint(false,true,\"ctrl+o\")    (rebind flows through)")
	fmt.Fprintf(&b, "  -> %q\n", ExpandHint(false, true, "ctrl+o"))
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "HintLine(MoreItems(4,\"line\",\"lines\"), ExpandHint(false,true,\"enter\"))")
	fmt.Fprintf(&b, "  -> %q\n", HintLine(MoreItems(4, "line", "lines"), ExpandHint(false, true, "enter")))
	fmt.Fprintln(&b, "HintLine(EarlierItems(3,\"line\",\"lines\"), ExpandHint(false,true,\"enter\"))")
	fmt.Fprintf(&b, "  -> %q\n", HintLine(EarlierItems(3, "line", "lines"), ExpandHint(false, true, "enter")))
	fmt.Fprintln(&b, "HintLine(MoreItems(4,\"line\",\"lines\"), \"\")           (no hint -> more only)")
	fmt.Fprintf(&b, "  -> %q\n", HintLine(MoreItems(4, "line", "lines"), ""))
	fmt.Fprintln(&b, "HintLine(\"\", \"\")                                     (both empty)")
	fmt.Fprintf(&b, "  -> %q\n", HintLine("", ""))
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "CaptureTruncatedHint()")
	fmt.Fprintf(&b, "  -> %q\n", CaptureTruncatedHint())

	out := filepath.Join(dir, "25-task-1-shared-gallery.txt")
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write gallery: %v", err)
	}
	notes := filepath.Join(dir, "25-task-1-shared-gallery.notes.txt")
	body := "Generated by TestTruncationSharedGallery in internal/tui/shared/truncation_test.go.\n" +
		"Command: JIG_UI_SNAPSHOT_DIR=<dir> go test ./internal/tui/shared -run TestTruncationSharedGallery -count=1\n" +
		"Charm v2 dependencies at go.mod pins; no lipgloss import in the helpers under test.\n"
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
}

// TestTruncationSourceShape locks FR-06.4: the shared truncation helpers
// must not import lipgloss at package scope. Styling belongs to the
// caller (Theme.Chat.Hint.Render or an equivalent hint style), so the
// helper file's source stays presentation-agnostic and portable across
// panels that reach the operator through a different hint style token.
func TestTruncationSourceShape(t *testing.T) {
	src, err := os.ReadFile("truncation.go")
	if err != nil {
		t.Fatalf("read truncation.go: %v", err)
	}
	if strings.Contains(string(src), "charm.land/lipgloss") {
		t.Fatal("truncation.go must not import charm.land/lipgloss; styling belongs to callers")
	}
}
