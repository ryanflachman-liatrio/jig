package monitor

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"jig/internal/datastore"
	"jig/internal/transcript"
	"jig/internal/tui/shared"
)

// fileReadChunk is the bounded read size used by copyOutputFile so a growing
// file cannot cause an unbounded single-shot allocation. Matches the existing
// output-file cap in outputfiles.go.
const fileReadChunk = 32 * 1024

// copyOutputFileRequest builds a shared.ClipboardRequest that copies the given
// file path in full, subject to the shared 256 KiB payload/read caps. The
// request captures path and kind at admission time; the loader opens the file
// off the synchronous key handler and reads at most its initial length.
func copyOutputFileRequest(path string, kind fileKind) shared.ClipboardRequest {
	target := shared.ClipboardTarget{Surface: shared.ClipboardSurfaceMonitorFile, Label: path}
	if path == "" {
		return shared.ClipboardRequest{
			Target: target,
			Loader: func() shared.ClipboardPayload {
				return shared.ClipboardPayload{Err: shared.ErrClipboardUnavailable}
			},
		}
	}
	_ = kind // reserved for future format-aware exports; the copy remains raw text.
	return shared.ClipboardRequest{
		Target: target,
		Loader: func() shared.ClipboardPayload {
			data, err := readFileBounded(path, outputFileCap)
			if err != nil {
				return shared.ClipboardPayload{Err: err}
			}
			return shared.ClipboardPayload{Payload: string(data)}
		},
	}
}

// readFileBounded opens path, requires a regular file, records the initial
// size, and streams only that prefix in bounded reads. A source larger than
// limit is rejected without touching the clipboard; a source that shrinks or
// unexpectedly short-reads mid-copy also rejects without dispatch.
func readFileBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", shared.ErrClipboardUnavailable, err.Error())
		}
		return nil, fmt.Errorf("%w: %s", shared.ErrClipboardUnavailable, err.Error())
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", shared.ErrClipboardUnavailable, err.Error())
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: not a regular file", shared.ErrClipboardUnavailable)
	}
	size := info.Size()
	if size == 0 {
		return nil, shared.ErrClipboardEmpty
	}
	if size > limit {
		return nil, shared.ErrClipboardOversized
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(f, buf); err != nil {
		return nil, fmt.Errorf("%w: %s", shared.ErrClipboardUnavailable, err.Error())
	}
	return buf, nil
}

// copyOutputFileCmd wraps a copyOutputFileRequest in a tea.Cmd suitable for
// direct return from a key handler.
func copyOutputFileCmd(path string, kind fileKind) tea.Cmd {
	req := copyOutputFileRequest(path, kind)
	return func() tea.Msg { return req }
}

// ── whole transcript export (Task 2) ──────────────────────────────────────

// copyTranscriptRequest builds a request that copies the recorded transcript
// for a step in durable block order, subject to the shared caps. Path/step
// identity are captured at admission time so navigation between steps cannot
// redirect the export mid-flight.
func copyTranscriptRequest(runDir, stepID string) shared.ClipboardRequest {
	target := shared.ClipboardTarget{
		Surface: shared.ClipboardSurfaceTranscriptSnapshot,
		Label:   stepID,
	}
	if runDir == "" || stepID == "" {
		return shared.ClipboardRequest{
			Target: target,
			Loader: func() shared.ClipboardPayload {
				return shared.ClipboardPayload{Err: shared.ErrClipboardUnavailable}
			},
		}
	}
	path := datastore.TranscriptPath(runDir, stepID)
	return shared.ClipboardRequest{
		Target: target,
		Loader: func() shared.ClipboardPayload {
			payload, notes, err := exportRecordedTranscript(path)
			if err != nil {
				return shared.ClipboardPayload{Err: err}
			}
			return shared.ClipboardPayload{Payload: payload, Notes: notes}
		},
	}
}

