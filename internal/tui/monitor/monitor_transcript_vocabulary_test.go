package monitor

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	keybind "charm.land/bubbles/v2/key"

	"jig/internal/toolcall"
	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

// makeLogBody returns rows unique log lines separated by newlines with no
// trailing newline. boundTranscriptDetail splits on "\n", so a trailing
// newline would add a phantom empty row to the count.
func makeLogBody(rows int) string {
	var b strings.Builder
	for i := 0; i < rows; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "row-%02d", i+1)
	}
	return b.String()
}

// hiddenLinesExchange builds a synthetic tool exchange whose "Output" body
// exceeds the transcript detail row bound (12 rows) so writeItemDetail emits
// its hidden-lines hint. Each output row is unique so a test can assert
// which rows survived the head+tail crop.
func hiddenLinesExchange(id string, rows int) []transcript.Entry {
	return []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{
			Type: transcript.BlockToolUse,
			Tool: &toolcall.Activity{ID: id, Kind: "read", Title: "Read synthetic log"},
		}}},
		{Seq: 2, Role: transcript.RoleUser, Blocks: []transcript.Block{{
			Type: transcript.BlockToolResult,
			Tool: &toolcall.Activity{
				ID:     id,
				Kind:   "read",
				Status: "completed",
				Output: []byte(makeLogBody(rows)),
			},
		}}},
	}
}

// diffExchange builds a synthetic edit exchange whose New code body exceeds
// the row bound so writeNewCodeCards emits its hidden-lines hint. The
// content lives on the result-side activity so the effective-activity merge
// in writeTranscriptItem preserves it (mirrors monitor_transcript_card_test's
// pattern).
func diffExchange(id, path string, rows int) []transcript.Entry {
	body := makeLogBody(rows)
	return []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{
			Type: transcript.BlockToolUse,
			Tool: &toolcall.Activity{ID: id, Kind: "edit", Title: "Edit synthetic file"},
		}}},
		{Seq: 2, Role: transcript.RoleUser, Blocks: []transcript.Block{{
			Type: transcript.BlockToolResult,
			Tool: &toolcall.Activity{
				ID: id, Kind: "edit", Status: "completed",
				Content: []toolcall.Content{{Diff: &toolcall.Diff{Path: path, NewText: body}}},
			},
		}}},
	}
}

// TestWriteItemDetailHiddenLinesHint locks FR-06.6: the writeItemDetail
// truncation hint uses the shared MoreItems + ExpandHint vocabulary, is
// pluralized against the hidden count, and derives the expand key from the
// live Toggle binding.
func TestWriteItemDetailHiddenLinesHint(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 80
	m.chatItemExpandAll = true
	m.setChatPage(transcript.Page{Entries: hiddenLinesExchange("hint", 20)})

	plain := stripANSI(m.itemTranscriptBody())
	// 20 rows - 12 kept = 8 hidden.
	want := "… 8 more lines [enter: Expand]"
	if !strings.Contains(plain, want) {
		t.Fatalf("writeItemDetail hidden-lines hint missing %q:\n%s", want, plain)
	}
	if strings.Contains(plain, "lines hidden") {
		t.Fatalf("retired wording \"lines hidden\" still rendered:\n%s", plain)
	}
}

// TestWriteItemDetailHiddenLinesHintSingular locks FR-06.1: MoreItems
// pluralizes on n == 1 so a one-row hidden count produces "1 more line", not
// "1 more lines".
func TestWriteItemDetailHiddenLinesHintSingular(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 80
	m.chatItemExpandAll = true
	// 13 rows -> 12 kept, 1 hidden.
	m.setChatPage(transcript.Page{Entries: hiddenLinesExchange("hint-singular", 13)})

	plain := stripANSI(m.itemTranscriptBody())
	want := "… 1 more line [enter: Expand]"
	if !strings.Contains(plain, want) {
		t.Fatalf("singular hidden-lines hint missing %q:\n%s", want, plain)
	}
	if strings.Contains(plain, "1 more lines") {
		t.Fatalf("singular case rendered plural word:\n%s", plain)
	}
}

