package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// Resolve only accepted run-owned artifacts. Bytes are verified and copied by
// the worker before create; the scheduler never reads a large binary or falls
// back to interpreting a failed file read as literal input.
func (s *scheduler) resolveContainerInputs(st *workflow.Step) error {
	if len(st.Inputs) == 0 {
		return nil
	}
	if s.runDir == "" || !filepath.IsAbs(s.runDir) {
		return fmt.Errorf("container file inputs require durable artifact ownership")
	}
	if len(st.Inputs) > step.ContainerInputCount {
		return fmt.Errorf("container input count limit exceeded")
	}
	inputs := make([]ResolvedInput, 0, len(st.Inputs))
	aliases := map[string]bool{}
	var total int64
	for _, in := range st.Inputs {
		alias := workflow.InputEnvironmentName(in.As)
		if in.Ref == "" || strings.TrimSpace(in.As) == "" || len(in.As) > 128 || in.Inline || in.From != "" || in.Path != "" || in.Artifact != "" || len(in.RefField) != 0 || in.Once || aliases[alias] {
			return fmt.Errorf("invalid container file input binding")
		}
		if st.ForEach != nil && alias == workflow.InputEnvironmentName(st.ForEach.As) {
			return fmt.Errorf("container input conflicts with foreach binding")
		}
		aliases[alias] = true
		dependency := false
		for _, id := range st.DependsOn {
			if id == in.Ref {
				dependency = true
			}
		}
		producer := s.stepByID(in.Ref)
		state := s.states[in.Ref]
		if !dependency || producer == nil || producer.Type != workflow.StepCommand || producer.Output == "" || producer.ForEach != nil || producer.Schema != nil || producer.OutputType.Kind != workflow.OutputText || state == nil || state.Status != step.StatusSucceeded || state.Result == nil || state.Result.Status != step.StatusSucceeded || state.Result.ExecutionUncertain {
			return fmt.Errorf("container input requires an accepted command output")
		}
		result := state.Result
		if err := result.OutputFile.Validate(); err != nil {
			return err
		}
		total += result.OutputFile.Size
		if total > step.ContainerInputTotalBytes {
			return fmt.Errorf("container input aggregate byte limit exceeded")
		}
		root := filepath.Join(s.runDir, "artifacts")
		relative, err := filepath.Rel(root, result.OutputPath)
		if err != nil || !filepath.IsAbs(result.OutputPath) || !filepath.IsLocal(relative) || relative == "." {
			return fmt.Errorf("container input is outside run artifact storage")
		}
		inputs = append(inputs, ResolvedInput{Ref: in, Value: result.OutputPath, SnapshotPath: result.OutputPath, SHA256: result.OutputFile.SHA256, File: result.OutputFile})
	}
	s.preResolvedInputs[st.ID] = inputs
	return nil
}

// The artifact itself is already a publication snapshot. Persist only bounded
// metadata here; workers own the private input copy and its digest verification.
func (s *scheduler) snapshotContainerInputs(id string, inputs []ResolvedInput) error {
	if len(inputs) == 0 {
		return nil
	}
	if s.runDir == "" {
		return fmt.Errorf("container file inputs require durable artifact ownership")
	}
	if len(inputs) > step.ContainerInputCount {
		return fmt.Errorf("container input count limit exceeded")
	}
	snapshots := make([]inputSnapshot, 0, len(inputs))
	for _, input := range inputs {
		if err := input.File.Validate(); err != nil {
			return err
		}
		snapshots = append(snapshots, inputSnapshot{Label: input.Ref.String(), SHA256: input.SHA256, Path: input.SnapshotPath, File: input.File})
	}
	dir, err := datastore.StepDir(s.runDir, id)
	if err != nil {
		return err
	}
	state := s.states[id]
	data, err := json.MarshalIndent(snapshots, "", "  ")
	if err != nil {
		return err
	}
	name := fmt.Sprintf("inputs-generation-%03d-iteration-%03d-attempt-%03d.json", state.Generation, state.Iteration, state.Attempt)
	return os.WriteFile(filepath.Join(dir, name), data, 0600)
}
