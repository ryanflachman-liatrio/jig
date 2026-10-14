package runner

import (
	"bytes"
	"context"
	"io"
	"strings"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const validationOutputLimit = 8 << 20

func (e *CommandExecutor) Validate(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (bool, string, error) {
	return validateAttempt(ctx, req, rep, e.cwd)
}

func (e *AgentExecutor) Validate(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (bool, string, error) {
	return validateAttempt(ctx, req, rep, "")
}

func (e *CheckExecutor) Validate(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (bool, string, error) {
	return e.command.Validate(ctx, req, rep)
}

func (m *Mux) Validate(ctx context.Context, req engine.StepRequest, rep engine.Reporter) (bool, string, error) {
	v, ok := m.executors[req.Step.Type].(engine.Validator)
	if !ok {
		return false, "executor does not support validation", nil
	}
	return v.Validate(ctx, req, rep)
}

func validateAttempt(ctx context.Context, req engine.StepRequest, rep engine.Reporter, fallback string) (bool, string, error) {
	v := req.Step.Validate
	if v == nil {
		return true, "gate passed", nil
	}
	var schema *jsonschema.Schema
	if v.OutputSchema != "" {
		raw, err := req.Assets.Bytes(req.AssetOwner, workflow.AssetValidationSchema)
		if err != nil {
			return false, "validation schema capture unavailable", err
		}
		schema, err = workflow.CompileValidationSchema(raw)
		if err != nil {
			return false, err.Error(), nil
		}
	}
	if err := ctx.Err(); err != nil {
		return false, "validation cancelled", err
	}
	if v.Command != "" {
		copyStep := *req.Step
		copyStep.Type = workflow.StepCommand
		copyStep.Run, copyStep.Script = v.Command, ""
		copyStep.Validate = nil
		validatorReq := req
		validatorReq.Step = &copyStep
		// Transcript writes append, preserving normalized main content and adding
		// validator output under the same execution coordinates.
		res, err := NewCommandExecutor(fallback).Execute(ctx, validatorReq, rep)
		if err != nil {
			return false, "gate command failed", err
		}
		if res.ExecutionUncertain {
			return false, "gate termination uncertain", &engine.ExecutionUncertainError{}
		}
		if err := ctx.Err(); err != nil {
			return false, "validation cancelled", err
		}
		if res.Status != "succeeded" {
			return false, "gate command failed: " + res.Err, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return false, "validation cancelled", err
	}
	if v.OutputExists || v.OutputContains != "" || schema != nil {
		root := req.ExecutionDir
		if root == "" {
			root = fallback
		}
		path, err := workflow.ExecutionPath(root, req.Step.Output)
		if err != nil {
			return false, "resolve validation output: " + err.Error(), nil
		}
		f, err := openValidationOutput(path)
		if err != nil {
			return false, "validation output unavailable", nil
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > validationOutputLimit {
			return false, "validation output must be a regular file at most 8 MiB", nil
		}
		data, err := io.ReadAll(io.LimitReader(f, validationOutputLimit+1))
		if err != nil || len(data) > validationOutputLimit {
			return false, "validation output read failed or exceeded 8 MiB", nil
		}
		if v.OutputContains != "" && !strings.Contains(string(data), v.OutputContains) {
			return false, "output_contains gate failed", nil
		}
		if schema != nil {
			value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
			if err != nil || schema.Validate(value) != nil {
				return false, "output_schema gate failed", nil
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return false, "validation cancelled", err
	}
	return true, "gate passed", nil
}
