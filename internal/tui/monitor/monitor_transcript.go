package monitor

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/step"
	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

const (
	transcriptScrollRows     = 2
	transcriptFastScrollRows = 10
)

func (m *Model) scrollTranscript(rows int) {
	if rows > 0 {
		m.chatVP.ScrollDown(rows)
	} else {
		m.chatVP.ScrollUp(-rows)
	}
	m.updateTranscriptFollow(m.chatVP.AtBottom())
}

// reloadTranscript re-points the Transcript panel at the cursor's step and reads
// its transcript eagerly (Resolved Decision 10), resetting per-step view state so
// block-cursor/expand toggles never carry over between steps (seq keys are only
// meaningful within one step's transcript).
func (m *Model) reloadTranscript() {
	stepID := m.cursorStepID()
	if stepID == "" {
		return
	}
	wasFile := m.selKind == "file"

	// File row: set selKind/selFile so chatBody renders the file.
	rows := m.visibleRows()
	if m.cursor >= 0 && m.cursor < len(rows) && rows[m.cursor].isFileRow() {
		f := rows[m.cursor].file
		if f != nil {
			m.selKind = "file"
			m.selFile = f.path
			m.chatAutoScroll = false
			m.pendingGPrefix = false
			if m.ready {
				m.chatVP.GotoTop()
			}
		}
		return
	}

	// Step row: revert to chat transcript.
	m.selKind = ""
	m.selFile = ""

	if stepID == m.chatStep {
		if wasFile {
			m.resumeTranscriptFollow()
		}
		return
	}
	m.chatStep = stepID
	m.chatItems = nil
	m.chatVisibleItems = nil
	m.chatItemCursor = 0
	m.chatItemExpand = make(map[transcriptItemKey]bool)
	m.chatItemExpandAll = false
	m.chatItemRendered = make(map[transcriptRenderKey]string)
	m.chatItemLineRanges = make(map[transcriptLineKey]lineRange)
	m.chatPage = transcript.Page{}
	m.searchOpen = false
	m.searchQuery = ""
	m.searchHits = nil
	m.searchHitCursor = 0
	m.filterOpen = false
	m.filters = transcriptFilters{}
	// blockKey is (seq, block) and seq restarts per step-file, so cached renders
	// from the previous step would collide with the new step's same-seq blocks.
	// Reset the render cache along with the other per-step view state.
	m.chatRendered = make(map[blockKey]string)
	m.pendingGPrefix = false
	m.loadChatTail()
	m.resumeTranscriptFollow()
}

func (m Model) latestChatSeq() int {
	seq := m.msgCount[m.chatStep]
	if n := len(m.chatEntries); n > 0 && m.chatEntries[n-1].Seq > seq {
		seq = m.chatEntries[n-1].Seq
	}
	return seq
}

func (m Model) unseenChatEntries() int {
	if m.chatAutoScroll {
		return 0
	}
	return max(m.msgCount[m.chatStep]-m.chatSeenSeq, 0)
}

func (m Model) showsTranscriptFollow() bool {
	if m.chatStep == "" || m.selKind == "file" {
		return false
	}
	_, review := m.reviews[m.chatStep]
	return !review || len(m.chatEntries) > 0
}

func (m *Model) resumeTranscriptFollow() {
	m.chatAutoScroll = true
	m.chatSeenSeq = m.latestChatSeq()
	if m.ready {
		m.chatVP.GotoBottom()
	}
}

func (m *Model) updateTranscriptFollow(atBottom bool) {
	if atBottom && !m.chatPage.HasLater {
		loadedSeq := 0
		if n := len(m.chatEntries); n > 0 {
			loadedSeq = m.chatEntries[n-1].Seq
		}
		if m.msgCount[m.chatStep] > loadedSeq {
			m.loadChatTail()
			m.refreshPanels()
		}
		m.resumeTranscriptFollow()
		return
	}
	m.chatAutoScroll = false
}

// loadChatTail re-reads the newest bounded page. The transcript file remains the
// source of truth; byte cursors make both memory use and page-read work
// proportional to chatWindowMax rather than total run length.
func (m *Model) loadChatTail() {
	// Preserve the block cursor across same-step reloads (e.g. a new StepMessage
	// arriving while the user is navigating). The saved key won't be found in a
	// freshly-loaded different step, so cursor correctly resets to 0 on step changes.
	if m.RunDir == "" || m.chatStep == "" {
		m.setChatPage(transcript.Page{})
		return
	}
	r, err := transcript.Open(datastore.TranscriptPath(m.RunDir, m.chatStep))
	if err != nil {
		return
	}
	page, err := r.TailPage(chatWindowMax)
	if err != nil {
		return
	}
	page = completeToolBoundaryContext(r, page)
	m.setChatPage(page)
}

