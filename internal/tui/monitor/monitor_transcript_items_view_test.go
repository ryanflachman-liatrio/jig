package monitor

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"jig/internal/step"
	"jig/internal/toolcall"
	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

func syntheticExchange(id, status string) []transcript.Entry {
	entries := []transcript.Entry{{
		Seq: 1, Role: transcript.RoleAssistant,
		Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: id, Kind: "read", Title: "Reading synthetic config"}}},
	}}
	if status != "" {
		entries = append(entries, transcript.Entry{
			Seq: 2, Role: transcript.RoleUser,
			Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: id, Kind: "read", Status: status}}},
		})
	}
	return entries
}

func TestToolExchangeHeaderCardStateMapping(t *testing.T) {
	tests := []struct {
		name string
		got  toolDisplayState
		want shared.CardState
	}{
		{name: "success", got: toolDisplaySuccess, want: shared.CardSuccess},
		{name: "error", got: toolDisplayError, want: shared.CardError},
		{name: "running", got: toolDisplayRunning, want: shared.CardRunning},
		{name: "incomplete use", got: toolDisplayUnknownUse, want: shared.CardWarning},
		{name: "incomplete result", got: toolDisplayUnknownResult, want: shared.CardWarning},
		{name: "invalid", got: toolDisplayState(99), want: shared.CardWarning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cardState(tt.got); got != tt.want {
				t.Fatalf("cardState(%v) = %v, want %v", tt.got, got, tt.want)
			}
		})
	}
}

func TestToolExchangeHeaderCardStatesAndWidths(t *testing.T) {
	tests := []struct {
		name      string
		status    string
		running   bool
		wantState toolDisplayState
		wantIcon  string
	}{
		// FR-02.15: settled success on kind "read" gets the read signature glyph.
		{name: "success", status: "completed", wantState: toolDisplaySuccess, wantIcon: shared.IconToolRead},
		// FR-02.12: state prose ("failed", "running", "incomplete") is gone.
		// FR-02.5/02.15: the icon slot carries the state.
		{name: "failed", status: "failed", wantState: toolDisplayError, wantIcon: shared.IconStatusError},
		{name: "running use only", running: true, wantState: toolDisplayRunning, wantIcon: shared.IconStatusRunning},
		{name: "terminal incomplete use only", wantState: toolDisplayUnknownUse, wantIcon: shared.IconStatusWarning},
	}
	for _, width := range []int{40, 72} {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("%s_width_%d", tt.name, width), func(t *testing.T) {
				m := newMonitorWithSteps(t)
				m.transcriptInnerW = width
				m.steps[m.index["a"]].status = step.StatusPending
				if tt.running {
					m.steps[m.index["a"]].status = step.StatusRunning
				}
				m.chatStep = "a"
				m.setChatPage(transcript.Page{Entries: syntheticExchange("state", tt.status)})
				if len(m.chatItems) != 1 || m.chatItems[0].displayState != tt.wantState {
					t.Fatalf("display state = %+v, want %v", m.chatItems, tt.wantState)
				}
				body := m.itemTranscriptBody()
				rows := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
				if len(rows) != 2 {
					t.Fatalf("header-only card rows = %d, want 2:\n%s", len(rows), stripANSI(body))
				}
				for i, row := range rows {
					if got := lipgloss.Width(row); got != width {
						t.Fatalf("row %d width = %d, want %d: %q", i, got, width, row)
					}
				}
				plain := stripANSI(body)
				if !strings.HasPrefix(plain, "  ▌ ╭") || !strings.Contains(plain, "\n  ▌ ╰") {
					t.Fatalf("selected prefix was not applied to both rows:\n%s", plain)
				}
				if !strings.Contains(plain, tt.wantIcon) {
					t.Fatalf("header missing state icon %q:\n%s", tt.wantIcon, plain)
				}
				for _, forbidden := range []string{" failed", " · running", " · incomplete"} {
					if strings.Contains(plain, forbidden) {
						t.Fatalf("header retained state prose %q:\n%s", forbidden, plain)
					}
				}
			})
		}
	}
}

