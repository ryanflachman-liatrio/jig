// Package runexport builds disclosure-safe, local-only diagnostic bundles from
// inactive persisted runs. Its public result deliberately contains no source
// values, paths, or identifiers beyond the caller-supplied destination.
package runexport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"jig/internal/engine"
	"jig/internal/ops"
	"jig/internal/workflow"
)

var (
	// ErrUsage identifies a request that cannot safely name an export target.
	ErrUsage = errors.New("run export: invalid request")
	// ErrOperational identifies a safe operational refusal without source detail.
	ErrOperational = errors.New("run export: unavailable")
)

// Options names the only caller-controlled export inputs. Root is the .jig
// persistence directory; Destination is never created by acquisition.
type Options struct {
	Root        string
	RunID       string
	Destination string
	IncludeText bool
}

// Result is intentionally source-text-free so callers cannot accidentally
// expose collected evidence through logs or command output.
type Result struct {
	Destination string
	Complete    bool
	// Notices are fixed, source-text-free operator messages (e.g. the
	// best-effort text-mode warning) the CLI relays to stderr.
	Notices []string
}

// Export acquires the scheduler ownership lease before it touches evidence,
// projects a closed structural (and, if requested, sanitized text) view of
// the run, and publishes it as a ZIP archive without ever overwriting a
// competing destination.
func Export(ctx context.Context, options Options) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("%w: canceled", ErrOperational)
	}
	request, err := resolveRequest(options)
	if err != nil {
		return Result{}, err
	}
	lease, err := engine.AcquireRunLease(request.runDir)
	if err != nil {
		return Result{}, fmt.Errorf("%w: run has a live scheduler", ErrOperational)
	}
	defer lease.Close()
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("%w: canceled", ErrOperational)
	}

	inventory, err := collectInventory(request.runDir)
	if err != nil {
		return Result{}, err
	}

	root, err := os.OpenRoot(request.runDir)
	if err != nil {
		return Result{}, fmt.Errorf("%w: run evidence is unavailable", ErrOperational)
	}
	defer root.Close()

	budget := newBudget(maxTotalInput)
	aliases := newAliasTable()
	anchor := &timeAnchor{}
	var gaps []Gap
	counters := Counters{Replacements: map[string]int{}, Omissions: map[string]int{}}

	jr, journalGap := readJournal(root, inventory, budget)
	if journalGap != nil {
		gaps = append(gaps, *journalGap)
	}
	if jr.gap != nil {
		gaps = append(gaps, *jr.gap)
	}
	jr.seedAliases(aliases)
	for _, r := range jr.records {
		if !r.env.Ts.IsZero() {
			anchor.establish(r.env.Ts)
			break
		}
	}
	sawRunStarted := false
	for _, r := range jr.records {
		if _, ok := r.event.(engine.RunStarted); ok {
			sawRunStarted = true
		}
	}
	journalAuthoritative := jr.gap == nil && journalGap == nil && len(jr.records) > 0 && sawRunStarted

	wf, snapshotDir, workflowGap := readWorkflowSnapshot(root, inventory, budget)
	if workflowGap != nil {
		gaps = append(gaps, *workflowGap)
	}

	aliases.seedDiscoveredSteps(inventory.steps)

	report := ops.FoldStatus(options.RunID, jr.engineJournalRecords(), false)

	replacements := identifierReplacements(options.RunID, wf, snapshotDir, aliases, request.runDir)
	sanitizer := newExportSanitizer(replacements, &counters)

	var transcriptRecords []TranscriptRecord
	usableTranscriptEvidence := false
	for _, stepName := range inventory.steps {
		alias := aliases.stepAlias(stepName)
		rec, present := inventory.fileRecord(filepath.Join("steps", stepName, "transcript.jsonl"))
		if !present || !rec.present {
			if expectedTranscript(report, stepName) {
				gaps = append(gaps, Gap{Reason: GapMissingSource, Source: "transcript", Alias: alias})
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			return Result{}, fmt.Errorf("%w: canceled", ErrOperational)
		}
		f, err := root.Open(filepath.Join("steps", stepName, "transcript.jsonl"))
		if err != nil {
			gaps = append(gaps, Gap{Reason: GapMissingSource, Source: "transcript", Alias: alias})
			continue
		}
		lines, tgaps := decodeTranscriptPrefix(&boundedReader{r: f, b: budget}, alias)
		f.Close()
		gaps = append(gaps, tgaps...)
		if len(lines) > 0 {
			usableTranscriptEvidence = true
		}
		if !anchor.set {
			for _, l := range lines {
				if ts, tErr := time.Parse(time.RFC3339, l.entry.Ts); tErr == nil && !ts.IsZero() {
					anchor.establish(ts)
					break
				}
			}
		}
		if options.IncludeText {
			for _, l := range lines {
				scope := fmt.Sprintf("%s|%d|%d|%d", alias, l.entry.Generation, l.entry.Iteration, l.entry.Attempt)
				rec := projectTranscriptEntry(alias, l.entry, anchor, aliases, scope, sanitizer.Sanitize)
				transcriptRecords = append(transcriptRecords, rec)
			}
		}
	}
	for _, tr := range transcriptRecords {
		for _, blk := range tr.Blocks {
			if blk.Omitted != "" {
				counters.Omissions[blk.Omitted]++
			}
		}
	}

	if len(jr.records) == 0 && !usableTranscriptEvidence {
		return Result{}, fmt.Errorf("%w: run has no safely projectable evidence", ErrOperational)
	}

	run := buildRunSummary(options.RunID, report, wf, aliases, anchor, journalAuthoritative)
	runJSON, _ := json.MarshalIndent(run, "", "  ")

	events := make([]EventRecord, len(jr.records))
	for i, rec := range jr.records {
		events[i] = projectEvent(rec, aliases, anchor)
	}
	eventsJSONL, err := marshalJSONL(events)
	if err != nil {
		return Result{}, fmt.Errorf("%w: encode events", ErrOperational)
	}
	var transcriptJSONL []byte
	if options.IncludeText {
		transcriptJSONL, err = marshalJSONL(transcriptRecords)
		if err != nil {
			return Result{}, fmt.Errorf("%w: encode transcript", ErrOperational)
		}
	}

	mode := ModeStructural
	notices := []string{}
	if options.IncludeText {
		mode = ModeSanitizedText
		notices = append(notices, "text mode includes best-effort sanitized conversation text; review before sharing further")
	}
	completeness := "complete"
	if len(gaps) > 0 {
		completeness = "partial"
		notices = append(notices, fmt.Sprintf("archive is partial: %d evidence gap(s) recorded in manifest.json", len(gaps)))
	}
	readme := renderReadme(mode, run, gaps, options.IncludeText)

	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("%w: canceled", ErrOperational)
	}
	if err := inventory.Recheck(request.runDir); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("%w: canceled", ErrOperational)
	}

	tmpArchive, err := writeArchive(destinationTempDir(options.Destination), []byte(readme), runJSON, eventsJSONL, transcriptJSONL, options.IncludeText, mode, completeness, gaps, counters)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		os.Remove(tmpArchive)
		return Result{}, fmt.Errorf("%w: canceled", ErrOperational)
	}
	if err := publish(tmpArchive, options.Destination); err != nil {
		return Result{}, err
	}

	return Result{Destination: options.Destination, Complete: completeness == "complete", Notices: notices}, nil
}

