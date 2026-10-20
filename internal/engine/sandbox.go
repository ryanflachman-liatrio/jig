package engine

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/BurntSushi/toml"

	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// SandboxAuthorizer is frozen operator authority for one run epoch. Workflow
// files and ordinary preferences cannot supply it. Implementations own policy
// parsing; engine owns admission and dispatch. Callers pass owned steps/paths.
type SandboxAuthorizer interface {
	Authorize(st *workflow.Step, workspace, storage []string) error
}

// SetSandboxAuthorizer configures subsequent run epochs. Nil retains direct
// Manager's host-only support boundary. Configure before starting runs.
func (m *Manager) SetSandboxAuthorizer(authorizer SandboxAuthorizer) {
	m.sandboxAuthorizer = authorizer
}

// AuthorizeSandboxExecution is shared with concrete runners, including direct
// validation and integration calls. Policy permission never enables Docker.
func AuthorizeSandboxExecution(a SandboxAuthorizer, st *workflow.Step, workspace, storage []string) error {
	if a != nil {
		if err := a.Authorize(st, workspace, storage); err != nil {
			return err
		}
	}
	return checkSandboxStep(st)
}

func cloneSandboxStep(st *workflow.Step) *workflow.Step {
	if st == nil {
		return nil
	}
	owned := *st
	r := st.ResolvedSandbox()
	owned.SnapshotSandbox = &r
	if st.Sandbox != nil {
		ref := *st.Sandbox
		owned.Sandbox = &ref
	}
	owned.Secrets = append([]string(nil), st.Secrets...)
	if st.WorkspaceRequirements != nil {
		owned.WorkspaceRequirements = append([]string{}, st.WorkspaceRequirements...)
	}
	return &owned
}

func (s *scheduler) authorizeSandboxStep(st *workflow.Step) error {
	if s.policyBlock != nil {
		return *s.policyBlock
	}
	return AuthorizeSandboxExecution(s.sandboxAuthorizer, cloneSandboxStep(st), []string{s.repoRoot}, []string{s.jigRoot, s.runDir})
}

// SandboxRuntimeError is a support decision, never a host fallback. Policy
// admission will precede this permanent guard when operator policy is wired.
type SandboxRuntimeError struct{ StepID string }

func (e *SandboxRuntimeError) Error() string {
	return fmt.Sprintf("sandbox_runtime_unsupported: step %q requests Docker execution", e.StepID)
}

func checkSandboxStep(st *workflow.Step) error {
	if st == nil {
		return workflow.CaptureIntegrity
	}
	if st.SnapshotSandbox == nil && st.Sandbox != nil && *st.Sandbox != "host" {
		return workflow.CaptureIntegrity
	}
	r := st.ResolvedSandbox()
	if st.Sandbox != nil && *st.Sandbox != r.Reference {
		return workflow.CaptureIntegrity
	}
	if err := workflow.ValidateSandboxRequest(r); err != nil {
		return workflow.CaptureIntegrity
	}
	if r.Profile != nil {
		return &SandboxRuntimeError{StepID: st.ID}
	}
	return nil
}
func checkSandboxWorkflow(wf *workflow.Workflow) error {
	for i := range wf.Steps {
		if wf.Defaults.Sandbox != nil && *wf.Defaults.Sandbox != "host" && wf.Steps[i].SnapshotSandbox == nil {
			return workflow.CaptureIntegrity
		}
		if err := checkSandboxStep(&wf.Steps[i]); err != nil {
			return err
		}
	}
	return nil
}

// sandboxSnapshot covers every expanded template; runtime fan-out children copy
// the template's locked request. Its digest is also covered by the outer record.
type sandboxSnapshot struct {
	Policy   *SandboxPolicyState                `json:"policy,omitempty"`
	Version  int                                `json:"version"`
	Requests map[string]workflow.SandboxRequest `json:"requests"`
	SHA256   string                             `json:"sha256"`
}