// TestWriteNewCodeCardsHiddenLinesHint locks FR-06.6 for the second call
// site: writeNewCodeCards emits the shared hint with its four-space indent.
func TestWriteNewCodeCardsHiddenLinesHint(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 96
	m.chatItemExpandAll = true
	m.setChatPage(transcript.Page{Entries: diffExchange("edit-hint", "internal/synthetic/file.go", 20)})

	plain := stripANSI(m.itemTranscriptBody())
	want := "… 8 more lines [enter: Expand]"
	if !strings.Contains(plain, want) {
		t.Fatalf("writeNewCodeCards hidden-lines hint missing %q:\n%s", want, plain)
	}
	if strings.Contains(plain, "lines hidden") {
		t.Fatalf("retired wording \"lines hidden\" still rendered:\n%s", plain)
	}
}

// TestCaptureTruncatedHint locks the write-time capture-truncated literal
// routes through shared.CaptureTruncatedHint. The Truncated flag is set on
// the tool-use block, so writeTranscriptItem must emit the hint verbatim.
func TestCaptureTruncatedHint(t *testing.T) {
	entries := []transcript.Entry{{
		Seq: 1, Role: transcript.RoleAssistant,
		Blocks: []transcript.Block{{
			Type:      transcript.BlockToolUse,
			Truncated: true,
			Tool:      &toolcall.Activity{ID: "cap", Kind: "read", Title: "Read big log"},
		}},
	}, {
		Seq: 2, Role: transcript.RoleUser,
		Blocks: []transcript.Block{{
			Type: transcript.BlockToolResult,
			Tool: &toolcall.Activity{ID: "cap", Kind: "read", Status: "completed"},
		}},
	}}
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 80
	m.chatItemExpandAll = true
	m.setChatPage(transcript.Page{Entries: entries})

	plain := stripANSI(m.itemTranscriptBody())
	want := shared.CaptureTruncatedHint()
	if !strings.Contains(plain, want) {
		t.Fatalf("capture-truncated hint missing %q:\n%s", want, plain)
	}
}

// TestWriteItemDetailHintRespectsToggleRebind locks FR-06.7: the expand
// key text is read from the Monitor's live Toggle binding at render time.
// Rebinding Toggle changes the rendered hint without editing the renderer.
func TestWriteItemDetailHintRespectsToggleRebind(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 80
	m.chatItemExpandAll = true
	m.setChatPage(transcript.Page{Entries: hiddenLinesExchange("rebind", 20)})

	m.keys.Toggle = keybind.NewBinding(
		keybind.WithKeys("ctrl+o"),
		keybind.WithHelp("ctrl+o", "expand"),
	)
	// Bust the header-only card cache so the item re-renders with the
	// rebound key; the hint lives below the card, but re-rendering keeps
	// the surrounding body prefix stable.
	m.chatItemRendered = make(map[transcriptRenderKey]string)

	plain := stripANSI(m.itemTranscriptBody())
	want := "[ctrl+o: Expand]"
	if !strings.Contains(plain, want) {
		t.Fatalf("rebound key text missing %q:\n%s", want, plain)
	}
	if strings.Contains(plain, "[enter: Expand]") {
		t.Fatalf("default key text leaked into rebind case:\n%s", plain)
	}
}