// FR-02.15 / CC-4: only settling to success may swap the icon glyph.
// A running or pending row keeps the generic pending glyph even when the
// tool kind is known, so a running "edit" shows the same glyph as a running
// "read". Settled success is the only state that emits the tool's signature
// glyph.
func TestToolExchangeHeaderSignatureGlyphOnlyOnSettledSuccess(t *testing.T) {
	tests := []struct {
		name     string
		kind     string
		status   string
		wantIcon string
	}{
		{name: "settled read shows read signature", kind: "read", status: "completed", wantIcon: shared.IconToolRead},
		{name: "settled edit shows edit signature", kind: "edit", status: "completed", wantIcon: shared.IconToolEdit},
		{name: "settled bash shows shell signature", kind: "bash", status: "completed", wantIcon: shared.IconToolShell},
		{name: "running edit stays on generic pending", kind: "edit", status: "", wantIcon: shared.IconStatusRunning},
		{name: "running read stays on generic pending", kind: "read", status: "", wantIcon: shared.IconStatusRunning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMonitorWithSteps(t)
			m.transcriptInnerW = 60
			if tt.status == "" {
				m.steps[m.index["a"]].status = step.StatusRunning
			}
			m.chatStep = "a"
			entries := []transcript.Entry{{
				Seq: 1, Role: transcript.RoleAssistant,
				Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "signature", Kind: tt.kind, Title: tt.kind + " synthetic"}}},
			}}
			if tt.status != "" {
				entries = append(entries, transcript.Entry{
					Seq: 2, Role: transcript.RoleUser,
					Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "signature", Kind: tt.kind, Status: tt.status}}},
				})
			}
			m.setChatPage(transcript.Page{Entries: entries})
			plain := stripANSI(m.itemTranscriptBody())
			if !strings.Contains(plain, tt.wantIcon) {
				t.Fatalf("header missing signature glyph %q for kind %q status %q:\n%s", tt.wantIcon, tt.kind, tt.status, plain)
			}
		})
	}
}

// FR-02.18: selection emphasizes only the title fragment. The card frame,
// description, and meta are not re-wrapped in TranscriptSelected, so the
// styled title fragment appears verbatim in the raw ANSI output while the
// description text is not enclosed by that same style. State color still
// comes from the border and the icon (both unaffected by selection).
func TestToolExchangeHeaderSelectedTitleOnly(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: syntheticExchange("selected", "completed")})
	raw := m.itemTranscriptBody()

	titleStyle := shared.Theme.Chat.TranscriptSelected.Inherit(shared.Theme.Chat.ToolTitle)
	styledTitle := titleStyle.Render("Read")
	if !strings.Contains(raw, styledTitle) {
		t.Fatalf("selected title fragment missing from body:\nwant contained: %q\nbody:\n%s", styledTitle, raw)
	}
	selectedDescription := shared.Theme.Chat.TranscriptSelected.Render("")
	if selectedDescription != "" && strings.Contains(raw, shared.Theme.Chat.TranscriptSelected.Render("synthetic")) {
		t.Fatalf("TranscriptSelected style leaked outside the title slot:\n%s", raw)
	}
}

// FR-02.17: `toolErrorHint` is written into the Meta slot so it stays
// visible on collapsed error rows without occupying the title. The title
// keeps only `action` (e.g. "Read") and the hint text is styled as meta —
// distinct from the title style — regardless of whether a description is
// present.
func TestToolExchangeHeaderErrorHintInMeta(t *testing.T) {
	entries := []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{
			Type: transcript.BlockToolUse,
			Tool: &toolcall.Activity{ID: "err", Kind: "read", Title: "Read", Input: []byte(`{"file_path":"/tmp/forbidden/config.toml"}`)},
		}}},
		{Seq: 2, Role: transcript.RoleUser, Blocks: []transcript.Block{{
			Type: transcript.BlockToolResult,
			Tool: &toolcall.Activity{ID: "err", Kind: "read", Status: "failed", Content: []toolcall.Content{{Type: "text", Text: "permission denied"}}},
		}}},
	}
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 96
	m.setChatPage(transcript.Page{Entries: entries})
	raw := m.itemTranscriptBody()
	plain := stripANSI(raw)

	if !strings.Contains(plain, "permission denied") {
		t.Fatalf("error hint absent from header body:\n%s", plain)
	}
	// FR-02.17: the title stays exactly `Read`, so the styled title
	// fragment must not include the hint text.
	titleStyled := shared.Theme.Chat.ToolTitle.Render("Read permission denied")
	if strings.Contains(raw, titleStyled) {
		t.Fatalf("error hint entered the title slot:\n%s", raw)
	}
	// FR-02.17: the hint text is rendered as meta (distinct style from
	// the title), so the meta-styled substring must be present.
	metaStyled := shared.Theme.Chat.ToolMeta.Render("permission denied")
	if !strings.Contains(raw, metaStyled) {
		t.Fatalf("error hint not styled as meta:\n want contained: %q\nraw:\n%s", metaStyled, raw)
	}
	// The row must never end in a dangling meta separator.
	if strings.HasSuffix(strings.TrimRight(plain, " │╮╯"), " · ") {
		t.Fatalf("meta slot ended with dangling separator:\n%s", plain)
	}
}