func (m *Model) setChatPage(page transcript.Page) {
	// A replacement may retain item keys while changing the terminal activity or
	// result. Header cards therefore cannot survive a page replacement.
	m.chatItemRendered = make(map[transcriptRenderKey]string)
	var savedItem transcriptItemKey
	if len(m.chatVisibleItems) > 0 && m.chatItemCursor >= 0 && m.chatItemCursor < len(m.chatVisibleItems) {
		savedItem = m.chatVisibleItems[m.chatItemCursor].key
	}
	m.chatPage = page
	m.chatEntries = page.Entries
	m.chatItems = buildTranscriptItems(page.Entries, m.currentChatStepRunning())
	m.defaultExpandEditCodeItems()
	m.chatVisibleItems = nil
	m.prunePageState()
	m.rebuildTranscriptItemState(savedItem)
	m.rerunSearch()
}

// defaultExpandEditCodeItems opens structured edits until an operator explicitly
// folds one. The map retains a false value after that action, so transcript
// reloads do not override the operator's choice.
func (m *Model) defaultExpandEditCodeItems() {
	for _, item := range m.chatItems {
		if _, configured := m.chatItemExpand[item.key]; configured || !itemHasStructuredDiff(m.chatEntries, item) {
			continue
		}
		m.chatItemExpand[item.key] = true
	}
}

func itemHasStructuredDiff(entries []transcript.Entry, item transcriptItem) bool {
	for _, ref := range itemMembers(item) {
		activity := entries[ref.entryIdx].Blocks[ref.blockIdx].Activity()
		if activity == nil {
			continue
		}
		for _, content := range activity.Content {
			if content.Diff != nil {
				return true
			}
		}
	}
	return false
}

// rebuildTranscriptItemState establishes the item list as the page-local
// navigation source. Filtering will later replace the visible slice while
// retaining this same stable-key restoration behavior.
func (m *Model) rebuildTranscriptItemState(saved transcriptItemKey) {
	m.chatVisibleItems = m.filteredTranscriptItems()
	if len(m.chatVisibleItems) == 0 {
		m.chatItemCursor = 0
		return
	}
	m.chatItemCursor = 0
	for i, item := range m.chatVisibleItems {
		if item.key == saved {
			m.chatItemCursor = i
			return
		}
	}
}

// filteredTranscriptItems retains a complete conversation unit whenever one
// of its members matches. In particular, a tool input or output hit never
// splits the exchange into two independently visible rows.
func (m Model) filteredTranscriptItems() []transcriptItem {
	query := strings.TrimSpace(strings.ToLower(m.searchQuery))
	if query == "" && !m.filters.active() {
		return m.chatItems
	}
	visible := make([]transcriptItem, 0, len(m.chatItems))
	for _, item := range m.chatItems {
		for _, ref := range itemMembers(item) {
			entry := m.chatEntries[ref.entryIdx]
			block := entry.Blocks[ref.blockIdx]
			if m.blockMatchesView(entry, block, query) {
				visible = append(visible, item)
				break
			}
		}
	}
	return visible
}

func (m Model) currentChatStepRunning() bool {
	i, ok := m.index[m.chatStep]
	return ok && m.steps[i].status == step.StatusRunning
}

func (m *Model) loadOlderChat() {
	if !m.chatPage.HasEarlier || m.RunDir == "" || m.chatStep == "" {
		return
	}
	r, err := transcript.Open(datastore.TranscriptPath(m.RunDir, m.chatStep))
	if err != nil {
		return
	}
	page, err := r.PageBefore(m.chatPage.Start, chatWindowMax)
	if err != nil || len(page.Entries) == 0 {
		return
	}
	page = completeToolBoundaryContext(r, page)
	m.setChatPage(page)
	m.chatAutoScroll = false
	if m.ready {
		m.refreshPanels()
		m.chatVP.GotoBottom()
	}
}

func (m *Model) loadNewerChat() {
	if !m.chatPage.HasLater || m.RunDir == "" || m.chatStep == "" {
		return
	}
	r, err := transcript.Open(datastore.TranscriptPath(m.RunDir, m.chatStep))
	if err != nil {
		return
	}
	page, err := r.PageAfter(m.chatPage.End, chatWindowMax)
	if err != nil || len(page.Entries) == 0 {
		return
	}
	page = completeToolBoundaryContext(r, page)
	m.setChatPage(page)
	m.chatAutoScroll = false
	if m.ready {
		m.refreshPanels()
		m.chatVP.GotoTop()
	}
}

