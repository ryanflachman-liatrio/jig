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
	data, err := encodeWorkflowSnapshot(wf, a.reference())
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
	manifest, err := datastore.ReadAuthoringFile(dir, "authoring/manifest.json", 8<<20)
	if err != nil {
		return workflow.CaptureStorage
	}
	if !bytes.Equal(manifest, a.manifest) {
		return workflow.CaptureIntegrity
	}
	snapshot, err := datastore.ReadAuthoringFile(dir, "workflow.json", 32<<20)
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
		data, err := datastore.ReadAuthoringFile(dir, "authoring/blobs/"+b.SHA256, 1<<20)
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

func restoreRunAuthoring(dir string, wf *workflow.Workflow) (*runAuthoring, error) {
	data, err := datastore.ReadAuthoringFile(dir, "workflow.json", 32<<20)
	if err != nil {
		return nil, workflow.CaptureStorage
	}
	var snapshot workflowSnapshot
	if err := datastore.DecodeAuthoringRecord(data, 32<<20, &snapshot); err != nil {
		return nil, workflow.CaptureIntegrity
	}
	ref := snapshot.Authoring
	if ref == nil || ref.Version != 1 || ref.Path != "authoring/manifest.json" {
		return nil, fmt.Errorf("workflow snapshot lacks authoring captures; start a new run")
	}
	raw, err := datastore.ReadAuthoringFile(dir, ref.Path, 8<<20)
	if err != nil {
		return nil, workflow.CaptureStorage
	}
	if authoringDigest(raw) != ref.SHA256 {
		return nil, workflow.CaptureIntegrity
	}
	var m workflow.AssetManifest
	if err := datastore.DecodeAuthoringRecord(raw, 8<<20, &m); err != nil {
		return nil, workflow.CaptureIntegrity
	}
	blobs := map[string][]byte{}
	if len(m.Blobs) > 1024 || len(m.Bindings) > 2048 {
		return nil, workflow.CaptureLimit
	}
	for _, b := range m.Blobs {
		content, err := datastore.ReadAuthoringFile(dir, "authoring/blobs/"+b.SHA256, 1<<20)
		if err != nil {
			return nil, workflow.CaptureStorage
		}
		blobs[b.SHA256] = content
	}
	assets, err := workflow.RestoreAuthoring(wf, m, blobs)
	if err != nil {
		return nil, err
	}
	a, err := newRunAuthoring(assets)
	if err != nil {
		return nil, err
	}
	if *a.reference() != *ref || !bytes.Equal(raw, a.manifest) {
		return nil, workflow.CaptureIntegrity
	}
	a.snapshot = data
	return a, nil
}
