package engine

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// SandboxPolicyState is locked local authority metadata, never an export DTO.
// Requests live in the adjacent snapshot section; effective values are separate
// so a changed default cannot silently resize a reopened workload.
type SandboxPolicyState struct {
	Source      SandboxPolicySource         `json:"source"`
	Fingerprint string                      `json:"fingerprint"`
	Effective   map[string]SandboxEffective `json:"effective"`
	Binding     *SandboxBinding             `json:"binding,omitempty"`
}
type SandboxPolicySource struct {
	Kind string `json:"kind"`
	Path string `json:"path,omitempty"`
}
type SandboxEffective struct {
	NanoCPUs     int64  `json:"nano_cpus,omitempty"`
	MemoryBytes  int64  `json:"memory_bytes,omitempty"`
	PIDs         int64  `json:"pids,omitempty"`
	CPUSource    string `json:"cpu_source,omitempty"`
	MemorySource string `json:"memory_source,omitempty"`
	PIDsSource   string `json:"pids_source,omitempty"`
	Platform     string `json:"platform,omitempty"`
}
type SandboxBinding struct {
	URI            string `json:"uri"`
	Context        string `json:"context,omitempty"`
	ContextSource  string `json:"context_source,omitempty"`
	EndpointSource string `json:"endpoint_source"`
	ConfigSource   string `json:"config_source,omitempty"`
	ConfigRoot     string `json:"config_root,omitempty"`
	DaemonID       string `json:"daemon_id"`
	API            string `json:"api"`
	NativePlatform string `json:"native_platform"`
}

func (p *SandboxPolicyState) Clone() *SandboxPolicyState {
	if p == nil {
		return nil
	}
	c := *p
	c.Effective = make(map[string]SandboxEffective, len(p.Effective))
	for k, v := range p.Effective {
		c.Effective[k] = v
	}
	if p.Binding != nil {
		b := *p.Binding
		c.Binding = &b
	}
	return &c
}
func builtinSandboxState(wf *workflow.Workflow) *SandboxPolicyState {
	p := &SandboxPolicyState{Source: SandboxPolicySource{Kind: "builtin"}, Fingerprint: authoringDigest([]byte("builtin-host-only-v1")), Effective: map[string]SandboxEffective{}}
	for _, st := range wf.Steps {
		p.Effective[st.ID] = SandboxEffective{}
	}
	return p
}
func validateSandboxState(p *SandboxPolicyState, steps []workflow.Step) error {
	if p == nil || len(p.Fingerprint) != 64 || len(p.Effective) != len(steps) {
		return workflow.CaptureIntegrity
	}
	if raw, err := hex.DecodeString(p.Fingerprint); err != nil || len(raw) != 32 {
		return workflow.CaptureIntegrity
	}
	switch p.Source.Kind {
	case "builtin":
		if p.Source.Path != "" {
			return workflow.CaptureIntegrity
		}
	case "explicit", "implicit":
		if !filepath.IsAbs(p.Source.Path) {
			return workflow.CaptureIntegrity
		}
	default:
		return workflow.CaptureIntegrity
	}
	docker := false
	for _, st := range steps {
		e, ok := p.Effective[st.ID]
		if !ok {
			return workflow.CaptureIntegrity
		}
		r := st.ResolvedSandbox()
		if r.Profile == nil {
			if e != (SandboxEffective{}) {
				return workflow.CaptureIntegrity
			}
			continue
		}
		docker = true
		if r.Profile.CPUs != nil && e.NanoCPUs != int64(*r.Profile.CPUs) || r.Profile.Memory != nil && e.MemoryBytes != int64(*r.Profile.Memory) || r.Profile.PIDs != nil && e.PIDs != *r.Profile.PIDs {
			return workflow.CaptureIntegrity
		}
		if e.NanoCPUs <= 0 || e.MemoryBytes < 6<<20 || e.PIDs <= 0 || e.PIDs > math.MaxInt32 {
			return workflow.CaptureIntegrity
		}
		for _, src := range []string{e.CPUSource, e.MemorySource, e.PIDsSource} {
			if src != "authored" && src != "policy_default" {
				return workflow.CaptureIntegrity
			}
		}
		if (r.Profile.CPUs != nil) != (e.CPUSource == "authored") || (r.Profile.Memory != nil) != (e.MemorySource == "authored") || (r.Profile.PIDs != nil) != (e.PIDsSource == "authored") {
			return workflow.CaptureIntegrity
		}
		if e.Platform == "" || !strings.HasPrefix(e.Platform, "linux/") || r.Profile.Platform != "" && string(r.Profile.Platform) != e.Platform {
			return workflow.CaptureIntegrity
		}
	}
	if docker {
		b := p.Binding
		if b == nil || !strings.HasPrefix(b.URI, "unix:///") || b.DaemonID == "" || b.API == "" || !strings.HasPrefix(b.NativePlatform, "linux/") {
			return workflow.CaptureIntegrity
		}
		for _, st := range steps {
			r := st.ResolvedSandbox()
			if r.Profile != nil && r.Profile.Platform == "" && p.Effective[st.ID].Platform != b.NativePlatform {
				return workflow.CaptureIntegrity
			}
		}
	} else if p.Binding != nil {
		return workflow.CaptureIntegrity
	}
	return nil
}