func (m *Model) loadChatBefore(end int64) bool {
	if m.RunDir == "" || m.chatStep == "" {
		return false
	}
	r, err := transcript.Open(datastore.TranscriptPath(m.RunDir, m.chatStep))
	if err != nil {
		return false
	}
	page, err := r.PageBefore(end, chatWindowMax)
	if err != nil {
		return false
	}
	page = completeToolBoundaryContext(r, page)
	m.setChatPage(page)
	return true
}

func completeToolBoundaryContext(r *transcript.Reader, page transcript.Page) transcript.Page {
	if len(page.Entries) == 0 {
		return page
	}
	if page.HasEarlier && toolOnlyEntry(page.Entries[0]) {
		if before, err := r.PageBefore(page.Start, chatBoundaryContextMax); err == nil &&
			len(before.Entries) > 0 && toolOnlyEntry(before.Entries[len(before.Entries)-1]) {
			page.Entries = append(before.Entries, page.Entries...)
			page.Start = before.Start
			page.HasEarlier = before.HasEarlier
		}
	}
	if page.HasLater && toolOnlyEntry(page.Entries[len(page.Entries)-1]) {
		if after, err := r.PageAfter(page.End, chatBoundaryContextMax); err == nil &&
			len(after.Entries) > 0 && toolOnlyEntry(after.Entries[0]) {
			page.Entries = append(page.Entries, after.Entries...)
			page.End = after.End
			page.HasLater = after.HasLater
		}
	}
	return page
}

func toolOnlyEntry(entry transcript.Entry) bool {
	if len(entry.Blocks) == 0 {
		return false
	}
	for _, blk := range entry.Blocks {
		switch blk.Type {
		case transcript.BlockToolUse, transcript.BlockToolResult:
		default:
			return false
		}
	}
	return true
}

func (m *Model) prunePageState() {
	loadedItems := make(map[transcriptItemKey]struct{}, len(m.chatItems))
	for _, item := range m.chatItems {
		loadedItems[item.key] = struct{}{}
	}
	for key := range m.chatItemExpand {
		if _, ok := loadedItems[key]; !ok {
			delete(m.chatItemExpand, key)
		}
	}
	for key := range m.chatItemRendered {
		if _, ok := loadedItems[key.itemKey]; !ok {
			delete(m.chatItemRendered, key)
		}
	}
	for key := range m.chatItemLineRanges {
		if _, ok := loadedItems[key.itemKey]; !ok {
			delete(m.chatItemLineRanges, key)
		}
	}
}

