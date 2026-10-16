package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

type authoringReference struct {
	Version  int    `json:"version"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Bindings int    `json:"bindings"`
	Blobs    int    `json:"blobs"`
	Bytes    int    `json:"bytes"`
}
type runAuthoring struct {
	policy   *SandboxPolicyState
	assets   *workflow.AuthoringAssets
	manifest []byte
	snapshot []byte
}

func newRunAuthoring(assets *workflow.AuthoringAssets) (*runAuthoring, error) {
	if err := assets.Verify(""); err != nil {
		return nil, err
	}
	manifest, err := json.Marshal(assets.Manifest())
	if err != nil {
		return nil, workflow.CaptureIntegrity
	}
	return &runAuthoring{assets: assets, manifest: manifest}, nil
}
func (a *runAuthoring) reference() *authoringReference {
	m := a.assets.Manifest()
	n := 0
	for _, b := range m.Blobs {
		n += b.Size
	}
	return &authoringReference{Version: 1, Path: "authoring/manifest.json", SHA256: authoringDigest(a.manifest), Bindings: len(m.Bindings), Blobs: len(m.Blobs), Bytes: n}
}
func authoringDigest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func (a *runAuthoring) publish(dir string, wf *workflow.Workflow, publish func(string, []byte, map[string][]byte, []byte) error) error {
	data, err := encodeWorkflowSnapshot(wf, a.reference(), a.policy)
	if err != nil {
		return err
	}
	a.snapshot = data
	blobs := map[string][]byte{}
	for _, b := range a.assets.Manifest().Blobs {
		blobs[b.SHA256], _ = a.assets.Blob(b.SHA256)
	}
	if err := publish(dir, a.manifest, blobs, data); err != nil {
		return workflow.CaptureStorage
	}
	return a.verify(dir, "")
}
func (a *runAuthoring) verify(dir, owner string) error {
	if a == nil {
		return workflow.CaptureMissing
	}
	if err := a.assets.Verify(owner); err != nil {
		return err
	}
	if dir == "" {
		return nil
	}
	reader, err := datastore.OpenAuthoringReader(dir)
	if err != nil {
		return workflow.CaptureStorage
	}
	defer reader.Close()
	manifest, err := reader.Read("authoring/manifest.json", 8<<20)
	if err != nil {
		return workflow.CaptureStorage
	}
	if !bytes.Equal(manifest, a.manifest) {
		return workflow.CaptureIntegrity
	}
	snapshot, err := reader.Read("workflow.json", 32<<20)
	if err != nil {
		return workflow.CaptureStorage
	}
	if !bytes.Equal(snapshot, a.snapshot) {
		return workflow.CaptureIntegrity
	}
	seen := map[string]bool{}
	for _, b := range a.assets.Manifest().Bindings {
		if owner != "" && b.Owner != "" && b.Owner != owner || seen[b.SHA256] {
			continue
		}
		seen[b.SHA256] = true
		data, err := reader.Read("authoring/blobs/"+b.SHA256, 1<<20)
		if err != nil {
			return workflow.CaptureStorage
		}
		if len(data) != b.Size || authoringDigest(data) != b.SHA256 {
			return workflow.CaptureIntegrity
		}
	}
	return nil
}
func (s *scheduler) assetOwner(id string) string {
	if state := s.states[id]; state != nil && state.ParentID != "" {
		return state.ParentID
	}
	return id
}
func (s *scheduler) verifyAuthoring(id string) bool {
	if s.fatalCaptureErr != nil {
		return false
	}
	// Unit schedulers constructed directly have no prepared run; Manager always
	// installs an authority, including empty inventories.
	if s.authoring == nil {
		return true
	}
	if err := s.authoring.verify(s.runDir, s.assetOwner(id)); err != nil {
		s.failCapture(err)
		return false
	}
	return true
}

// validationSchema supplies the gate owner with frozen authoring bytes. Schema
// enforcement remains separate from capture integrity; no checkout read is allowed.
func (s *scheduler) validationSchema(st *workflow.Step) ([]byte, error) {
	if s.authoring == nil {
		return nil, workflow.CaptureMissing
	}
	if err := s.authoring.verify(s.runDir, s.assetOwner(st.ID)); err != nil {
		return nil, err
	}
	return s.authoring.assets.Bytes(s.assetOwner(st.ID), workflow.AssetValidationSchema)
}
func (s *scheduler) failCapture(err error) {
	if s.fatalCaptureErr != nil {
		return
	}
	var capture workflow.CaptureError
	if !errors.As(err, &capture) {
		capture = workflow.CaptureIntegrity
	}
	s.fatalCaptureErr = capture
	// Set the fence before emission so even a journal failure cannot reauthorize
	// dispatch or contribution. Cancellation preserves the inbox until workers exit.
	_ = s.emit(RunError{RunID: s.runID, Err: capture.Error(), CaptureReason: capture})
	for _, cancel := range s.stepCancels {
		cancel()
	}
}

// loadAuthoringSnapshot pins the run once, decodes metadata without source IO,
// and verifies the complete capture contract before returning execution authority.
func loadAuthoringSnapshot(dir string) (*workflow.Workflow, *runAuthoring, error) {
	reader, err := datastore.OpenAuthoringReader(dir)
	if err != nil {
		return nil, nil, workflow.CaptureStorage
	}
	defer reader.Close()
	data, err := reader.Read("workflow.json", 32<<20)
	if err != nil {
		return nil, nil, workflow.CaptureStorage
	}
	wf, err := DecodeWorkflowSnapshot(data)
	if err != nil {
		return nil, nil, fmt.Errorf("decode locked workflow: %w", err)
	}
	var snapshot workflowSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, nil, workflow.CaptureIntegrity
	}
	if snapshot.SandboxVersion < 2 {
		for _, st := range wf.Steps {
			if st.ResolvedSandbox().Profile != nil {
				return nil, nil, workflow.CaptureIntegrity
			}
		}
	}
	ref := snapshot.Authoring
	if ref == nil || ref.Version != 1 || ref.Path != "authoring/manifest.json" {
		return nil, nil, fmt.Errorf("workflow snapshot lacks a supported authoring capture contract; start a new run: %w", workflow.CaptureMissing)
	}
	if ref.Bindings < 0 || ref.Bindings > 2048 || ref.Blobs < 0 || ref.Blobs > 1024 || ref.Bytes < 0 || ref.Bytes > 16<<20 {
		return nil, nil, workflow.CaptureLimit
	}
	raw, err := reader.Read(ref.Path, 8<<20)
	if err != nil {
		return nil, nil, workflow.CaptureStorage
	}
	if authoringDigest(raw) != ref.SHA256 {
		return nil, nil, workflow.CaptureIntegrity
	}
	var m workflow.AssetManifest
	if err := datastore.DecodeAuthoringRecord(raw, 8<<20, &m); err != nil {
		return nil, nil, workflow.CaptureIntegrity
	}
	if err := workflow.ValidateAuthoringManifest(wf, m); err != nil {
		return nil, nil, err
	}
	if len(m.Bindings) != ref.Bindings || len(m.Blobs) != ref.Blobs {
		return nil, nil, workflow.CaptureIntegrity
	}
	total := 0
	for _, b := range m.Blobs {
		total += b.Size
	}
	if total != ref.Bytes {
		return nil, nil, workflow.CaptureIntegrity
	}
	blobs := map[string][]byte{}
	for _, b := range m.Blobs {
		content, err := reader.Read("authoring/blobs/"+b.SHA256, 1<<20)
		if err != nil {
			return nil, nil, workflow.CaptureStorage
		}
		if len(content) != b.Size || authoringDigest(content) != b.SHA256 {
			return nil, nil, workflow.CaptureIntegrity
		}
		blobs[b.SHA256] = content
	}
	assets, err := workflow.RestoreAuthoring(wf, m, blobs)
	if err != nil {
		return nil, nil, err
	}
	a, err := newRunAuthoring(assets)
	if err != nil {
		return nil, nil, err
	}
	if *a.reference() != *ref || !bytes.Equal(raw, a.manifest) {
		return nil, nil, workflow.CaptureIntegrity
	}
	a.snapshot = data
	if snapshot.Sandbox != nil {
		a.policy = snapshot.Sandbox.Policy.Clone()
	}
	return wf, a, nil
}