// SandboxEpochLoader is installed by composition. It must honor ctx, evaluate
// every step, preserve locked effective values and return immutable authority.
// Engine does not import the policy implementation or Docker SDK.
type SandboxEpochLoader func(context.Context, *workflow.Workflow, *SandboxPolicyState) (SandboxAuthorizer, *SandboxPolicyState, error)

func (m *Manager) SetSandboxEpochLoader(load SandboxEpochLoader) { m.sandboxEpochLoader = load }
func (m *Manager) loadSandboxEpoch(ctx context.Context, wf *workflow.Workflow, locked *SandboxPolicyState) (SandboxAuthorizer, *SandboxPolicyState, error) {
	a, p := m.sandboxAuthorizer, locked.Clone()
	if p == nil {
		p = builtinSandboxState(wf)
	}
	var err error
	if m.sandboxEpochLoader != nil {
		a, p, err = m.sandboxEpochLoader(ctx, wf, locked.Clone())
		if err != nil {
			return nil, nil, err
		}
	}
	var workspace []string
	if m.root != "" {
		workspace = append(workspace, filepath.Dir(m.root))
	}
	if path, _ := wf.Source(); path != "" {
		workspace = append(workspace, filepath.Dir(path))
	}
	for _, src := range wf.ModuleSources() {
		workspace = append(workspace, filepath.Dir(src.Path))
	}
	for i := range wf.Steps {
		if a != nil {
			if err = a.Authorize(cloneSandboxStep(&wf.Steps[i]), workspace, []string{m.root}); err != nil {
				return nil, nil, err
			}
		}
	}
	if err = checkSandboxWorkflow(wf); err != nil {
		return nil, nil, err
	}
	if err = validateSandboxState(p, wf.Steps); err != nil {
		return nil, nil, err
	}
	return a, p.Clone(), nil
}

// SandboxPolicyBlocked overlays all step parks. Reason is a bounded code, never
// an underlying filesystem, daemon, or policy parser error.
type SandboxPolicyBlocked struct {
	RunID  string
	Reason string
	Epoch  uint64
}

func (SandboxPolicyBlocked) isEvent()                                             {}
func (b SandboxPolicyBlocked) Authorize(*workflow.Step, []string, []string) error { return b }
func (e SandboxPolicyBlocked) Error() string {
	return "sandbox_policy_blocked: " + SandboxPolicyReason(e.Reason)
}

type SandboxPolicyCleared struct {
	RunID string
	Epoch uint64
	// Policy is local durable authority metadata. Public consumers must project it.
	Policy *SandboxPolicyState `json:"policy,omitempty"`
}

func (SandboxPolicyCleared) isEvent() {}
func sandboxReason(err error) string {
	var typed interface{ SandboxReason() string }
	if errors.As(err, &typed) {
		r := typed.SandboxReason()
		if len(r) > 0 && len(r) <= 64 && strings.Trim(r, "abcdefghijklmnopqrstuvwxyz0123456789_") == "" {
			return SandboxPolicyReason(r)
		}
	}
	var unsupported *SandboxRuntimeError
	if errors.As(err, &unsupported) {
		return "sandbox_runtime_unsupported"
	}
	return "sandbox_policy_denied"
}
func clonePolicyBlock(b *SandboxPolicyBlocked) *SandboxPolicyBlocked {
	if b == nil {
		return nil
	}
	c := *b
	return &c
}

// RecheckSandboxPolicy queues one explicit recheck. It never answers an
// underlying human gate. Concurrent requests coalesce while I/O is in flight.
func (r *Run) RecheckSandboxPolicy(epoch uint64) {
	select {
	case r.inbox <- sandboxRecheckMsg{epoch: epoch}:
	case <-r.done:
	}
}

type sandboxRecheckMsg struct {
	epoch uint64
	abort bool
}

func (sandboxRecheckMsg) isSchedMsg() {}
func (m sandboxRecheckMsg) execute(s *scheduler) {
	if s.policyBlock == nil || s.policyBlock.Epoch != m.epoch {
		return
	}
	if m.abort {
		s.cancel()
		return
	}
	if s.policyChecking || s.policyLoad == nil {
		return
	}
	s.policyChecking = true
	epoch := s.policyBlock.Epoch
	// Decode our verified immutable bytes to give the loader a private workflow.
	data, err := encodeWorkflowSnapshot(s.wf, nil, nil)
	if err != nil {
		s.policyChecking = false
		s.failCapture(workflow.CaptureIntegrity)
		return
	}
	wf, err := DecodeWorkflowSnapshot(data)
	if err != nil {
		s.policyChecking = false
		s.failCapture(workflow.CaptureIntegrity)
		return
	}
	locked := s.policyState.Clone()
	ctx, cancel := context.WithTimeout(s.policyContext, 10*time.Second)
	go func() {
		defer cancel()
		a, p, err := s.policyLoad(ctx, wf, locked)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		result := sandboxRecheckResult{epoch: epoch, authorizer: a, state: p.Clone(), err: err}
		select {
		case s.inbox <- result:
		case <-s.policyContext.Done():
		}
	}()
}