// chatBody renders one step's agent chat chain from its transcript. Loaded
// entries delegate to the normalized item renderer; this branch owns only the
// empty-state and filter chrome.
func (m *Model) chatBody() string {
	if m.selKind == "file" && m.selFile != "" {
		return m.fileBody()
	}
	if len(m.chatItems) > 0 {
		body := m.itemTranscriptBody()
		if i, ok := m.index[m.chatStep]; ok {
			s := m.steps[i]
			if s.status == step.StatusFailed && s.err != "" {
				// A transcript can end before the backend reports its terminal result.
				// Keep the recovered failure visible beside that partial evidence.
				return "  " + shared.Theme.Error.Render(shared.IconError+" "+s.err) + "\n\n" + body
			}
		}
		return body
	}

	var b strings.Builder

	i, ok := m.index[m.chatStep]
	if !ok {
		if m.chatStep == "" {
			return shared.RenderEmptyState(shared.EmptyState{
				Title: "No step selected",
				Body:  "Select a step in the Steps panel to view its transcript.",
				CTA:   "j/k  select · enter  open",
			})
		}
		return shared.RenderEmptyState(shared.EmptyState{
			Title: "Unknown step",
			Body:  "No step named " + m.chatStep + " in this run.",
		})
	}
	s := m.steps[i]
	indicator, _ := stepIndicator(s.status)
	header := indicator + "  " + statusStyle(s.status).Render(string(s.status))
	var context []string
	if s.iteration > 0 {
		context = append(context, fmt.Sprintf("iter %d", s.iteration+1))
	}
	if s.attempt > 0 {
		context = append(context, fmt.Sprintf("attempt %d", s.attempt))
	}
	if len(context) > 0 {
		header += "  " + shared.Theme.Chat.Hint.Render(strings.Join(context, " • "))
	}
	b.WriteString("  " + header + "\n\n")

	if m.searchOpen {
		b.WriteString("  " + shared.Theme.Accent.Render("/") + " " + m.searchInput.View() + "\n")
	}
	var viewState []string
	if status := m.searchStatus(); status != "" {
		viewState = append(viewState, status)
	}
	if filters := m.filterSummary(); filters != "" {
		viewState = append(viewState, "filters: "+filters)
	}
	if len(viewState) > 0 {
		b.WriteString("  " + shared.Theme.Chat.Hint.Render(strings.Join(viewState, "  •  ")) + "\n\n")
	}
	if m.filterOpen {
		b.WriteString("  " + shared.Theme.Title.Render("Transcript filters") + "\n")
		for i, label := range filterLabels {
			mark := "[ ]"
			if m.filterEnabled(i) {
				mark = "[x]"
			}
			line := fmt.Sprintf("%s %s", mark, label)
			if i == m.filterCursor {
				line = shared.Theme.SelectedLine.Render("› " + line)
			} else {
				line = "  " + line
			}
			b.WriteString("  " + line + "\n")
		}
		b.WriteString("  " + shared.Theme.Chat.Hint.Render("j/k move · space toggle · enter/esc close") + "\n\n")
	}

	running := s.status == step.StatusRunning
	hasTail := false
	if buf, ok := m.stepOutput[m.chatStep]; ok && buf.Len() > 0 {
		hasTail = true
	}

	if len(m.chatEntries) == 0 {
		// Review steps have no transcript. Their immutable document inventory is
		// the useful closed-state context; opening the Gate owns document browsing.
		if rev, ok := m.reviews[m.chatStep]; ok {
			m.writeReviewOverview(&b, rev)
			return b.String()
		}
		if m.RunDir == "" {
			b.WriteString(shared.RenderEmptyState(shared.EmptyState{
				Title: "Transcript unavailable",
				Body:  "Persistence is off for this run — nothing was captured.",
			}))
		} else if running && !hasTail {
			b.WriteString(shared.RenderEmptyState(shared.EmptyState{
				Title: "Waiting for step to start",
				Body:  "Output will appear here once the step begins writing.",
			}))
		} else if !running && !hasTail {
			if s.err != "" {
				b.WriteString("  " + shared.Theme.Error.Render(s.err) + "\n")
			} else if s.status == step.StatusPending {
				b.WriteString(shared.RenderEmptyState(shared.EmptyState{
					Title: "Waiting for step to start",
					Body:  "This step has not begun yet.",
				}))
			} else {
				b.WriteString(shared.RenderEmptyState(shared.EmptyState{
					Title: "No transcript captured",
					Body:  "This step finished without recorded output.",
				}))
			}
		}
	}
	return b.String()
}

func (m *Model) writeReviewOverview(b *strings.Builder, request engine.ReviewRequest) {
	reviewed, comments := 0, 0
	for i := range m.inputQueue {
		entry := &m.inputQueue[i]
		if entry.kind != inputKindReview || entry.stepID != request.StepID || entry.workspace == nil {
			continue
		}
		comments = len(entry.workspace.Comments())
		for _, document := range entry.workspace.Documents() {
			if entry.workspace.Reviewed(document.ID) {
				reviewed++
			}
		}
		break
	}

	outcome, submitted := m.reviewOutcomeFor(request)
	if submitted {
		reviewed = len(request.Documents)
		comments = outcome.commentCount
		b.WriteString("  " + shared.Theme.Valid.Render("Review submitted") + "\n")
		b.WriteString(fmt.Sprintf("  Verdict: %s · %d / %d documents reviewed · %d comments\n\n",
			outcome.verdict, reviewed, len(request.Documents), comments))
	} else {
		b.WriteString(fmt.Sprintf("  %d / %d documents reviewed · %d comments",
			reviewed, len(request.Documents), comments))
		if request.RoundID != "" {
			b.WriteString(" · round " + request.RoundID)
		}
		b.WriteString("\n\n")
	}

	if len(request.Documents) == 0 {
		b.WriteString("  " + shared.Theme.Chat.Hint.Render("No review document descriptors were captured.") + "\n")
		return
	}
	for _, document := range request.Documents {
		mark := "○"
		for i := range m.inputQueue {
			entry := &m.inputQueue[i]
			if entry.kind == inputKindReview && entry.stepID == request.StepID && entry.workspace != nil && entry.workspace.Reviewed(document.ID) {
				mark = "✓"
				break
			}
		}
		if submitted {
			mark = "✓"
		}
		label := document.Label
		if label == "" {
			label = document.ID
		}
		format := reviewFormatLabel(document.Format)
		lineWord := "lines"
		if document.LineCount == 1 {
			lineWord = "line"
		}
		b.WriteString(fmt.Sprintf("  %s  %s  %s · %d %s\n", mark, label, format, document.LineCount, lineWord))
	}
	if !submitted {
		hint := "Open this review from the Gate panel."
		if m.historical {
			hint = "Resume this run from Home to reopen the review workspace."
		}
		b.WriteString("\n  " + shared.Theme.Chat.Hint.Render(hint) + "\n")
	}
}

