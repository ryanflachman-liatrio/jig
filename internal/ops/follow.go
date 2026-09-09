package ops

import (
	"container/heap"
	"context"
	"fmt"
	"time"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/transcript"
)

const followPageSize = 200

// FollowLogs emits the requested initial tail and then pages forward until the
// journal is terminal or ctx is cancelled. Each cursor is an append-only byte
// offset, so entries are not duplicated even when malformed lines are skipped.
func FollowLogs(ctx context.Context, root, runID string, opts LogOptions, interval time.Duration, emit func(LogEntry) error) (int, error) {
	if interval <= 0 {
		interval = 300 * time.Millisecond
	}
	runDir, err := datastore.ResolveRunDir(root, runID)
	if err != nil {
		return 0, err
	}
	initial, err := readLogsDir(runDir, runID, opts)
	if err != nil {
		return 0, err
	}
	if initial.Omitted > 0 && opts.OnOmitted != nil {
		opts.OnOmitted(initial.Omitted)
	}
	for _, item := range initial.Entries {
		if err := emit(item); err != nil {
			return initial.Omitted, err
		}
	}
	records, _ := engine.ReplayJournalRecords(runDir)
	steps := declaredSteps(records)
	if opts.StepID != "" {
		steps = []string{opts.StepID}
	}
	cursors := initial.Cursors

	poll := func() (bool, error) {
		if emitErr := mergeFollowPages(runDir, runID, steps, cursors, emit); emitErr != nil {
			return false, emitErr
		}
		latest, replayErr := engine.ReplayJournalRecords(runDir)
		if replayErr != nil {
			return false, fmt.Errorf("logs: follow journal: %w", replayErr)
		}
		for _, record := range latest {
			if _, ok := record.Event.(engine.RunFinished); ok {
				return true, nil
			}
		}
		return false, nil
	}

	if terminal, pollErr := poll(); terminal || pollErr != nil {
		return initial.Omitted, pollErr
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return initial.Omitted, ctx.Err()
		case <-ticker.C:
			terminal, pollErr := poll()
			if pollErr != nil || terminal {
				return initial.Omitted, pollErr
			}
		}
	}
}

type followSource struct {
	stepID string
	order  int
	reader *transcript.Reader
	page   transcript.Page
	index  int
	item   LogEntry
}

type followHeap []*followSource

func (h followHeap) Len() int { return len(h) }
func (h followHeap) Less(i, j int) bool {
	ti, _ := time.Parse(time.RFC3339Nano, h[i].item.Entry.Ts)
	tj, _ := time.Parse(time.RFC3339Nano, h[j].item.Entry.Ts)
	if !ti.Equal(tj) {
		return ti.Before(tj)
	}
	if h[i].order != h[j].order {
		return h[i].order < h[j].order
	}
	return h[i].item.Entry.Seq < h[j].item.Entry.Seq
}
func (h followHeap) Swap(i, j int)   { h[i], h[j] = h[j], h[i] }
func (h *followHeap) Push(value any) { *h = append(*h, value.(*followSource)) }
func (h *followHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func mergeFollowPages(runDir, runID string, steps []string, cursors map[string]int64, emit func(LogEntry) error) error {
	h := &followHeap{}
	heap.Init(h)
	for order, id := range steps {
		reader, err := transcript.Open(datastore.TranscriptPath(runDir, id))
		if err != nil {
			return err
		}
		source := &followSource{stepID: id, order: order, reader: reader}
		if err := advanceFollowSource(source, runID, cursors); err != nil {
			return err
		}
		if source.item.StepID != "" {
			heap.Push(h, source)
		}
	}
	for h.Len() > 0 {
		source := heap.Pop(h).(*followSource)
		if err := emit(source.item); err != nil {
			return err
		}
		if err := advanceFollowSource(source, runID, cursors); err != nil {
			return err
		}
		if source.item.StepID != "" {
			heap.Push(h, source)
		}
	}
	return nil
}

func advanceFollowSource(source *followSource, runID string, cursors map[string]int64) error {
	for {
		if source.index < len(source.page.Entries) {
			entry := source.page.Entries[source.index]
			source.index++
			source.item = LogEntry{RunID: runID, StepID: source.stepID, Entry: entry}
			return nil
		}
		if len(source.page.Entries) > 0 && !source.page.HasLater {
			source.item = LogEntry{}
			return nil
		}
		page, err := source.reader.PageAfter(cursors[source.stepID], followPageSize)
		if err != nil {
			return err
		}
		if page.End == cursors[source.stepID] && len(page.Entries) == 0 {
			source.item = LogEntry{}
			return nil
		}
		cursors[source.stepID] = page.End
		source.page = page
		source.index = 0
		if len(page.Entries) == 0 && !page.HasLater {
			source.item = LogEntry{}
			return nil
		}
	}
}
