package runexport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestDamagedRunExportMatrix covers the settled/partial/corrupt evidence
// shapes spec FR-13/FR-14 require export to handle without treating
// uncertain evidence as authoritative.
func TestDamagedRunExportMatrix(t *testing.T) {
	newFixture := func(t *testing.T) (root string) {
		root, _ = buildIntactFixture(t)
		return root
	}

	t.Run("missing workflow snapshot still exports usable journal/transcript", func(t *testing.T) {
		root := newFixture(t)
		runDir := filepath.Join(root, "runs", fixtureRunName)
		if err := os.Remove(filepath.Join(runDir, "workflow.json")); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(t.TempDir(), "bundle.zip")
		if _, err := Export(context.Background(), Options{Root: root, RunID: fixtureRunName, Destination: dest}); err != nil {
			t.Fatalf("Export: %v", err)
		}
		members := readZip(t, dest)
		var manifest Manifest
		if err := json.Unmarshal(members[manifestMember], &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.Completeness != "partial" {
			t.Fatalf("completeness = %q, want partial", manifest.Completeness)
		}
		foundWorkflowGap := false
		for _, g := range manifest.Gaps {
			if g.Source == "workflow" {
				foundWorkflowGap = true
			}
		}
		if !foundWorkflowGap {
			t.Fatalf("gaps = %+v, want a workflow gap", manifest.Gaps)
		}
		var run RunSummary
		if err := json.Unmarshal(members[runMember], &run); err != nil {
			t.Fatal(err)
		}
		for _, s := range run.Steps {
			if s.Type != "" {
				t.Fatalf("step %s has a type %q despite a missing workflow snapshot", s.Alias, s.Type)
			}
		}
	})

	t.Run("corrupt workflow snapshot checksum still exports", func(t *testing.T) {
		root := newFixture(t)
		runDir := filepath.Join(root, "runs", fixtureRunName)
		if err := os.WriteFile(filepath.Join(runDir, "workflow.json"), []byte(`{"sha256":"deadbeef","toml":"corrupted"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(t.TempDir(), "bundle.zip")
		if _, err := Export(context.Background(), Options{Root: root, RunID: fixtureRunName, Destination: dest}); err != nil {
			t.Fatalf("Export: %v", err)
		}
		members := readZip(t, dest)
		var manifest Manifest
		if err := json.Unmarshal(members[manifestMember], &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.Completeness != "partial" {
			t.Fatalf("completeness = %q, want partial", manifest.Completeness)
		}
	})

	t.Run("torn journal tail is reported and state is non-authoritative", func(t *testing.T) {
		root := newFixture(t)
		runDir := filepath.Join(root, "runs", fixtureRunName)
		data, err := os.ReadFile(filepath.Join(runDir, "journal.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		torn := append(data, []byte(`{"seq":9999,"kind":"run_error"`)...) // no trailing newline
		if err := os.WriteFile(filepath.Join(runDir, "journal.jsonl"), torn, 0o600); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(t.TempDir(), "bundle.zip")
		if _, err := Export(context.Background(), Options{Root: root, RunID: fixtureRunName, Destination: dest}); err != nil {
			t.Fatalf("Export: %v", err)
		}
		members := readZip(t, dest)
		var manifest Manifest
		if err := json.Unmarshal(members[manifestMember], &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.Completeness != "partial" {
			t.Fatalf("completeness = %q, want partial", manifest.Completeness)
		}
		var run RunSummary
		if err := json.Unmarshal(members[runMember], &run); err != nil {
			t.Fatal(err)
		}
		if run.StateAuthoritative {
			t.Fatal("run state marked authoritative despite a torn journal tail")
		}
	})

	t.Run("no RunStarted leaves state non-authoritative", func(t *testing.T) {
		root := t.TempDir()
		runDir := filepath.Join(root, "runs", fixtureRunName)
		if err := os.MkdirAll(filepath.Join(runDir, "steps", "fetch"), 0o755); err != nil {
			t.Fatal(err)
		}
		lines := fixtureJournalLines(t)[1:] // drop the RunStarted record
		writeJSONLFile(t, filepath.Join(runDir, "journal.jsonl"), lines)
		transcripts := fixtureTranscripts(t)
		if err := os.WriteFile(filepath.Join(runDir, "steps", "fetch", "transcript.jsonl"), transcripts["fetch"], 0o600); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(t.TempDir(), "bundle.zip")
		if _, err := Export(context.Background(), Options{Root: root, RunID: fixtureRunName, Destination: dest}); err != nil {
			t.Fatalf("Export: %v", err)
		}
		members := readZip(t, dest)
		var run RunSummary
		if err := json.Unmarshal(members[runMember], &run); err != nil {
			t.Fatal(err)
		}
		if run.StateAuthoritative {
			t.Fatal("run state marked authoritative despite a missing RunStarted record")
		}
		if run.TotalCostUSD != nil || run.TotalTokens != nil {
			t.Fatalf("non-authoritative totals should be null: cost=%v tokens=%v", run.TotalCostUSD, run.TotalTokens)
		}
	})

	t.Run("skipped review step missing transcript is not a gap", func(t *testing.T) {
		root := newFixture(t)
		dest := filepath.Join(t.TempDir(), "bundle.zip")
		if _, err := Export(context.Background(), Options{Root: root, RunID: fixtureRunName, Destination: dest}); err != nil {
			t.Fatalf("Export: %v", err)
		}
		members := readZip(t, dest)
		var manifest Manifest
		if err := json.Unmarshal(members[manifestMember], &manifest); err != nil {
			t.Fatal(err)
		}
		for _, g := range manifest.Gaps {
			if g.Source == "transcript" {
				t.Fatalf("skipped review-1 step should not produce a transcript gap: %+v", manifest.Gaps)
			}
		}
	})
}

func TestExportCancellationAbortsWithoutDestination(t *testing.T) {
	root, _ := buildIntactFixture(t)
	dest := filepath.Join(t.TempDir(), "bundle.zip")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Export(ctx, Options{Root: root, RunID: fixtureRunName, Destination: dest})
	if err == nil {
		t.Fatal("Export succeeded against an already-canceled context")
	}
	if _, statErr := os.Lstat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("canceled export created a destination: %v", statErr)
	}
}
