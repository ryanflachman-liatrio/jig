package runner

import "github.com/ryanflachman-liatrio/jig/internal/engine"

func authorizeRequest(req engine.StepRequest) error {
	return engine.AuthorizeSandboxExecution(req.SandboxAuthorizer, req.Step,
		[]string{req.RepoRoot, req.ExecutionDir, req.Worktree}, []string{req.ArtifactDir})
}