// exportRecordedTranscript opens the transcript at path, captures its initial
// end offset, and streams only that prefix in complete JSONL records. It
// enforces both the raw-scan cap and the sanitized payload cap by producing
// intermediate output for shared.PrepareClipboardPayload to check.
func exportRecordedTranscript(path string) (string, string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", shared.ErrClipboardUnavailable
		}
		return "", "", fmt.Errorf("%w: %s", shared.ErrClipboardUnavailable, err.Error())
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", "", fmt.Errorf("%w: %s", shared.ErrClipboardUnavailable, err.Error())
	}
	end := info.Size()
	if end == 0 {
		return "", "", shared.ErrClipboardEmpty
	}
	if end > shared.ClipboardMaxTranscriptScanBytes {
		return "", "", fmt.Errorf("%w: recorded transcript exceeds %d byte scan cap", shared.ErrClipboardOversized, shared.ClipboardMaxTranscriptScanBytes)
	}

	limited := io.LimitReader(f, end)
	br := bufio.NewReader(limited)
	var b strings.Builder
	skipped := 0
	entries := 0
	for {
		line, readErr := br.ReadString('\n')
		complete := strings.HasSuffix(line, "\n")
		trimmed := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if complete && trimmed != "" {
			var entry transcript.Entry
			if json.Unmarshal([]byte(trimmed), &entry) == nil {
				serialized := serializeTranscriptEntry(entry)
				if entries > 0 {
					b.WriteString("\n")
				}
				b.WriteString(serialized)
				entries++
			} else {
				skipped++
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", "", fmt.Errorf("%w: %s", shared.ErrClipboardUnavailable, readErr.Error())
		}
	}
	if entries == 0 && skipped == 0 {
		return "", "", shared.ErrClipboardEmpty
	}
	notes := ""
	if skipped > 0 {
		if skipped == 1 {
			notes = "1 malformed record skipped"
		} else {
			notes = fmt.Sprintf("%d malformed records skipped", skipped)
		}
	}
	return b.String(), notes, nil
}

// copyTranscriptCmd wraps copyTranscriptRequest in a tea.Cmd.
func copyTranscriptCmd(runDir, stepID string) tea.Cmd {
	req := copyTranscriptRequest(runDir, stepID)
	return func() tea.Msg { return req }
}

