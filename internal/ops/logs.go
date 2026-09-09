package ops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/transcript"
)

const DefaultLogTail = 200

type LogOptions struct {
	StepID          string
	Tail            int
	All             bool
	IncludeThinking bool
	OnOmitted       func(int)
}

type LogEntry struct {
	RunID  string           `json:"run_id"`
	StepID string           `json:"step_id"`
	Entry  transcript.Entry `json:"entry"`
}

type LogBatch struct {
	Entries []LogEntry
	Omitted int
	Cursors map[string]int64
}

func ReadLogs(root, runID string, opts LogOptions) (LogBatch, error) {
	runDir, err := datastore.ResolveRunDir(root, runID)
	if err != nil {
		return LogBatch{}, err
	}
	return readLogsDir(runDir, runID, opts)
}

func readLogsDir(runDir, runID string, opts LogOptions) (LogBatch, error) {
	records, err := engine.ReplayJournalRecords(runDir)
	if err != nil && len(records) == 0 {
		return LogBatch{}, fmt.Errorf("logs: read journal: %w", err)
	}
	steps := declaredSteps(records)
	if len(steps) == 0 {
		return LogBatch{}, fmt.Errorf("logs: run %q has no declared steps", runID)
	}
	if opts.StepID != "" {
		found := false
		for _, id := range steps {
			if id == opts.StepID {
				found = true
				break
			}
		}
		if !found {
			return LogBatch{}, fmt.Errorf("logs: unknown step %q", opts.StepID)
		}
		steps = []string{opts.StepID}
	}
	for _, id := range steps {
		if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\\`) {
			return LogBatch{}, fmt.Errorf("logs: invalid persisted step id %q", id)
		}
	}
	limit := opts.Tail
	if limit == 0 && !opts.All {
		limit = DefaultLogTail
	}
	if limit < 0 {
		return LogBatch{}, fmt.Errorf("logs: --tail must be non-negative")
	}

	batch := LogBatch{Cursors: make(map[string]int64)}
	for _, id := range steps {
		reader, openErr := transcript.Open(datastore.TranscriptPath(runDir, id))
		if openErr != nil {
			return LogBatch{}, openErr
		}
		var entries []transcript.Entry
		var end int64
		if opts.All {
			entries, openErr = reader.Window(0, 0)
			if openErr == nil {
				page, pageErr := reader.PageAfter(0, max(1, len(entries)+1))
				if pageErr != nil {
					openErr = pageErr
				} else {
					end = page.End
				}
			}
		} else {
			page, pageErr := reader.TailPage(limit)
			openErr, entries, end = pageErr, page.Entries, page.End
			count, countErr := reader.Count()
			if countErr != nil {
				return LogBatch{}, countErr
			}
			if count > len(entries) {
				batch.Omitted += count - len(entries)
			}
		}
		if openErr != nil {
			return LogBatch{}, openErr
		}
		end, openErr = reader.CompleteCursor()
		if openErr != nil {
			return LogBatch{}, openErr
		}
		batch.Cursors[id] = end
		for _, entry := range entries {
			batch.Entries = append(batch.Entries, LogEntry{RunID: runID, StepID: id, Entry: entry})
		}
	}
	sortLogEntries(batch.Entries, steps)
	if !opts.All && len(batch.Entries) > limit {
		batch.Omitted += len(batch.Entries) - limit
		batch.Entries = append([]LogEntry(nil), batch.Entries[len(batch.Entries)-limit:]...)
	}
	return batch, nil
}

func declaredSteps(records []engine.JournalRecord) []string {
	for _, record := range records {
		if started, ok := record.Event.(engine.RunStarted); ok {
			return append([]string(nil), started.Steps...)
		}
	}
	return nil
}

func sortLogEntries(entries []LogEntry, steps []string) {
	order := make(map[string]int, len(steps))
	for i, id := range steps {
		order[id] = i
	}
	sort.SliceStable(entries, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC3339Nano, entries[i].Entry.Ts)
		tj, _ := time.Parse(time.RFC3339Nano, entries[j].Entry.Ts)
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		if order[entries[i].StepID] != order[entries[j].StepID] {
			return order[entries[i].StepID] < order[entries[j].StepID]
		}
		return entries[i].Entry.Seq < entries[j].Entry.Seq
	})
}

func RenderLogText(item LogEntry, includeThinking bool) string {
	e := item.Entry
	var meta []string
	if e.Generation != 0 {
		meta = append(meta, fmt.Sprintf("gen=%d", e.Generation))
	}
	if e.Iteration != 0 {
		meta = append(meta, fmt.Sprintf("iter=%d", e.Iteration))
	}
	if e.Attempt != 0 {
		meta = append(meta, fmt.Sprintf("attempt=%d", e.Attempt))
	}
	header := fmt.Sprintf("%s %s %s", e.Ts, item.StepID, e.Role)
	if len(meta) > 0 {
		header += " " + strings.Join(meta, " ")
	}
	var out bytes.Buffer
	out.WriteString(header)
	out.WriteByte('\n')
	for _, block := range e.Blocks {
		switch block.Type {
		case transcript.BlockText:
			out.WriteString(block.Text)
			if !strings.HasSuffix(block.Text, "\n") {
				out.WriteByte('\n')
			}
		case transcript.BlockThinking:
			if includeThinking {
				out.WriteString(block.Text)
				if !strings.HasSuffix(block.Text, "\n") {
					out.WriteByte('\n')
				}
			} else {
				out.WriteString("[thinking omitted]\n")
			}
		case transcript.BlockToolUse, transcript.BlockToolResult:
			renderTool(&out, block)
		default:
			fmt.Fprintf(&out, "[%s]\n", block.Type)
		}
	}
	return strings.TrimRight(out.String(), "\n") + "\n"
}

func renderTool(out *bytes.Buffer, block transcript.Block) {
	a := block.Activity()
	if a == nil {
		fmt.Fprintf(out, "[%s]\n", block.Type)
		return
	}
	title := a.Title
	if title == "" {
		title = a.Kind
	}
	if title == "" {
		title = string(block.Type)
	}
	status := a.Status
	if status == "" {
		status = "started"
	}
	fmt.Fprintf(out, "[%s] %s %s\n", block.Type, title, status)
	writeRawIndented(out, a.Input)
	writeRawIndented(out, a.Output)
	for _, content := range a.Content {
		if content.Text != "" {
			writeIndented(out, content.Text)
		} else if len(content.Raw) > 0 {
			writeRawIndented(out, content.Raw)
		}
	}
}

func writeRawIndented(out *bytes.Buffer, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) == nil {
		writeIndented(out, compact.String())
		return
	}
	writeIndented(out, string(raw))
}

func writeIndented(out *bytes.Buffer, content string) {
	for _, line := range strings.Split(content, "\n") {
		fmt.Fprintf(out, "  %s\n", line)
	}
}
