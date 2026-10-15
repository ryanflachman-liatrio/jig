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

// SandboxReport separates permission, observations and execution support. Raw
// authored profiles (including env), policy data and SDK replies never enter it.
type SandboxReport struct {
	SchemaVersion    int                 `json:"schema_version"`
	Structure        string              `json:"structure"`
	Policy           string              `json:"policy"`
	Endpoint         string              `json:"endpoint"`
	Capabilities     string              `json:"capabilities"`
	ExecutionSupport string              `json:"execution_support"`
	Source           sandbox.Source      `json:"source"`
	Fingerprint      string              `json:"policy_fingerprint,omitempty"`
	Inspection       *sandbox.Inspection `json:"inspection,omitempty"`
}

func newSandboxReport(opts DoctorOptions, wf *workflow.Workflow, permitted bool) *SandboxReport {
	report := &SandboxReport{SchemaVersion: 1, Structure: "unavailable", Policy: "fail", Endpoint: "unavailable", Capabilities: "unavailable", ExecutionSupport: "unavailable", Source: sandbox.Source{Kind: "builtin"}}
	if wf != nil {
		report.Structure = "pass"
		report.ExecutionSupport = "host"
		for i := range wf.Steps {
			if wf.Steps[i].ResolvedSandbox().Profile != nil {
				report.ExecutionSupport = "sandbox_runtime_unsupported"
			}
		}
	}
	if permitted {
		report.Policy = "pass"
	}
	if opts.SandboxPolicy != nil {
		report.Source = opts.SandboxPolicy.Source()
		report.Source.Path = sandbox.Display(report.Source.Path)
		report.Fingerprint = opts.SandboxPolicy.Fingerprint()
	}
	return report
}
func inspectSandboxDeployment(opts DoctorOptions, wf *workflow.Workflow, report *SandboxReport) []Check {
	eligible := opts.EndpointInput.Explicit() || wf == nil && opts.SandboxPolicy.HasDocker()
	var steps []workflow.Step
	if wf != nil {
		steps = wf.Steps
		for i := range steps {
			if steps[i].ResolvedSandbox().Profile != nil {
				eligible = true
			}
		}
	}
	if !eligible {
		return nil
	}
	endpoint, err := sandbox.SelectEndpoint(opts.EndpointInput, opts.SandboxPolicy)
	if err != nil {
		report.Endpoint = "fail"
		return []Check{{ID: "sandbox.endpoint", Status: CheckFail, Scope: "deployment", Message: err.Error()}}
	}
	report.Endpoint = "pass"
	summary := endpoint.Summary()
	checks := []Check{{ID: "sandbox.endpoint", Status: CheckPass, Scope: summary.EndpointSource, Message: summary.URI}, {ID: "sandbox.endpoint.context", Status: CheckPass, Scope: summary.ContextSource, Message: summary.Context}, {ID: "sandbox.endpoint.config", Status: CheckPass, Scope: summary.ConfigSource, Message: summary.ConfigRoot}}
	observed, err := sandbox.Inspect(opts.Context, endpoint, opts.SandboxPolicy, steps, nil)
	report.Inspection = &observed
	if err != nil {
		report.Capabilities = "fail"
		return append(checks, Check{ID: "sandbox.capability", Status: CheckFail, Scope: "deployment", Message: err.Error()})
	}
	report.Capabilities = "unverified"
	status := CheckWarn
	if observed.Capabilities == nil || !observed.Capabilities.Ready {
		status = CheckFail
	}
	checks = append(checks, Check{ID: "sandbox.capability", Status: status, Scope: "deployment", Message: "reported compute capabilities are metadata only; enforcement, UID/GID access, Desktop sharing, storage and emulation remain unverified"})
	for _, st := range observed.Steps {
		if st.Reason != "" {
			checks = append(checks, Check{ID: "sandbox.capability.platform", Status: CheckFail, Scope: st.StepID, Message: st.Reason})
		}
	}
	for _, image := range observed.Images {
		status := CheckWarn
		if image.Status == "cache_required_missing" || image.Status == "unavailable" || image.Status == "metadata_unavailable" {
			status = CheckFail
		}
		checks = append(checks, Check{ID: "sandbox.capability.image", Status: status, Scope: image.Reference, Message: image.Status})
	}
	return checks
}
