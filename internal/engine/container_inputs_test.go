package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func d05InputScheduler(t *testing.T) (*scheduler, *workflow.Step) {
	t.Helper()
	wf, err := workflow.Load(filepath.Join("..", "..", "examples", "docker-build-jig.toml"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	s := &scheduler{wf: wf, runDir: root, states: map[string]*step.State{
		"build_jig":              {Status: step.StatusSucceeded, Result: &step.Result{Status: step.StatusSucceeded, OutputPath: filepath.Join(root, "artifacts", "binary"), OutputFile: step.FileArtifact{SHA256: strings.Repeat("a", 64), Size: 5, Mode: 0555}}},
		"verify_returned_binary": {},
	}, preResolvedInputs: map[string][]ResolvedInput{}}
	return s, s.stepByID("verify_returned_binary")
}
func TestD05ContainerInputResolution(t *testing.T) {
	s, consumer := d05InputScheduler(t)
	// No file exists: resolution must transfer accepted metadata without blocking
	// the scheduler on file reads. The worker verifies bytes before any create.
	if err := s.resolveAllInputs(consumer, ""); err != nil {
		t.Fatal(err)
	}
	inputs := s.preResolvedInputs[consumer.ID]
	if len(inputs) != 1 || inputs[0].File.Mode != 0555 || inputs[0].SHA256 != strings.Repeat("a", 64) {
		t.Fatal(inputs)
	}
	if err := s.snapshotInputs(consumer.ID, inputs); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(s.runDir, "steps", consumer.ID, "inputs-*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("manifest: %v %v", paths, err)
	}
	raw, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var records []inputSnapshot
	if err := json.Unmarshal(raw, &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].File != inputs[0].File {
		t.Fatal(records)
	}
	s.states["build_jig"].Result.OutputFile.Mode = 0444
	if inputs[0].File.Mode != 0555 {
		t.Fatal("metadata aliases producer result")
	}
}
func TestD05ContainerInputResolutionRejects(t *testing.T) {
	for _, name := range []string{"no_storage", "missing_result", "failed", "uncertain", "no_digest", "bad_mode", "too_large", "outside", "relative", "no_dependency", "inline", "no_alias", "alias_collision", "aggregate"} {
		t.Run(name, func(t *testing.T) {
			s, consumer := d05InputScheduler(t)
			producer := s.states["build_jig"]
			switch name {
			case "no_storage":
				s.runDir = ""
			case "missing_result":
				producer.Result = nil
			case "failed":
				producer.Status = step.StatusFailed
			case "uncertain":
				producer.Result.ExecutionUncertain = true
			case "no_digest":
				producer.Result.OutputFile.SHA256 = ""
			case "bad_mode":
				producer.Result.OutputFile.Mode = 04755
			case "too_large":
				producer.Result.OutputFile.Size = step.ContainerInputFileBytes + 1
			case "outside":
				producer.Result.OutputPath = filepath.Join(t.TempDir(), "binary")
			case "relative":
				producer.Result.OutputPath = "binary"
			case "no_dependency":
				consumer.DependsOn = nil
			case "inline":
				consumer.Inputs[0].Inline = true
			case "no_alias":
				consumer.Inputs[0].As = ""
			case "alias_collision":
				consumer.Inputs = append(consumer.Inputs, consumer.Inputs[0])
			case "aggregate":
				producer.Result.OutputFile.Size = step.ContainerInputFileBytes
				for _, alias := range []string{"two", "three"} {
					in := consumer.Inputs[0]
					in.As = alias
					consumer.Inputs = append(consumer.Inputs, in)
				}
			}
			if err := s.resolveAllInputs(consumer, ""); err == nil {
				t.Fatal("accepted invalid input")
			}
			if len(s.preResolvedInputs[consumer.ID]) != 0 {
				t.Fatal("partially published resolution")
			}
		})
	}
}
