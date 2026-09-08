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
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"jig/internal/datastore"
	"jig/internal/step"
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
	StepID            string
	Status            string // "succeeded" | "failed" | "skipped"
	Attempt           int
	TotalCostUSD      *float64
	Result            *step.Result
	Backend           string
	Model             string
	Transport         string
	ToolPolicy        []string
	IntegrationCommit string
	DiffSHA256        string
}

// AppendLine writes one pre-encoded JSONL line to journal.jsonl. If terminal
// is non-nil the step's result.json is also written. A journal write error is
// returned so the engine can fail closed before subscribers observe an event
// that is not durable.
func (w *Writer) AppendLine(line []byte, terminal *StepTerminal) error {
	// Append the line plus newline in one write so a process crash can damage at
	// most the trailing record; the replay reader rejects interior corruption.
	record := make([]byte, len(line)+1)
	copy(record, line)
	record[len(line)] = '\n'
	n, err := w.journal.Write(record)
	if err != nil {
		return fmt.Errorf("manifest: append journal: %w", err)
	}
	if n != len(record) {
		return fmt.Errorf("manifest: append journal: %w", io.ErrShortWrite)
	}

	if terminal != nil {
		w.writeResult(terminal)
	}
	return nil
}

// AppendBatch appends a recovery transaction's pre-encoded events with one
// write and syncs them before Resume starts the scheduler or fans them out.
func (w *Writer) AppendBatch(lines [][]byte) error {
	if len(lines) == 0 {
		return nil
	}
	var batch bytes.Buffer
	for _, line := range lines {
		batch.Write(line)
		batch.WriteByte('\n')
	}
	record := batch.Bytes()
	n, err := w.journal.Write(record)
	if err != nil {
		return fmt.Errorf("manifest: append journal batch: %w", err)
	}
	if n != len(record) {
		return fmt.Errorf("manifest: append journal batch: %w", io.ErrShortWrite)
	}
	if err := w.journal.Sync(); err != nil {
		return fmt.Errorf("manifest: sync journal batch: %w", err)
	}
	return nil
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
		StepID:       t.StepID,
		Status:       t.Status,
		Attempt:      t.Attempt,
		TotalCostUSD: t.TotalCostUSD,
		Result:       t.Result,
		Provenance: provenanceJSON{
			Backend: t.Backend, Model: t.Model, Transport: t.Transport,
			ToolPolicy: t.ToolPolicy, IntegrationCommit: t.IntegrationCommit,
			DiffSHA256:     t.DiffSHA256,
			OutputSHA256:   digestPath(resultOutputPath(t.Result)),
			ArtifactSHA256: digestArtifacts(t.Result),
		},
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// stepResultJSON is the shape written to steps/<id>/result.json.
type stepResultJSON struct {
	StepID       string         `json:"step_id"`
	Status       string         `json:"status"`
	Attempt      int            `json:"attempt"`
	TotalCostUSD *float64       `json:"total_cost_usd,omitempty"`
	Result       *step.Result   `json:"result,omitempty"`
	Provenance   provenanceJSON `json:"provenance"`
}

type provenanceJSON struct {
	Backend           string            `json:"backend,omitempty"`
	Model             string            `json:"model,omitempty"`
	Transport         string            `json:"transport,omitempty"`
	ToolPolicy        []string          `json:"tool_policy,omitempty"`
	IntegrationCommit string            `json:"integration_commit,omitempty"`
	DiffSHA256        string            `json:"diff_sha256,omitempty"`
	OutputSHA256      string            `json:"output_sha256,omitempty"`
	ArtifactSHA256    map[string]string `json:"artifact_sha256,omitempty"`
}

func resultOutputPath(result *step.Result) string {
	if result == nil {
		return ""
	}
	return result.OutputPath
}

func digestPath(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func digestArtifacts(result *step.Result) map[string]string {
	if result == nil || len(result.Artifacts) == 0 {
		return nil
	}
	digests := make(map[string]string, len(result.Artifacts))
	for name, path := range result.Artifacts {
		if digest := digestPath(path); digest != "" {
			digests[name] = digest
		}
	}
	return digests
}

// Close flushes and closes the journal file.  Call once after the final event
// has been appended (after RunFinished).
func (w *Writer) Close() error {
	return w.journal.Close()
}
