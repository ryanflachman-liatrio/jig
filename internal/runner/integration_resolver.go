package runner

import (
	"context"
	"strings"

	"jig/internal/engine"
	"jig/internal/harness"
	"jig/internal/step"
	"jig/internal/workflow"
)

// IntegrationResolver runs an operator-requested resolution through the same
// backend selection as the conflicted agent step. The engine owns staging and
// committing, so the resolver is instructed to leave a reviewable worktree.
type IntegrationResolver struct{ agent *AgentExecutor }

func NewIntegrationResolver(forHarness func(backend, transport string) (harness.Harness, error)) *IntegrationResolver {
	return &IntegrationResolver{agent: NewAgentExecutor(forHarness)}
}

func (r *IntegrationResolver) ResolveIntegration(ctx context.Context, req engine.IntegrationResolutionRequest, rep engine.Reporter) (*step.Result, error) {
	if req.Step == nil {
		return &step.Result{Status: step.StatusFailed, Err: "missing conflicted step"}, nil
	}
	resolverStep := *req.Step
	resolverStep.Type = workflow.StepAgent
	resolverStep.Isolation = workflow.IsolationNone
	resolverStep.BlockOn = ""
	resolverStep.Schema = nil
	resolverStep.SchemaFile = ""
	resolverStep.AllowedTools = []string{"Read", "Grep", "Glob", "Write", "Edit", "Bash"}
	resolverStep.DisallowedTools = nil
	resolverStep.AppendSystemPrompt = strings.TrimSpace(resolverStep.AppendSystemPrompt + "\n\n" + integrationResolutionPrompt(req.Conflicts))

	return r.agent.Execute(ctx, engine.StepRequest{
		RunID: req.RunID, Step: &resolverStep, Worktree: req.Worktree, ExecutionDir: req.Worktree,
	}, rep)
}

func integrationResolutionPrompt(paths []string) string {
	return "You are resolving an integration conflict in the current worktree. Resolve only these conflicted paths: " + strings.Join(paths, ", ") + ". Preserve both intended changes when compatible. Do not commit, reset, rebase, or alter workflow state. Remove all conflict markers, run focused checks when practical, and leave the resolution staged for human review."
}