// TestTruncationVocabularyRetirement is FR-06.8: a grep-based Monitor
// package regression that fails if any of the retired literals reappears in
// a non-test source file. expandView's byte-elision marker is exempted via
// an explicit allowlist since it reports a distinct concept (KB elision
// inside a single blob, not a hidden-rows count).
func TestTruncationVocabularyRetirement(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read monitor pkg dir: %v", err)
	}

	// Files that are explicitly allowed to carry a legacy "… <n> …"
	// literal, with the reason so a maintainer editing this test can
	// re-justify each exception in one place.
	allowed := map[string]string{
		"monitor_transcript.go": "expandView's byte-elision marker (\"\\n… %d KB elided …\\n\") is out of scope per slice 06 non-goal 1",
		"monitor_view.go":       "Security pane's \"… %d more finding\" wording is out of scope per slice 06 non-goal 7 (Security pane, not Transcript panel)",
	}
	// Retired quantitative-hint literals. The regex catches any bare
	// "… " prefix followed on the same line by a %d conversion.
	retiredExact := []string{"lines hidden"}
	retiredPattern := regexp.MustCompile(`"… [^"]*%d`)

	var (
		sawMoreItems  bool
		sawExpandHint bool
	)
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		src := string(data)
		for _, lit := range retiredExact {
			if strings.Contains(src, lit) {
				t.Errorf("%s contains retired literal %q; use the shared truncation vocabulary", name, lit)
			}
		}
		if _, ok := allowed[name]; !ok {
			if loc := retiredPattern.FindString(src); loc != "" {
				t.Errorf("%s contains hand-rolled truncation literal %q outside the shared vocabulary", name, loc)
			}
		}
		if name == "monitor_transcript_items_view.go" {
			sawMoreItems = strings.Contains(src, "shared.MoreItems(")
			sawExpandHint = strings.Contains(src, "shared.ExpandHint(")
		}
	}
	if !sawMoreItems {
		t.Errorf("monitor_transcript_items_view.go missing shared.MoreItems call site; retirement was inline-renamed rather than routed through the shared helper")
	}
	if !sawExpandHint {
		t.Errorf("monitor_transcript_items_view.go missing shared.ExpandHint call site; retirement was inline-renamed rather than routed through the shared helper")
	}
}

// TestTruncationMonitorGallery captures a deterministic sample of the new
// vocabulary rendered inside the Monitor: an expanded tool exchange whose
// Output body exceeds the row bound (so writeItemDetail emits its hint),
// showing both the styled and ANSI-stripped form. Opt-in via
// JIG_UI_SNAPSHOT_DIR to match the repo's proof-capture convention.
func TestTruncationMonitorGallery(t *testing.T) {
	dir := os.Getenv("JIG_UI_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set JIG_UI_SNAPSHOT_DIR to capture the truncation-vocabulary Monitor gallery")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 80
	m.chatItemExpandAll = true
	m.setChatPage(transcript.Page{Entries: hiddenLinesExchange("gallery", 20)})

	styled := m.itemTranscriptBody()
	plain := stripANSI(styled)

	var b strings.Builder
	fmt.Fprintln(&b, "# Slice 06 Monitor truncation-vocabulary capture")
	fmt.Fprintln(&b, "#")
	fmt.Fprintln(&b, "# Fabricated exchange: read synthetic log, 20 rows -> 12 kept, 8 hidden.")
	fmt.Fprintln(&b, "# transcriptInnerW=80, chatItemExpandAll=true.")
	fmt.Fprintln(&b, "# Toggle binding:", m.keys.Toggle.Help().Key, "->", m.keys.Toggle.Help().Desc)
	fmt.Fprintln(&b, "#")
	fmt.Fprintln(&b, "# --- ANSI-stripped ---")
	fmt.Fprintln(&b, plain)
	fmt.Fprintln(&b, "# --- Styled (raw ANSI, hex-escaped) ---")
	fmt.Fprintln(&b, hexEscape(styled))

	out := filepath.Join(dir, "25-task-2-monitor-vocabulary.txt")
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write gallery: %v", err)
	}

	notes := "Generated by TestTruncationMonitorGallery in internal/tui/monitor/monitor_transcript_vocabulary_test.go.\n" +
		"Command: JIG_UI_SNAPSHOT_DIR=<dir> go test ./internal/tui/monitor -run TestTruncationMonitorGallery -count=1\n" +
		"transcriptInnerW=80, displayState=toolDisplaySuccess (result Status=\"completed\").\n" +
		"Toggle.Help().Key=" + m.keys.Toggle.Help().Key + " -> the hint reads via ExpandHint(false, hidden>0, key).\n"
	if err := os.WriteFile(filepath.Join(dir, "25-task-2-monitor-vocabulary.notes.txt"), []byte(notes), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
}

// hexEscape returns s with every non-printable byte replaced by \xHH. Used
// by capture generators so a styled ANSI stream is safe to diff without
// terminal reinterpretation.
func hexEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\n':
			b.WriteByte('\n')
		case c >= 0x20 && c < 0x7f:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "\\x%02x", c)
		}
	}
	return b.String()
}