func readJournal(root *os.Root, inv inventory, b *budget) (journalResult, *Gap) {
	rec, present := inv.fileRecord("journal.jsonl")
	if !present || !rec.present {
		return journalResult{}, &Gap{Reason: GapMissingSource, Source: "journal"}
	}
	f, err := root.Open("journal.jsonl")
	if err != nil {
		return journalResult{}, &Gap{Reason: GapMissingSource, Source: "journal"}
	}
	defer f.Close()
	return decodeJournalPrefix(&boundedReader{r: f, b: b}), nil
}

// readWorkflowSnapshot decodes the captured workflow and separately recovers
// its base directory (a top-level workflow.json field) for path-prefix
// sanitization: once a snapshot round-trips through RestoreExpanded, the
// decoded *workflow.Workflow itself no longer carries a source path (Resume
// never reloads or re-resolves the original checkout), but the raw JSON
// still has it, so it is read directly rather than via wf.Source().
func readWorkflowSnapshot(root *os.Root, inv inventory, b *budget) (*workflow.Workflow, string, *Gap) {
	rec, present := inv.fileRecord("workflow.json")
	if !present || !rec.present {
		return nil, "", &Gap{Reason: GapMissingSource, Source: "workflow"}
	}
	f, err := root.Open("workflow.json")
	if err != nil {
		return nil, "", &Gap{Reason: GapMissingSource, Source: "workflow"}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(&boundedReader{r: f, b: b}, maxTotalInput))
	if err != nil {
		return nil, "", &Gap{Reason: GapCorruptSource, Source: "workflow"}
	}
	wf, err := engine.DecodeWorkflowSnapshot(data)
	if err != nil {
		return nil, "", &Gap{Reason: GapCorruptSource, Source: "workflow"}
	}
	return wf, snapshotBaseDir(data), nil
}

