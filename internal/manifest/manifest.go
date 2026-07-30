// Package manifest persists every engine event to disk so the run is
// auditable and resumable (resume deferred to Phase 5).
//
// Writer is an engine.Event consumer that:
//  1. Appends every event as a JSONL line to journal.jsonl
//     (opened O_APPEND|O_CREATE|O_WRONLY — one write per line, safe for
//     concurrent reads by log-tail tooling).
//  2. On terminal StepStatus (succeeded / failed / skipped), writes
//     steps/<id>/result.json from state.Result so each step's outcome is
//     inspectable without replaying the journal.
//
// The Writer is created once per run (in Manager.Start) and is called
// synchronously inside emit(), before any fan-out to subscribers.  This
// preserves the "journal before fan-out" invariant: in-memory state is always
// fold(journal), and the TUI can never have seen something the journal missed.
//
// # Import-cycle note
//
// manifest deliberately does NOT import jig/internal/engine.  The engine
// package's Envelope encoding uses engine.MarshalEnvelope; to avoid the
// engine → manifest → engine import cycle, the engine's emit() method
// pre-encodes each line into a byte slice and passes (line, terminalStepID)
// to Writer.AppendLine.  The engine knows which events are terminal step
// transitions; the manifest writer only needs to persist them and write
// result.json.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"

	"jig/internal/datastore"
)

// Writer appends events to journal.jsonl and materializes per-step result.json.
// All methods are called from the scheduler goroutine; no synchronisation needed.
type Writer struct {
	runDir  string
	journal *os.File
}

// NewWriter opens (or creates) journal.jsonl inside runDir and returns a Writer
// ready to receive events.  The caller is responsible for calling Close when
// the run finishes.
func NewWriter(runDir string) (*Writer, error) {
	path := datastore.JournalPath(runDir)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("manifest: open journal %q: %w", path, err)
	}
	return &Writer{runDir: runDir, journal: f}, nil
}

// StepTerminal carries the minimal fields needed to write result.json when a
// step reaches a terminal status.  It is filled by the engine's emit() method,
// which already knows the current step state.
type StepTerminal struct {
	StepID  string
	Status  string // "succeeded" | "failed" | "skipped"
	Attempt int
}

// AppendLine writes one pre-encoded JSONL line to journal.jsonl.  If terminal
// is non-nil the step's result.json is also written.  Errors are silently
// dropped — a journal write failure is unfortunate but must not kill a live run.
func (w *Writer) AppendLine(line []byte, terminal *StepTerminal) {
	// Append the line plus newline in a single write; O_APPEND makes this
	// atomic on POSIX for writes smaller than PIPE_BUF (~4 KiB for most events).
	_, _ = w.journal.Write(append(line, '\n'))

	if terminal != nil {
		w.writeResult(terminal)
	}
}

// writeResult serialises the terminal step summary into steps/<stepID>/result.json.
func (w *Writer) writeResult(t *StepTerminal) {
	// Ensure the step subdirectory exists (RunDir pre-creates it, but be safe).
	_, err := datastore.StepDir(w.runDir, t.StepID)
	if err != nil {
		return
	}
	path := datastore.ResultPath(w.runDir, t.StepID)
	result := stepResultJSON{
		StepID:  t.StepID,
		Status:  t.Status,
		Attempt: t.Attempt,
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// stepResultJSON is the shape written to steps/<id>/result.json.
// Phase 4 will enrich this with the full step.Result fields.
type stepResultJSON struct {
	StepID  string `json:"step_id"`
	Status  string `json:"status"`
	Attempt int    `json:"attempt"`
}

// Close flushes and closes the journal file.  Call once after the final event
// has been appended (after RunFinished).
func (w *Writer) Close() error {
	return w.journal.Close()
}
