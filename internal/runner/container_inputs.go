package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// stageInputs consumes engine snapshots, never unresolved producer paths. The
// runtime must mount Inputs read-only at /inputs, separately from Workspace.
// A digest is mandatory even without persistence. Failure leaves only private
// partial files, which the workspace preparer's cleanup owns.
func (w *containerWorkspace) stageInputs(ctx context.Context, req engine.StepRequest) error {
	if len(req.Inputs) > step.ContainerInputCount || len(req.Inputs) != len(req.Step.Inputs) {
		return fmt.Errorf("container input count mismatch or limit exceeded")
	}
	w.InputEnv = make(map[string]string, len(req.Inputs))
	remaining := step.ContainerInputTotalBytes
	for i, input := range req.Inputs {
		declared := req.Step.Inputs[i]
		ref := input.Ref
		alias := workflow.InputEnvironmentName(ref.As)
		key := "JIG_INPUT_" + alias
		if ref.Ref != declared.Ref || ref.As != declared.As || ref.Ref == "" || strings.TrimSpace(ref.As) == "" || len(ref.As) > 128 || ref.Inline || ref.From != "" || ref.Path != "" || ref.Artifact != "" || len(ref.RefField) != 0 || ref.Once {
			return fmt.Errorf("invalid container file input binding")
		}
		if _, exists := w.InputEnv[key]; exists {
			return fmt.Errorf("duplicate container input environment binding")
		}
		if req.Step.ForEach != nil && alias == workflow.InputEnvironmentName(req.Step.ForEach.As) {
			return fmt.Errorf("container input conflicts with foreach binding")
		}
		if !filepath.IsAbs(input.SnapshotPath) || input.Value != input.SnapshotPath {
			return fmt.Errorf("container input requires an engine file snapshot")
		}
		if err := input.File.Validate(); err != nil || input.SHA256 != input.File.SHA256 {
			return fmt.Errorf("container input requires published file metadata")
		}

		name := fmt.Sprintf("%03d", i+1)
		n, err := copyContainerInput(ctx, req.ArtifactDir, input, filepath.Join(w.Inputs, name), remaining)
		if err != nil {
			// Do not disclose host paths from filesystem errors in runtime output.
			return fmt.Errorf("container input %d copy or integrity check failed", i+1)
		}
		remaining -= n
		w.InputEnv[key] = "/inputs/" + name
	}
	return nil
}

func copyContainerInput(ctx context.Context, artifactRoot string, input engine.ResolvedInput, target string, remaining int64) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := input.File.Validate(); err != nil {
		return 0, err
	}
	if !filepath.IsAbs(artifactRoot) {
		return 0, fmt.Errorf("missing artifact root")
	}
	relative, err := filepath.Rel(artifactRoot, input.SnapshotPath)
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return 0, fmt.Errorf("input outside artifact root")
	}
	root, err := os.OpenRoot(artifactRoot)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	before, err := root.Lstat(relative)
	if err != nil {
		return 0, err
	}
	if !before.Mode().IsRegular() {
		return 0, fmt.Errorf("input is not regular")
	}
	source, err := openContainerInput(root, relative)
	if err != nil {
		return 0, err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) || info.Size() != input.File.Size || info.Size() > step.ContainerInputFileBytes || info.Size() > remaining {
		return 0, fmt.Errorf("input changed or exceeds byte limit")
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return 0, err
	}
	h := sha256.New()
	// LimitReader detects growth without allowing it to consume the disk budget.
	r := io.LimitReader(source, info.Size()+1)
	buf := make([]byte, 32<<10)
	var total int64
	for {
		if err = ctx.Err(); err != nil {
			break
		}
		var n int
		n, err = r.Read(buf)
		if total+int64(n) > info.Size() {
			err = fmt.Errorf("input grew")
			break
		}
		if n > 0 {
			var writeErr error
			_, writeErr = io.MultiWriter(out, h).Write(buf[:n])
			total += int64(n)
			if writeErr != nil {
				err = writeErr
				break
			}
		}
		if err == io.EOF {
			err = nil
			break
		}
		if err != nil {
			break
		}
	}
	if err == nil && (total != info.Size() || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), input.File.SHA256)) {
		err = fmt.Errorf("input digest or size changed")
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		mode := os.FileMode(input.File.Mode)
		err = out.Chmod(mode)
	}
	return total, errors.Join(err, out.Close())
}