// serializeTranscriptEntry emits every block in one entry with the header
// contract from the spec (role, block type, seq, generation, iteration,
// attempt). Bodies are formatted by serializeBlockBody. Blocks are separated
// by a blank line; entries themselves are separated by a blank line by the
// caller so ordering and provenance are observable.
func serializeTranscriptEntry(entry transcript.Entry) string {
	var b strings.Builder
	for i, block := range entry.Blocks {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(formatBlockHeader(entry, block))
		body := serializeBlockBody(block)
		if body != "" {
			b.WriteString("\n")
			b.WriteString(body)
		}
		if block.Truncated {
			if body != "" {
				b.WriteString("\n")
			}
			b.WriteString("[recorded content truncated]")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// formatBlockHeader is the identifying prefix for one whole-transcript block.
// It intentionally does not carry timestamps; those are per-entry and
// consuming tools reconstruct chronology from seq/gen/iter/attempt.
func formatBlockHeader(entry transcript.Entry, block transcript.Block) string {
	kind := string(block.Type)
	if kind == "" {
		kind = "unknown"
	}
	return fmt.Sprintf("== %s %s · seq %d · gen %d · iter %d · attempt %d ==",
		entry.Role, kind, entry.Seq, entry.Generation, entry.Iteration, entry.Attempt)
}

// serializeBlockBody produces the readable body for one block. Text/thinking
// emit their recorded text; tool blocks emit a normalized JSON activity so
// every decoded field is present; unsupported blocks emit a labeled decoded
// JSON so nothing disappears.
func serializeBlockBody(block transcript.Block) string {
	switch block.Type {
	case transcript.BlockText, transcript.BlockThinking:
		return block.Text
	case transcript.BlockToolUse, transcript.BlockToolResult:
		activity := block.Activity()
		if activity == nil {
			return "[tool block missing activity]"
		}
		name := activity.Title
		if name == "" {
			name = "(unnamed)"
		}
		id := activity.ID
		status := activity.Status
		header := fmt.Sprintf("tool %s · %s · id=%s · status=%s", block.Type, name, id, status)
		enc, err := json.MarshalIndent(activity, "", "  ")
		if err != nil {
			return header
		}
		return header + "\n" + string(enc)
	default:
		// Unknown block type: emit a labeled decoded JSON so the operator sees
		// exactly what was in the durable record.
		enc, err := json.MarshalIndent(block, "", "  ")
		if err != nil {
			return fmt.Sprintf("[unsupported block %q]", block.Type)
		}
		return fmt.Sprintf("[unsupported block %q]\n%s", block.Type, string(enc))
	}
}

// ── selected transcript item (Task 3) ─────────────────────────────────────

// copyTranscriptItemCmd builds a request for the currently selected transcript
// item on the given model. All source data is captured up front by cloning the
// referenced blocks; the loader only formats.
func (m Model) copyTranscriptItemCmd() tea.Cmd {
	item, ok := m.selectedTranscriptItem()
	if !ok {
		return clipboardImmediateItemUnavailable("no item selected")
	}
	entries := m.chatEntries
	return copyTranscriptItemRequest(item, entries)
}

func (m Model) selectedTranscriptItem() (transcriptItem, bool) {
	if n := len(m.chatVisibleItems); n == 0 || m.chatItemCursor < 0 || m.chatItemCursor >= n {
		return transcriptItem{}, false
	}
	return m.chatVisibleItems[m.chatItemCursor], true
}

// copyTranscriptItemRequest builds a request whose payload is the concatenated
// body of one item's members. A single text/thinking item copies its recorded
// text with no role header; a matched tool exchange emits use then result via
// the shared block-body serializer.
func copyTranscriptItemRequest(item transcriptItem, entries []transcript.Entry) tea.Cmd {
	target := shared.ClipboardTarget{
		Surface: shared.ClipboardSurfaceTranscriptItem,
		Label:   describeTranscriptItem(item),
	}
	blocks := collectItemBlocks(item, entries)
	req := shared.ClipboardRequest{
		Target: target,
		Loader: func() shared.ClipboardPayload {
			if len(blocks) == 0 {
				return shared.ClipboardPayload{Err: shared.ErrClipboardEmpty}
			}
			var b strings.Builder
			for i, blk := range blocks {
				if i > 0 {
					b.WriteString("\n\n")
				}
				b.WriteString(itemBodyFor(item, blk))
				if blk.Truncated {
					b.WriteString("\n[recorded content truncated]")
				}
			}
			var notes string
			if item.toolUse == nil && item.kind == transcriptItemToolExchange {
				notes = "tool_use member missing from loaded page"
			}
			if item.toolResult == nil && item.kind == transcriptItemToolExchange {
				if notes != "" {
					notes += "; "
				}
				notes += "tool_result member missing from loaded page"
			}
			return shared.ClipboardPayload{Payload: b.String(), Notes: notes}
		},
	}
	return func() tea.Msg { return req }
}

// collectItemBlocks deep-copies the referenced blocks so the loader is
// unaffected by a subsequent reload of the loaded page.
func collectItemBlocks(item transcriptItem, entries []transcript.Entry) []transcript.Block {
	refs := itemMembers(item)
	blocks := make([]transcript.Block, 0, len(refs))
	for _, ref := range refs {
		if ref.entryIdx < 0 || ref.entryIdx >= len(entries) {
			continue
		}
		entry := entries[ref.entryIdx]
		if ref.blockIdx < 0 || ref.blockIdx >= len(entry.Blocks) {
			continue
		}
		blocks = append(blocks, entry.Blocks[ref.blockIdx])
	}
	return blocks
}

// itemBodyFor produces the payload for one member of an item. Text/thinking
// items use their raw recorded text (no role header). Tool members use the
// shared block-body serializer so both surfaces agree on formatting.
func itemBodyFor(item transcriptItem, block transcript.Block) string {
	switch item.kind {
	case transcriptItemText, transcriptItemThinking, transcriptItemSystem:
		return block.Text
	default:
		return serializeBlockBody(block)
	}
}

// describeTranscriptItem returns a short human-readable label used in the
// clipboard notice so the operator sees exactly what was captured.
func describeTranscriptItem(item transcriptItem) string {
	switch item.kind {
	case transcriptItemText:
		return "message"
	case transcriptItemThinking:
		return "thinking"
	case transcriptItemToolExchange:
		return "tool exchange"
	case transcriptItemToolResult:
		return "tool result"
	case transcriptItemSystem:
		return "system"
	default:
		return "item"
	}
}

// clipboardImmediateItemUnavailable produces a message that surfaces a "no
// item selected" or similar rejection without going through the loader path.
func clipboardImmediateItemUnavailable(reason string) tea.Cmd {
	target := shared.ClipboardTarget{Surface: shared.ClipboardSurfaceTranscriptItem, Label: reason}
	return func() tea.Msg {
		return shared.ClipboardRequest{
			Target: target,
			Loader: func() shared.ClipboardPayload {
				return shared.ClipboardPayload{Err: shared.ErrClipboardUnavailable}
			},
		}
	}
}