func (m *Model) reviewOutcomeFor(request engine.ReviewRequest) (reviewOutcome, bool) {
	outcome, ok := m.reviewOutcomes[request.StepID]
	if !ok || outcome.roundID != "" && request.RoundID != "" && outcome.roundID != request.RoundID {
		return reviewOutcome{}, false
	}
	return outcome, true
}

func reviewFormatLabel(format string) string {
	switch format {
	case "markdown":
		return "Markdown"
	case "diff":
		return "Diff"
	case "text":
		return "Plain text"
	case "":
		return "Document"
	default:
		return format
	}
}

// renderMarkdown renders a text block as markdown, caching the result per block.
// Glamour recognizes code fences and delegates them to the registered
// Chroma/Lip Gloss formatter. The cache map is shared across the value copies of
// Model, so writing to it here persists even though the receiver is by value; the
// map is invalidated wholesale on a width change (rebuildRenderer).
func (m Model) renderMarkdown(key blockKey, text string) string {
	if cached, ok := m.chatRendered[key]; ok {
		return cached
	}
	out := text
	if m.renderer != nil {
		if rendered, err := m.renderer.Render(text); err == nil {
			out = rendered
		}
	}
	if m.chatRendered != nil {
		m.chatRendered[key] = out
	}
	return out
}

// renderInsetMarkdown uses the renderer whose wrap width reserves room for the
// thick-bar prefix around expanded tool content.
func (m Model) renderInsetMarkdown(key blockKey, text string) string {
	if cached, ok := m.chatRendered[key]; ok {
		return cached
	}
	out := text
	if m.insetRenderer != nil {
		if rendered, err := m.insetRenderer.Render(text); err == nil {
			out = stripBlankEdges(rendered)
		}
	}
	if m.chatRendered != nil {
		m.chatRendered[key] = out
	}
	return out
}

// fenceJSON pretty-prints s as a ```json fenced markdown block so it can be
// rendered with syntax highlighting via renderMarkdown. Returns "" when s is
// not valid JSON so callers can fall back to plain text. The caller is
// responsible for bounding s before passing it in (e.g. via expandView) when
// the source is an unbounded transcript block; file content is pre-bounded by
// readOutputFile's 256 KiB cap so no truncation is needed there.
func fenceJSON(s string) string {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return ""
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return "```json\n" + string(pretty) + "\n```"
}

// jsonlToMarkdown converts a JSONL string into a sequence of fenced JSON
// blocks so each record gets syntax highlighting via glamour. Lines that are
// not valid JSON fall back to a plain code fence.
func jsonlToMarkdown(content string) string {
	var sb strings.Builder
	first := true
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		if line == "" {
			continue
		}
		if !first {
			sb.WriteString("\n")
		}
		first = false
		if fenced := fenceJSON(line); fenced != "" {
			sb.WriteString(fenced)
		} else {
			sb.WriteString("```\n" + line + "\n```")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// expandView bounds a block's expanded content: below chatExpandMax it returns
// the content unchanged; above it, a head+tail with the middle elided so a
// write-capped 256 KiB result never lays out in full.
func expandView(s string) string {
	if len(s) <= chatExpandMax {
		return s
	}
	half := chatExpandMax / 2
	head := clampRunes(s[:half])
	tail := clampRunesTail(s[len(s)-half:])
	elided := len(s) - len(head) - len(tail)
	return head + fmt.Sprintf("\n… %d KB elided …\n", elided/1024) + tail
}

// clampRunes / clampRunesTail back a byte slice off to a rune boundary so
// expandView never splits a multibyte rune at the elision seam.
func clampRunes(s string) string {
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func clampRunesTail(s string) string {
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[1:]
	}
	return s
}

// writeVerbatim renders text as-is, one indented line per line, without markdown
// reflow. Used for command-step output (role system), where the content is
// terminal output that glamour would mangle (Phase 6).
func writeVerbatim(b *strings.Builder, text string) {
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		b.WriteString("  " + line + "\n")
	}
}

// isStructuralBlank reports whether a line's raw bytes contain only ASCII
// whitespace. An ANSI escape byte (\x1b), a printable glyph, or any other
// non-whitespace byte makes it false. This is the semantic inverse of the
// predicate used inside stripBlankEdges (which strips SGR before testing):
// a tinted padding row emitted by the shared card primitive contains
// \x1b[48;2;...m bytes and therefore counts as content here, so slice 04
// per-item edge trimming preserves it while a plain " " line does not.
func isStructuralBlank(line string) bool {
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case ' ', '\t', '\r', '\v', '\f':
			continue
		default:
			return false
		}
	}
	return true
}

