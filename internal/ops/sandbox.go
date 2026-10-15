package ops

import (
	"errors"
	"path/filepath"

	"github.com/ryanflachman-liatrio/jig/internal/sandbox"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func checkSandboxPolicy(opts DoctorOptions, wf *workflow.Workflow) ([]Check, bool) {
	checks := []Check{{ID: "sandbox.scope", Status: CheckWarn, Scope: "auxiliary_host", Message: "Workflow workloads, executable validators, and integration resolution are policy checked. Help and Tier-2 services run on the host outside this boundary."}}
	permitted := true
	addFailure := func(err error) {
		permitted = false
		check := Check{ID: "sandbox.policy", Status: CheckFail, Scope: "operator", Message: "sandbox_policy_unavailable"}
		var f *sandbox.Failure
		if errors.As(err, &f) {
			check.Scope = sandbox.Display(f.StepID)
			check.Message = f.Error()
		}
		checks = append(checks, check)
	}
	if opts.SandboxPolicyError != nil {
		addFailure(opts.SandboxPolicyError)
		return checks, false
	}
	p := opts.SandboxPolicy
	if err := p.CheckGuarantees(); err != nil {
		addFailure(err)
		return checks, false
	}
	source := sandbox.Source{Kind: "builtin"}
	if p != nil {
		source = p.Source()
	}
	checks = append(checks, Check{ID: "sandbox.policy.source", Status: CheckPass, Scope: source.Kind, Message: sandbox.Display(source.Path)})
	var workspace, storage []string
	if opts.Cwd != "" {
		abs, err := filepath.Abs(opts.Cwd)
		if err == nil {
			workspace = append(workspace, abs)
		}
	}
	if opts.Root != "" {
		abs, err := filepath.Abs(opts.Root)
		if err == nil {
			storage = append(storage, abs)
			workspace = append(workspace, filepath.Dir(abs))
		}
	}
	if wf != nil {
		if path, _ := wf.Source(); path != "" {
			workspace = append(workspace, filepath.Dir(path))
		}
		for _, src := range wf.ModuleSources() {
			workspace = append(workspace, filepath.Dir(src.Path))
		}
		for i := range wf.Steps {
			st := &wf.Steps[i]
			if err := p.Authorize(st, workspace, storage); err != nil {
				addFailure(err)
			} else {
				checks = append(checks, Check{ID: "sandbox.policy", Status: CheckPass, Scope: sandbox.Display(st.ID), Message: "requested sandbox permitted by operator policy"})
			}
		}
	}
	return checks, permitted
}

func checkSandboxEndpoint(opts DoctorOptions, wf *workflow.Workflow) []Check {
	eligible := opts.EndpointInput.Explicit() || wf == nil && opts.SandboxPolicy.HasDocker()
	if wf != nil {
		for i := range wf.Steps {
			if wf.Steps[i].ResolvedSandbox().Profile != nil {
				eligible = true
				break
			}
		}
	}
	if !eligible {
		return nil
	}
	endpoint, err := sandbox.SelectEndpoint(opts.EndpointInput, opts.SandboxPolicy)
	if err != nil {
		return []Check{{ID: "sandbox.endpoint", Status: CheckFail, Scope: "deployment", Message: err.Error()}}
	}
	summary := endpoint.Summary()
	return []Check{
		{ID: "sandbox.endpoint", Status: CheckPass, Scope: summary.EndpointSource, Message: summary.URI},
		{ID: "sandbox.endpoint.context", Status: CheckPass, Scope: summary.ContextSource, Message: summary.Context},
		{ID: "sandbox.endpoint.config", Status: CheckPass, Scope: summary.ConfigSource, Message: summary.ConfigRoot},
		{ID: "sandbox.capability", Status: CheckWarn, Scope: "deployment", Message: "Daemon identity, API, platform and enforcement remain unverified; ambient TLS, auth, headers, API and platform overrides are not applied."},
	}
}
