package runner

import (
	"bytes"
	"context"
	"io"
	"strings"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// validateContainerOutput evaluates host predicates only after acknowledged
// container termination. The runtime owns executable validation in a fresh
// container; this helper neither launches it nor treats it as satisfied.
func validateContainerOutput(ctx context.Context, req engine.StepRequest, w *containerWorkspace) (bool, string, error) {
	if err := ctx.Err(); err != nil {
		return false, "validation cancelled", err
	}
	v := req.Step.Validate
	if v == nil || (!v.OutputExists && v.OutputContains == "" && v.OutputSchema == "") {
		return true, "output predicates passed", nil
	}
	file, info, err := openContainerOutput(w, req.Step.Output)
	if err != nil {
		return false, "validation output unavailable", nil
	}
	defer file.Close()
	if info.Size() > step.ContainerInputFileBytes {
		return false, "validation output exceeds binary limit", nil
	}
	// Existence is a binary-safe descriptor check, not a text/JSON read.
	if v.OutputContains == "" && v.OutputSchema == "" {
		return true, "output predicates passed", nil
	}
	if info.Size() > validationOutputLimit {
		return false, "text validation output exceeds 8 MiB", nil
	}
	data, err := io.ReadAll(io.LimitReader(file, validationOutputLimit+1))
	if err != nil || len(data) > validationOutputLimit || int64(len(data)) != info.Size() {
		return false, "validation output read failed or changed", nil
	}
	if err := ctx.Err(); err != nil {
		return false, "validation cancelled", err
	}
	if v.OutputContains != "" && !strings.Contains(string(data), v.OutputContains) {
		return false, "output_contains gate failed", nil
	}
	if v.OutputSchema != "" {
		raw, err := req.Assets.Bytes(req.AssetOwner, workflow.AssetValidationSchema)
		if err != nil {
			return false, "validation schema capture unavailable", err
		}
		schema, err := workflow.CompileValidationSchema(raw)
		if err != nil {
			return false, "validation schema invalid", err
		}
		value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil || schema.Validate(value) != nil {
			return false, "output_schema gate failed", nil
		}
	}
	if err := ctx.Err(); err != nil {
		return false, "validation cancelled", err
	}
	return true, "output predicates passed", nil
}