// trimStructuralBlankEdges drops leading and trailing structurally-blank
// lines (see isStructuralBlank) and returns the surviving lines joined with
// "\n". An entirely blank input returns "". The trailing newline is not
// reintroduced; the caller decides its own line terminator. Used by
// itemTranscriptBody so an item whose renderer emits edge whitespace does
// not stack that whitespace on top of the inter-item separator, while
// items that emit no visible content contribute an empty string the
// caller can skip.
func trimStructuralBlankEdges(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	start := 0
	end := len(lines)
	for start < end && isStructuralBlank(lines[start]) {
		start++
	}
	for end > start && isStructuralBlank(lines[end-1]) {
		end--
	}
	if start >= end {
		return ""
	}
	return strings.Join(lines[start:end], "\n")
}

// stripBlankEdges drops leading and trailing lines that are blank when ANSI
// escape sequences are removed, then appends a single trailing newline.
// Glamour always emits a blank first line above a code block (its internal
// top-margin row) and a trailing blank; this trims both so the content sits
// flush in the panel without wasted screen rows.
//
// This is the SGR-aware sibling of trimStructuralBlankEdges: it treats a
// row containing only styling bytes as blank (Glamour's top/bottom margins),
// while trimStructuralBlankEdges preserves any row whose raw bytes contain
// a non-whitespace character (tinted card padding). Do not unify the two —
// their call sites depend on the semantic difference.
func stripBlankEdges(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	start := 0
	for start < len(lines) && strings.TrimSpace(stripSGR(lines[start])) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(stripSGR(lines[end-1])) == "" {
		end--
	}
	if start >= end {
		return "\n"
	}
	return strings.Join(lines[start:end], "\n") + "\n"
}

// stripSGR removes ANSI SGR escape sequences from s so blank-line detection
// can operate on visible content rather than styled spaces.
func stripSGR(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case inEsc:
			if r == 'm' {
				inEsc = false
			}
		case r == '\x1b':
			inEsc = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fileBody renders the currently-selected output file in the Transcript pane.
// Markdown goes through glamour, JSON is fenced+pretty then glamour, other
// types are verbatim.
func (m Model) fileBody() string {
	var b strings.Builder
	path := m.selFile

	// Find the outputFile to get its kind.
	kind := kindOther
	for _, files := range m.stepFiles {
		for _, f := range files {
			if f.path == path {
				kind = f.kind
				break
			}
		}
	}

	content, placeholder := readOutputFile(path, kind)
	if placeholder != "" {
		b.WriteString("  " + shared.Theme.Question.Render(placeholder) + "\n")
		return b.String()
	}

	// Render directly without the transcript block cache: the cache key
	// (blockKey) is integer-only, so there's no stable per-file key, and file
	// content is read fresh from disk each call anyway.
	// fileRenderer has document margin/prefix/suffix zeroed (see rebuildRenderer)
	// so content sits flush in the panel without glamour's standard document framing.
	render := func(text string) string {
		if m.fileRenderer != nil {
			if rendered, err := m.fileRenderer.Render(text); err == nil {
				return stripBlankEdges(rendered)
			}
		}
		return text
	}

	switch kind {
	case kindMarkdown:
		b.WriteString(render(content))
	case kindJSON:
		fenced := fenceJSON(content)
		if fenced == "" {
			fenced = content
		}
		b.WriteString(render(fenced))
	case kindJSONL:
		b.WriteString(render(jsonlToMarkdown(content)))
	default:
		writeVerbatim(&b, content)
	}

	return b.String()
}