type sandboxRecheckResult struct {
	epoch      uint64
	authorizer SandboxAuthorizer
	state      *SandboxPolicyState
	err        error
}

func (sandboxRecheckResult) isSchedMsg() {}
func (m sandboxRecheckResult) execute(s *scheduler) {
	if s.policyBlock == nil || s.policyBlock.Epoch != m.epoch || !s.policyChecking {
		return
	}
	s.policyChecking = false
	if m.err != nil {
		b := SandboxPolicyBlocked{RunID: s.runID, Reason: sandboxReason(m.err), Epoch: m.epoch + 1}
		if s.emit(b) == nil {
			s.policyBlock = &b
			s.sandboxAuthorizer = b
		}
		return
	}
	if validateSandboxState(m.state, s.wf.Steps) != nil {
		s.failCapture(workflow.CaptureIntegrity)
		return
	}
	if s.policyContext != nil && s.policyContext.Err() != nil {
		return
	}
	if s.emit(SandboxPolicyCleared{RunID: s.runID, Epoch: m.epoch, Policy: m.state.Clone()}) != nil {
		return
	}
	s.sandboxAuthorizer = m.authorizer
	s.policyState = m.state.Clone()
	previous := s.policyBlock
	s.policyBlock = nil
	// The grant is durable before any workspace restoration. A restoration
	// failure reinstates the fence without exposing a runnable scheduler.
	if s.policyRestore != nil {
		if err := s.policyRestore(); err != nil {
			previous.Reason = "sandbox_restore_failed"
			previous.Epoch++
			s.policyBlock = previous
			s.sandboxAuthorizer = *previous
			_ = s.emit(*previous)
			return
		}
		s.policyRestore = nil
	}

	for _, e := range s.policyParks {
		s.fanOut(e)
	}
	s.policyParks = nil
}

// sandboxSnapshotDigest covers both request and authority sections.
func sandboxSnapshotDigest(b *sandboxSnapshot) string {
	raw, _ := json.Marshal(struct {
		Requests map[string]workflow.SandboxRequest
		Policy   *SandboxPolicyState
	}{b.Requests, b.Policy})
	return authoringDigest(raw)
}

func (r *Run) AbortSandboxPolicy(epoch uint64) {
	select {
	case r.inbox <- sandboxRecheckMsg{epoch: epoch, abort: true}:
	case <-r.done:
	}
}

// SandboxPolicyReason projects local journal evidence to a closed public code.
func SandboxPolicyReason(reason string) string {
	switch reason {
	case "sandbox_policy_denied", "sandbox_policy_invalid", "sandbox_policy_unavailable", "sandbox_policy_unsafe", "sandbox_request_invalid", "sandbox_runtime_unsupported", "sandbox_authentication_unsupported", "sandbox_guarantee_unsupported", "sandbox_endpoint_changed", "sandbox_endpoint_denied", "sandbox_endpoint_invalid", "sandbox_endpoint_unavailable", "sandbox_endpoint_unsupported", "sandbox_api_unsupported", "sandbox_client_unavailable", "sandbox_daemon_unavailable", "sandbox_daemon_unsupported", "sandbox_daemon_invalid", "sandbox_emulation_denied", "sandbox_emulation_unverified", "sandbox_inspection_cancelled", "sandbox_platform_unavailable", "sandbox_inspection_forbidden", "sandbox_inspection_limit", "sandbox_inspection_redirect", "sandbox_inspection_unbounded", "sandbox_restore_failed":
		return reason
	default:
		return "sandbox_policy_denied"
	}
}

// restoredSandboxPolicy folds only verified grants. Authored/effective and
// inspected bindings remain immutable; source selection and fingerprint may
// change after an explicit operator reselection and successful recheck.
func restoredSandboxPolicy(wf *workflow.Workflow, original *SandboxPolicyState, events []Event) (*SandboxPolicyState, error) {
	locked := original.Clone()
	if locked == nil {
		locked = builtinSandboxState(wf)
	}
	for _, event := range events {
		grant, ok := event.(SandboxPolicyCleared)
		if !ok || grant.Policy == nil {
			continue
		}
		if err := validateSandboxState(grant.Policy, wf.Steps); err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(locked.Effective, grant.Policy.Effective) || !reflect.DeepEqual(locked.Binding, grant.Policy.Binding) {
			return nil, workflow.CaptureIntegrity
		}
		locked = grant.Policy.Clone()
	}
	return locked, nil
}