// FR-02.12: state prose has left every Monitor header code path. This is
// a broad grep-based regression that covers success/error/running/incomplete
// side-by-side. Any future accidental reintroduction (e.g. from a helper
// concatenating `" failed"` back into a title) fails here.
func TestMonitorHeaderNoStateProseRegression(t *testing.T) {
	entries := []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "ok", Kind: "read", Title: "Reading a"}}}},
		{Seq: 2, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "ok", Kind: "read", Status: "completed"}}}},
		{Seq: 3, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "bad", Kind: "read", Title: "Reading b"}}}},
		{Seq: 4, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "bad", Kind: "read", Status: "failed"}}}},
		{Seq: 5, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "run", Kind: "read", Title: "Reading c"}}}},
		{Seq: 6, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "warn", Kind: "read", Title: "Reading d"}}}},
	}
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 80
	m.steps[m.index["a"]].status = step.StatusRunning
	m.chatStep = "a"
	m.setChatPage(transcript.Page{Entries: entries})
	plain := stripANSI(m.itemTranscriptBody())
	for _, forbidden := range []string{" failed", " · running", " · incomplete", "Read failed", "Read running", "Read incomplete"} {
		if strings.Contains(plain, forbidden) {
			t.Fatalf("state prose %q leaked into a header:\n%s", forbidden, plain)
		}
	}
}

func TestToolExchangeHeaderCardSelectedAndUnselectedPrefixes(t *testing.T) {
	entries := append(syntheticExchange("one", "completed"), []transcript.Entry{
		{Seq: 3, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "two", Kind: "read", Title: "Reading second"}}}},
		{Seq: 4, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "two", Kind: "read", Status: "failed"}}}},
	}...)
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: entries})
	plain := stripANSI(m.itemTranscriptBody())
	if !strings.Contains(plain, "  ▌ ╭") || !strings.Contains(plain, "\n  ╭") {
		t.Fatalf("selected/unselected card prefixes missing:\n%s", plain)
	}
	for _, row := range strings.Split(strings.TrimSuffix(m.itemTranscriptBody(), "\n"), "\n") {
		if row == "" {
			continue
		}
		if got := lipgloss.Width(row); got != 60 {
			t.Fatalf("prefixed card row width = %d, want 60: %q", got, row)
		}
	}
	if len(m.chatItemRendered) != 2 {
		t.Fatalf("card cache size = %d, want 2", len(m.chatItemRendered))
	}
}

func TestOrphanAndNonExchangeItemsStayFlat(t *testing.T) {
	entries := []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "assistant prose"}}},
		{Seq: 2, Role: transcript.RoleSystem, Blocks: []transcript.Block{{Type: transcript.BlockText, Text: "system prose"}}},
		{Seq: 3, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockThinking, Text: "synthetic reasoning"}}},
		{Seq: 4, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockType("future"), Text: "future content"}}},
		{Seq: 5, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "orphan", Kind: "read", Status: "failed"}}}},
	}
	m := newMonitorWithSteps(t)
	m.setChatPage(transcript.Page{Entries: entries})
	plain := stripANSI(m.itemTranscriptBody())
	if strings.ContainsAny(plain, "╭╰") {
		t.Fatalf("non-exchange item acquired a card frame:\n%s", plain)
	}
	for _, want := range []string{"assistant prose", "system prose", "reasoning", "Unsupported future", "Result (unknown origin)", shared.IconStatusError} {
		if !strings.Contains(plain, want) {
			t.Fatalf("flat output missing %q:\n%s", want, plain)
		}
	}
	// FR-02.16 orphan path: uses RenderStatusLine, so it carries state via
	// the icon; the old " failed" suffix must not reappear.
	if strings.Contains(plain, "Result (unknown origin) failed") {
		t.Fatalf("orphan flat row retained state prose:\n%s", plain)
	}
}

