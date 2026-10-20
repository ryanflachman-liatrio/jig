package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

type workflowSnapshot struct {
	SourcePath    string                  `json:"source_path,omitempty"`
	BaseDir       string                  `json:"base_dir,omitempty"`
	SHA256        string                  `json:"sha256"`
	TOML          string                  `json:"toml"`
	ModuleSources []workflow.ModuleSource `json:"module_sources,omitempty"`
	Meta          workflow.Meta           `json:"meta"`
	Defaults      workflow.Defaults       `json:"defaults"`
	Telemetry     workflow.Telemetry      `json:"telemetry,omitempty"`
	PublicSteps   []workflow.Step         `json:"public_steps,omitempty"`
	ExpandedSteps []workflow.Step         `json:"expanded_steps,omitempty"`
	// Notification is the run's frozen resolved notification policy — the
	// list of events and destination aliases that were in effect when the
	// run started. It is secret-free: aliases only, no URL, bearer, or
	// operator enablement. Absent means notification-disabled (older
	// snapshots), which is not the same as an explicit disabled policy but
	// the observable behavior is identical.
	Notification       *workflow.NotificationPolicy `json:"notification,omitempty"`
	NotificationSHA256 string                       `json:"notification_sha256,omitempty"`
}

func persistWorkflowSnapshot(runDir datastore.Run, wf *workflow.Workflow) error {
	if !runDir.Enabled() || wf == nil {
		return nil
	}
	path, source := wf.Source()
	if source == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(source))
	snap := workflowSnapshot{
		SourcePath:    path,
		BaseDir:       filepath.Dir(path),
		SHA256:        hex.EncodeToString(sum[:]),
		TOML:          source,
		ModuleSources: wf.ModuleSources(),
		Meta:          wf.Meta,
		Defaults:      wf.Defaults,
		Telemetry:     wf.Telemetry,
		PublicSteps:   wf.PublicSteps(),
		ExpandedSteps: wf.Steps,
	}
	policy := wf.NotificationPolicy()
	if len(policy.Events) > 0 || len(policy.Routes) > 0 {
		snap.Notification = &policy
		hash, err := notificationPolicyDigest(policy)
		if err == nil {
			snap.NotificationSHA256 = hash
		}
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(runDir.WorkflowSnapshot(), data, 0o644)
}

func loadWorkflowSnapshot(runDir datastore.Run) (*workflow.Workflow, error) {
	data, err := os.ReadFile(runDir.WorkflowSnapshot())
	if err != nil {
		return nil, err
	}
	return DecodeWorkflowSnapshot(data)
}

// LoadWorkflowSnapshot reads and verifies the immutable workflow captured at
// run start. Historical commands must use this instead of current author TOML.
func LoadWorkflowSnapshot(runDir datastore.Run) (*workflow.Workflow, error) {
	return loadWorkflowSnapshot(runDir)
}

// DecodeWorkflowSnapshot verifies and decodes an already-read workflow.json
// payload. It performs no I/O itself, so callers with a confined or bounded
// file handle (e.g. a traversal-resistant os.Root read) can validate a
// snapshot's checksums without going through a path-based loader.
func DecodeWorkflowSnapshot(data []byte) (*workflow.Workflow, error) {
	var snap workflowSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("decode workflow snapshot: %w", err)
	}
	sum := sha256.Sum256([]byte(snap.TOML))
	if hex.EncodeToString(sum[:]) != snap.SHA256 {
		return nil, fmt.Errorf("workflow snapshot checksum mismatch")
	}
	for _, source := range snap.ModuleSources {
		sum := sha256.Sum256([]byte(source.TOML))
		if source.Path == "" || source.SHA256 == "" || hex.EncodeToString(sum[:]) != source.SHA256 {
			return nil, fmt.Errorf("workflow snapshot module checksum mismatch")
		}
	}
	if snap.Notification != nil && snap.NotificationSHA256 != "" {
		want, err := notificationPolicyDigest(*snap.Notification)
		if err != nil || want != snap.NotificationSHA256 {
			return nil, fmt.Errorf("workflow snapshot notification policy checksum mismatch")
		}
	}
	var wf *workflow.Workflow
	if len(snap.ExpandedSteps) > 0 {
		restored, err := workflow.RestoreExpandedWithTelemetry(snap.Meta, snap.Defaults, snap.Telemetry, snap.PublicSteps, snap.ExpandedSteps, snap.ModuleSources)
		if err != nil {
			return nil, err
		}
		wf = restored
	} else {
		// Every snapshot written since the agent schema carries
		// ExpandedSteps. Rebuilding one without them would re-resolve each
		// agent from [defaults] and the profiles on disk, so an agent step
		// here fails closed like a snapshot with no persisted agent.
		decoded, err := workflow.DecodeLocked(snap.TOML, snap.BaseDir, snap.SourcePath, snap.ModuleSources)
		if err != nil {
			return nil, err
		}
		for _, s := range decoded.Steps {
			if s.Type == workflow.StepAgent {
				return nil, fmt.Errorf("workflow snapshot predates the agent schema (step %q); start a new run", s.ID)
			}
		}
		wf = decoded
	}
	// Overwrite the workflow's notification policy with the frozen snapshot
	// value: reopen must never re-read current profile files. When no
	// notification policy was persisted we still call SetResolvedNotificationPolicy
	// with the zero value so a later mutation of the source profile cannot
	// silently promote a legacy run to notification-enabled.
	if snap.Notification != nil {
		wf.SetResolvedNotificationPolicy(*snap.Notification)
	} else {
		wf.SetResolvedNotificationPolicy(workflow.NotificationPolicy{})
	}
	return wf, nil
}

func notificationPolicyDigest(policy workflow.NotificationPolicy) (string, error) {
	data, err := json.Marshal(policy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
