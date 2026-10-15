package runner

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/step"
)

// writeAgentOutputs publishes derived artifacts from finalized, redacted
// content. OutputPath is set only after the deliverable is successfully saved.
func writeAgentOutputs(req engine.StepRequest, result *step.Result, text string) error {
	if req.TranscriptPath == "" {
		return nil
	}
	output := agentOutputMarkdown(text, result.Structured)
	metadata := metadataToMarkdown(result.Structured)
	dir := filepath.Dir(req.TranscriptPath)
	for _, artifact := range []struct {
		name string
		data []byte
	}{
		{"output.json", result.Structured},
		{"metadata.md", []byte(metadata)},
		{"output.md", []byte(output)},
	} {
		path := filepath.Join(dir, artifact.name)
		if len(artifact.data) == 0 {
			// A later attempt must not expose an earlier attempt's output.
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("clear agent %s: %w", artifact.name, err)
			}
		} else if err := os.WriteFile(path, artifact.data, 0o644); err != nil {
			return fmt.Errorf("write agent %s: %w", artifact.name, err)
		}
	}
	if output != "" {
		if req.Step.Output != "" {
			path, err := explicitOutputPath(req)
			if err != nil {
				return fmt.Errorf("resolve declared output: %w", err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return fmt.Errorf("create declared output directory: %w", err)
			}
			if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
				return fmt.Errorf("write declared output: %w", err)
			}
		}
		result.OutputPath = filepath.Join(dir, "output.md")
	}
	writeAgentAttemptArtifacts(req, result.Structured, output, metadata)
	return nil
}