func TestToolExchangeRendersHeaderOnlyCardAndOrphanStaysFlat(t *testing.T) {
	entries := []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "read-1", Kind: "read", Title: "Reading config"}}}},
		{Seq: 2, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "read-1", Kind: "read", Status: "completed"}}}},
		{Seq: 3, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "orphan", Kind: "read", Status: "failed"}}}},
	}
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.setChatPage(transcript.Page{Entries: entries})
	body := m.itemTranscriptBody()
	plain := stripANSI(body)
	if strings.Count(plain, "╭") != 1 || strings.Count(plain, "╰") != 1 {
		t.Fatalf("paired exchange should have exactly one framed header card:\n%s", plain)
	}
	if !strings.Contains(plain, "Result (unknown origin)") || !strings.Contains(plain, shared.IconStatusError) {
		t.Fatalf("orphan result presentation changed:\n%s", plain)
	}
	for _, row := range strings.Split(body, "\n") {
		if lipgloss.Width(row) > m.transcriptInnerW {
			t.Fatalf("row overflows transcript width: %d > %d: %q", lipgloss.Width(row), m.transcriptInnerW, row)
		}
	}
	if len(m.chatItemRendered) != 1 {
		t.Fatalf("card cache size = %d, want one exchange entry", len(m.chatItemRendered))
	}
}

func TestToolExchangeCardCacheRefreshesOnPageReplacementAndWidthChange(t *testing.T) {
	page := transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "tool-1", Kind: "read", Title: "Reading"}}}},
		{Seq: 2, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "tool-1", Kind: "read", Status: "completed"}}}},
	}}
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.rebuildRenderer()
	m.setChatPage(page)
	first := m.itemTranscriptBody()
	if len(m.chatItemRendered) != 1 {
		t.Fatalf("first render cache = %d, want 1", len(m.chatItemRendered))
	}
	_ = m.itemTranscriptBody()
	if len(m.chatItemRendered) != 1 {
		t.Fatalf("cached render grew cache to %d", len(m.chatItemRendered))
	}

	page.Entries[1].Blocks[0].Tool.Status = "failed"
	m.setChatPage(page)
	if len(m.chatItemRendered) != 0 {
		t.Fatalf("page replacement retained card cache: %d", len(m.chatItemRendered))
	}
	second := m.itemTranscriptBody()
	if first == second || !strings.Contains(stripANSI(second), shared.IconStatusError) {
		t.Fatalf("replacement did not refresh card output:\n%s", stripANSI(second))
	}
	if strings.Contains(stripANSI(second), " failed") {
		t.Fatalf("state prose reappeared in header after replacement:\n%s", stripANSI(second))
	}

	m.transcriptInnerW = 44
	m.rebuildRenderer()
	if len(m.chatItemRendered) != 0 {
		t.Fatalf("width rebuild retained card cache: %d", len(m.chatItemRendered))
	}
	_ = m.itemTranscriptBody()
	if len(m.chatItemRendered) != 1 {
		t.Fatalf("width rebuild did not cache current variant: %d", len(m.chatItemRendered))
	}
}