// snapshotBaseDir extracts workflow.json's top-level base_dir field without
// re-decoding or trusting the rest of the payload; a missing/invalid field
// simply yields no path replacement rather than an error.
func snapshotBaseDir(data []byte) string {
	var probe struct {
		BaseDir string `json:"base_dir"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return ""
	}
	return probe.BaseDir
}

// expectedTranscript reports whether stepName's observed status implies a
// transcript should exist. Pending and skipped steps were never dispatched,
// so their absence is not a gap (spec: "Missing transcripts for pending,
// skipped, review, or undispatched steps are not gaps").
func expectedTranscript(report ops.RunReport, stepName string) bool {
	for _, sr := range report.Steps {
		if sr.ID == stepName {
			switch sr.Status {
			case "pending", "skipped":
				return false
			default:
				return true
			}
		}
	}
	return false
}

func identifierReplacements(runID string, wf *workflow.Workflow, snapshotBaseDir string, aliases *aliasTable, runDir string) []idReplacement {
	var out []idReplacement
	if runID != "" {
		out = append(out, idReplacement{original: runID, replacement: runAlias, boundary: "token"})
	}
	if wf != nil && wf.Meta.Name != "" {
		out = append(out, idReplacement{original: wf.Meta.Name, replacement: workflowAlias, boundary: "token"})
	}
	for orig, alias := range aliases.steps {
		out = append(out, idReplacement{original: orig, replacement: alias, boundary: "token"})
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out, idReplacement{original: home, replacement: "[HOME]", boundary: "path"})
	}
	if runDir != "" {
		out = append(out, idReplacement{original: runDir, replacement: "[RUN_ROOT]", boundary: "path"})
	}
	if snapshotBaseDir != "" {
		out = append(out, idReplacement{original: snapshotBaseDir, replacement: "[SOURCE_DIR]", boundary: "path"})
	}
	return out
}

func buildRunSummary(runID string, report ops.RunReport, wf *workflow.Workflow, aliases *aliasTable, anchor *timeAnchor, authoritative bool) RunSummary {
	wfByID := map[string]*workflow.Step{}
	if wf != nil {
		for i := range wf.Steps {
			wfByID[wf.Steps[i].ID] = &wf.Steps[i]
		}
	}
	run := RunSummary{RunAlias: runAlias, WorkflowAlias: workflowAlias, State: string(report.State), StateAuthoritative: authoritative}
	run.StartedAtMS = anchor.relativeMS(deref(report.StartedAt))
	run.UpdatedAtMS = anchor.relativeMS(deref(report.UpdatedAt))
	run.FinishedAtMS = anchor.relativeMS(deref(report.FinishedAt))
	if authoritative {
		cost := report.TotalCostUSD
		tokens := report.TotalTokens
		run.TotalCostUSD = &cost
		run.TotalTokens = &tokens
	}
	for _, sr := range report.Steps {
		alias := aliases.stepAlias(sr.ID)
		if alias == "" {
			continue
		}
		lookupID := sr.ID
		if sr.ParentID != "" {
			lookupID = sr.ParentID
		}
		summary := StepSummary{
			Alias: alias, Status: projectedStatus(sr.Status), Attempt: sr.Attempt,
			Iteration: sr.Iteration, Generation: sr.Generation,
		}
		if authoritative && sr.CostUSD != 0 {
			cost := sr.CostUSD
			summary.CostUSD = &cost
		}
		if authoritative && sr.Tokens != 0 {
			tokens := sr.Tokens
			summary.Tokens = &tokens
		}
		if st, ok := wfByID[lookupID]; ok {
			summary.Type = string(st.Type)
			summary.Backend = st.Backend
			summary.Transport = st.Transport
			for _, dep := range st.DependsOn {
				if depAlias := aliases.stepAlias(dep); depAlias != "" {
					summary.DependsOn = append(summary.DependsOn, depAlias)
				}
			}
		}
		if sr.ParentID != "" {
			summary.ParentAlias = aliases.stepAlias(sr.ParentID)
			idx, total := sr.FanOutIndex, sr.FanOutTotal
			summary.FanOutIndex, summary.FanOutTotal = &idx, &total
		}
		run.Steps = append(run.Steps, summary)
	}
	return run
}

func deref(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func marshalJSONL[T any](items []T) ([]byte, error) {
	var buf []byte
	for _, item := range items {
		line, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		buf = append(buf, line...)
		buf = append(buf, '\n')
	}
	return buf, nil
}