func captureSandboxes(steps []workflow.Step) (*sandboxSnapshot, error) {
	b := &sandboxSnapshot{Version: 1, Requests: make(map[string]workflow.SandboxRequest, len(steps))}
	for i := range steps {
		st := &steps[i]
		if _, exists := b.Requests[st.ID]; exists || st.ID == "" {
			return nil, workflow.CaptureIntegrity
		}
		if st.SnapshotSandbox == nil && st.Sandbox != nil && *st.Sandbox != "host" {
			return nil, workflow.CaptureIntegrity
		}
		r := st.ResolvedSandbox()
		if err := workflow.ValidateSandboxRequest(r); err != nil {
			return nil, workflow.CaptureIntegrity
		}
		b.Requests[st.ID] = r
	}
	raw, err := json.Marshal(b.Requests)
	if err != nil {
		return nil, err
	}
	b.SHA256 = authoringDigest(raw)
	return b, nil
}

func restoreSandboxes(snap *workflowSnapshot) error {
	if snap.SandboxVersion == 0 && snap.Sandbox == nil {
		// Absence of the new section alone is insufficient. Inspect all supplied
		// locked documents, public steps and expanded steps without opening files.
		documents := []string{snap.TOML}
		for _, src := range snap.ModuleSources {
			documents = append(documents, src.TOML)
		}
		for _, data := range documents {
			var doc struct {
				Sandboxes []toml.Primitive `toml:"sandbox"`
				Defaults  struct {
					Sandbox *string `toml:"sandbox"`
				} `toml:"defaults"`
				Steps []struct {
					Sandbox *string `toml:"sandbox"`
				} `toml:"step"`
			}
			md, err := toml.Decode(data, &doc)
			if err != nil {
				return workflow.CaptureIntegrity
			}
			if md.IsDefined("sandbox") || len(doc.Sandboxes) != 0 || doc.Defaults.Sandbox != nil {
				return workflow.CaptureIntegrity
			}
			for _, st := range doc.Steps {
				if st.Sandbox != nil {
					return workflow.CaptureIntegrity
				}
			}
		}
		if snap.Defaults.Sandbox != nil {
			return workflow.CaptureIntegrity
		}
		for _, steps := range [][]workflow.Step{snap.PublicSteps, snap.ExpandedSteps} {
			for i := range steps {
				st := &steps[i]
				if st.Sandbox != nil || st.SnapshotSandbox != nil && st.SnapshotSandbox.Profile != nil {
					return workflow.CaptureIntegrity
				}
				r := workflow.SandboxRequest{Reference: "host", Origin: "implicit"}
				st.SnapshotSandbox = &r
			}
		}
		return nil
	}
	if snap.Sandbox == nil || (snap.SandboxVersion != 1 && snap.SandboxVersion != 2) || snap.Sandbox.Version != snap.SandboxVersion || len(snap.Sandbox.Requests) != len(snap.ExpandedSteps) {
		return workflow.CaptureIntegrity
	}
	raw, err := json.Marshal(snap.Sandbox.Requests)
	digest := authoringDigest(raw)
	if snap.SandboxVersion == 2 {
		digest = sandboxSnapshotDigest(snap.Sandbox)
	}
	if err != nil || snap.Sandbox.SHA256 != digest {
		return workflow.CaptureIntegrity
	}
	sources := map[string]string{snap.SourcePath: snap.SHA256}
	for _, src := range snap.ModuleSources {
		sources[src.Path] = src.SHA256
	}
	seen := map[string]bool{}
	for i := range snap.ExpandedSteps {
		st := &snap.ExpandedSteps[i]
		r, ok := snap.Sandbox.Requests[st.ID]
		if !ok || seen[st.ID] || workflow.ValidateSandboxRequest(r) != nil {
			return workflow.CaptureIntegrity
		}
		if r.SourceSHA256 != "" && sources[r.Source] != r.SourceSHA256 {
			return workflow.CaptureIntegrity
		}
		seen[st.ID] = true
		if st.SnapshotSandbox != nil && !reflect.DeepEqual(r, *st.SnapshotSandbox) {
			return workflow.CaptureIntegrity
		}
		owned := r.Clone()
		st.SnapshotSandbox = &owned
	}
	if snap.SandboxVersion == 2 {
		return validateSandboxState(snap.Sandbox.Policy, snap.ExpandedSteps)
	}
	if snap.Sandbox.Policy != nil {
		return workflow.CaptureIntegrity
	}
	return nil
}