// FR-02.19 / task 2.8: transitioning a running exchange to settled success
// changes the composed header — not merely the border color. The generic
// pending glyph swaps to the kind's signature glyph, so the cache key's
// `header` component differs and a stale render cannot be served.
func TestToolExchangeHeaderRunningToSuccessTransitionSwapsSignatureGlyph(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.transcriptInnerW = 60
	m.steps[m.index["a"]].status = step.StatusRunning
	m.chatStep = "a"
	m.setChatPage(transcript.Page{Entries: []transcript.Entry{{
		Seq: 1, Role: transcript.RoleAssistant,
		Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "e", Kind: "edit", Title: "Editing synthetic"}}},
	}}})
	running := stripANSI(m.itemTranscriptBody())
	if !strings.Contains(running, shared.IconStatusRunning) || strings.Contains(running, shared.IconToolEdit) {
		t.Fatalf("running row did not use the generic pending glyph:\n%s", running)
	}

	m.setChatPage(transcript.Page{Entries: []transcript.Entry{
		{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "e", Kind: "edit", Title: "Editing synthetic"}}}},
		{Seq: 2, Role: transcript.RoleUser, Blocks: []transcript.Block{{Type: transcript.BlockToolResult, Tool: &toolcall.Activity{ID: "e", Kind: "edit", Status: "completed"}}}},
	}})
	settled := stripANSI(m.itemTranscriptBody())
	if !strings.Contains(settled, shared.IconToolEdit) {
		t.Fatalf("settled success did not swap to the edit signature glyph:\n%s", settled)
	}
	if strings.Contains(settled, shared.IconStatusRunning) {
		t.Fatalf("settled success kept the generic pending glyph:\n%s", settled)
	}
	if running == settled {
		t.Fatalf("running→success transition produced identical body:\n%s", settled)
	}
}

// FR-02.20 / task 2.9: with persistence off (`RunDir == ""`), the transcript
// body renders the empty-state banner and never enters `itemTranscriptBody`,
// so `RenderStatusLine` is not called and no card cache entries appear.
func TestPersistenceOffKeepsEmptyStateOutOfStatusLineHeader(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.RunDir = ""
	m.chatStep = "a"
	m.reloadTranscript()

	if len(m.chatEntries) != 0 || len(m.chatItems) != 0 {
		t.Fatalf("persistence-off retained entries/items: entries=%d items=%d", len(m.chatEntries), len(m.chatItems))
	}
	body := stripANSI(m.chatBody())
	if !strings.Contains(body, "Persistence is off") {
		t.Fatalf("persistence-off did not render the empty-state banner:\n%s", body)
	}
	if strings.ContainsAny(body, "╭╰") {
		t.Fatalf("persistence-off body acquired a card frame:\n%s", body)
	}
	if len(m.chatItemRendered) != 0 {
		t.Fatalf("persistence-off populated card cache: %d entries", len(m.chatItemRendered))
	}
	if got := m.itemTranscriptBody(); got != "" {
		t.Fatalf("persistence-off itemTranscriptBody() returned non-empty output:\n%s", got)
	}
}

func TestStructuredEditShowsNewCodeInDefaultOpenCard(t *testing.T) {
	oldCode := "func greeting() string { return \"old implementation\" }"
	newCode := "func greeting() string {\n\treturn \"new implementation\"\n}"
	entries := []transcript.Entry{
		{
			Seq:  1,
			Role: transcript.RoleAssistant,
			Blocks: []transcript.Block{{
				Type: transcript.BlockToolUse,
				Tool: &toolcall.Activity{ID: "edit-1", Kind: "edit", Title: "Editing files"},
			}},
		},
		{
			Seq:  2,
			Role: transcript.RoleUser,
			Blocks: []transcript.Block{{
				Type: transcript.BlockToolResult,
				Tool: &toolcall.Activity{
					ID:     "edit-1",
					Kind:   "edit",
					Status: "completed",
					Content: []toolcall.Content{{Diff: &toolcall.Diff{
						Path:    "internal/greeting.go",
						OldText: &oldCode,
						NewText: newCode,
					}}},
				},
			}},
		},
	}

	m := newMonitorWithSteps(t)
	m.setChatPage(transcript.Page{Entries: entries})
	if len(m.chatItems) != 1 || !m.chatItemExpand[m.chatItems[0].key] {
		t.Fatalf("structured edit was not expanded by default: %+v", m.chatItemExpand)
	}

	body := stripANSI(m.itemTranscriptBody())
	for _, want := range []string{"New code · internal/greeting.go", "new implementation", "╭", "╰"} {
		if !strings.Contains(body, want) {
			t.Fatalf("default-open code card missing %q:\n%s", want, body)
		}
	}
	for _, unwanted := range []string{"old implementation", "old:"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("code card included %q:\n%s", unwanted, body)
		}
	}

	m.chatItemExpand[m.chatItems[0].key] = false
	if body := stripANSI(m.itemTranscriptBody()); strings.Contains(body, "new implementation") {
		t.Fatalf("folded code card remained visible:\n%s", body)
	}
}
