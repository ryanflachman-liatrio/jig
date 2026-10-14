package workflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CaptureError identifies a run infrastructure failure without disclosing paths
// or source content. It must bypass ordinary attempt recovery.
type CaptureError string

func (e CaptureError) Error() string { return "authoring capture: " + string(e) }

const (
	CaptureLimit       CaptureError = "limit"
	CaptureForbidden   CaptureError = "forbidden_path"
	CaptureUnsupported CaptureError = "unsupported_file"
	CapturePlatform    CaptureError = "unsupported_platform"
	CaptureIntegrity   CaptureError = "integrity"
	CaptureStorage     CaptureError = "storage"
	CaptureMissing     CaptureError = "missing_binding"
)

// AssetKind describes declared authoring inputs, never runtime workspace files.
type AssetKind string

const (
	AssetWorkflow         AssetKind = "workflow"
	AssetModule           AssetKind = "module"
	AssetScript           AssetKind = "script"
	AssetSkill            AssetKind = "skill"
	AssetAgentFile        AssetKind = "agent_file"
	AssetSchema           AssetKind = "schema"
	AssetValidationSchema AssetKind = "validation_schema"
	AssetOutputTemplate   AssetKind = "output_template"
)

// AssetBinding retains logical provenance even when content is deduplicated.
type AssetBinding struct {
	// Declaration distinguishes repeated module invocations before expansion.
	Declaration string    `json:"declaration,omitempty"`
	Owner       string    `json:"owner"`
	Kind        AssetKind `json:"kind"`
	Reference   string    `json:"reference"`
	Declaring   string    `json:"declaring"`
	Anchor      string    `json:"anchor"`
	Source      string    `json:"source"`
	Relative    string    `json:"relative,omitempty"`
	Size        int       `json:"size"`
	Executable  bool      `json:"executable"`
	SHA256      string    `json:"sha256"`
}
type AssetBlob struct {
	SHA256 string `json:"sha256"`
	Size   int    `json:"size"`
}
type AssetManifest struct {
	Version  int            `json:"version"`
	Bindings []AssetBinding `json:"bindings"`
	Blobs    []AssetBlob    `json:"blobs"`
}

// AuthoringAssets owns a sealed inventory. All exported accessors return copies.
// A run and its workers never receive a mutable alias to the capture ledger.
type AuthoringAssets struct {
	manifest AssetManifest
	blobs    map[string][]byte
}

func (a *AuthoringAssets) Manifest() AssetManifest {
	if a == nil {
		return AssetManifest{}
	}
	return AssetManifest{Version: a.manifest.Version, Bindings: append([]AssetBinding{}, a.manifest.Bindings...), Blobs: append([]AssetBlob{}, a.manifest.Blobs...)}
}
func (a *AuthoringAssets) Blob(digest string) ([]byte, bool) {
	if a == nil {
		return nil, false
	}
	b, ok := a.blobs[digest]
	return bytes.Clone(b), ok
}
func (a *AuthoringAssets) Bytes(owner string, kind AssetKind) ([]byte, error) {
	if a != nil {
		for _, b := range a.manifest.Bindings {
			if b.Owner == owner && b.Kind == kind {
				data, ok := a.Blob(b.SHA256)
				if !ok {
					return nil, CaptureMissing
				}
				return data, nil
			}
		}
	}
	return nil, CaptureMissing
}

// Verify checks either one template's inputs plus graph sources, or the entire
// inventory when owner is empty. Empty file contents remain present bindings.
func (a *AuthoringAssets) Verify(owner string) error {
	if a == nil || a.manifest.Version != 1 {
		return CaptureMissing
	}
	for _, b := range a.manifest.Bindings {
		if owner != "" && b.Owner != "" && b.Owner != owner {
			continue
		}
		data, ok := a.blobs[b.SHA256]
		if !ok {
			return CaptureMissing
		}
		if len(data) != b.Size || assetDigest(data) != b.SHA256 {
			return CaptureIntegrity
		}
	}
	return nil
}

// VerifyStep checks a worker's effective values against its immutable template
// captures, including runtime children whose IDs differ from the template owner.
func (a *AuthoringAssets) VerifyStep(owner string, st *Step) error {
	if st == nil {
		return CaptureMissing
	}
	if err := a.Verify(owner); err != nil {
		return err
	}
	required := map[AssetKind]bool{
		AssetSkill: st.Skill != "", AssetAgentFile: st.AgentFile != "",
		AssetSchema: st.SchemaFile != "", AssetOutputTemplate: st.OutputTemplate != "",
	}
	for kind, yes := range required {
		if !yes {
			continue
		}
		found := false
		for _, b := range a.manifest.Bindings {
			if b.Owner == owner && b.Kind == kind && (kind != AssetSkill || b.Relative == "SKILL.md") {
				found = true
				break
			}
		}
		if !found {
			return CaptureMissing
		}
	}
	copy := *st
	copy.ID = owner
	return a.validateEffective(&Workflow{Steps: []Step{copy}})
}
func assetDigest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

type authoringLoad struct {
	ledger  captureLedger
	sources []AssetBinding
}

func (l *authoringLoad) clone() *authoringLoad {
	if l == nil {
		return &authoringLoad{}
	}
	out := *l
	out.sources = append([]AssetBinding(nil), l.sources...)
	out.ledger.blobs = make(map[[32]byte][]byte, len(l.ledger.blobs))
	for k, b := range l.ledger.blobs {
		out.ledger.blobs[k] = bytes.Clone(b)
	}
	return &out
}
func assetPath(base, ref string) string {
	if filepath.IsAbs(ref) {
		return ref
	}
	return filepath.Join(base, ref)
}
func (l *authoringLoad) file(owner string, kind AssetKind, ref, base, declaring string) ([]byte, AssetBinding, error) {
	for _, path := range []string{base, ref} {
		if err := captureValidatePath(path); err != nil {
			return nil, AssetBinding{}, err
		}
	}
	path, err := filepath.Abs(assetPath(base, ref))
	if err != nil {
		return nil, AssetBinding{}, CaptureStorage
	}
	if err := captureValidatePath(path); err != nil {
		return nil, AssetBinding{}, err
	}
	anchor := filepath.Dir(path)
	root, err := capturePin(anchor)
	if err != nil {
		return nil, AssetBinding{}, err
	}
	defer root.Close()
	got, err := l.ledger.capture(root, filepath.Base(path), nil)
	if err != nil {
		return nil, AssetBinding{}, err
	}
	// The descriptor supplies the bytes; canonical provenance never authorizes a
	// later read. Link resolution is confined by the collector.

	b := AssetBinding{Owner: owner, Kind: kind, Reference: ref, Declaring: declaring, Anchor: root.Name(), Source: got.source, Size: len(got.bytes), Executable: got.executable, SHA256: fmt.Sprintf("%x", got.digest)}
	return got.bytes, b, nil
}
func (wf *Workflow) readAsset(s *Step, kind AssetKind, ref, base string) ([]byte, error) {
	if wf.authoring == nil {
		wf.authoring = &authoringLoad{}
	}
	data, b, err := wf.authoring.file(s.ID, kind, ref, base, s.declaring)
	if err == nil {
		s.assets = append(s.assets, b)
	}
	return data, err
}
func (wf *Workflow) readSkill(s *Step, base string) ([]byte, error) {
	for _, path := range []string{base, s.Skill} {
		if err := captureValidatePath(path); err != nil {
			return nil, err
		}
	}
	path := assetPath(base, s.Skill)
	project := RepoRoot(base)
	if project == "" {
		project = base
	}
	root, err := captureSelectBundle(path, project)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	files, err := wf.authoring.ledger.bundle(root)
	if err != nil {
		return nil, err
	}
	skill, ok := files["SKILL.md"]
	if !ok {
		return nil, CaptureMissing
	}

	for name, c := range files {
		s.assets = append(s.assets, AssetBinding{Owner: s.ID, Kind: AssetSkill, Reference: s.Skill, Declaring: s.declaring, Anchor: root.Name(), Source: c.source, Relative: filepath.ToSlash(name), Size: len(c.bytes), Executable: c.executable, SHA256: fmt.Sprintf("%x", c.digest)})
	}
	return bytes.Clone(skill.bytes), nil
}

// PrepareAuthoring takes an owned graph snapshot and completes capture before
// any dispatch. base is optional for structural/programmatic workflows; empty
// freezes the caller's current directory now, never an executor's later CWD.
func PrepareAuthoring(wf *Workflow, base string) (*Workflow, *AuthoringAssets, error) {
	if wf == nil {
		return nil, nil, CaptureMissing
	}
	if wf.lockedMetadata {
		return nil, nil, fmt.Errorf("display workflow is not execution authority; start a new run from authoring TOML: %w", CaptureMissing)
	}
	if len(wf.Steps) > 1024 {
		return nil, nil, CaptureLimit
	}
	if base == "" {
		var err error
		base, err = os.Getwd()
		if err != nil {
			return nil, nil, CaptureStorage
		}
	}
	base, err := filepath.Abs(base)
	if err != nil {
		return nil, nil, CaptureStorage
	}
	// JSON snapshots carry resolved agents, parsed schemas, and effective prompts.
	// Rebuild defaults/index after copying to avoid sharing pointer-bearing fields.
	data, err := json.Marshal(wf)
	if err != nil {
		return nil, nil, CaptureIntegrity
	}
	if len(data) > 32<<20 {
		return nil, nil, CaptureLimit
	}
	var out Workflow
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, nil, CaptureIntegrity
	}
	out.sourcePath, out.sourceTOML = wf.sourcePath, wf.sourceTOML
	out.sourceBaseDir = wf.sourceBaseDir
	out.moduleSources = wf.ModuleSources()
	public, err := json.Marshal(wf.PublicSteps())
	if err != nil {
		return nil, nil, CaptureIntegrity
	}
	if err := json.Unmarshal(public, &out.publicSteps); err != nil {
		return nil, nil, CaptureIntegrity
	}
	out.SetResolvedNotificationPolicy(wf.NotificationPolicy())
	out.authoring = wf.authoring.clone()
	for i := range out.Steps {
		s := &out.Steps[i]
		original := &wf.Steps[i]
		s.assets = append([]AssetBinding(nil), original.assets...)
		s.scriptAnchor, s.assetAnchor, s.declaring = original.scriptAnchor, original.assetAnchor, original.declaring
		if s.Validate != nil && original.Validate != nil {
			s.Validate.outputSchemaReference = original.Validate.outputSchemaReference
		}
		if s.scriptAnchor == "" {
			s.scriptAnchor = base
		}
		if s.assetAnchor == "" {
			s.assetAnchor = base
		}
		for j := range s.Review {
			s.Review[j].resolvedPath = original.Review[j].resolvedPath
		}
		has := func(kind AssetKind) bool {
			for _, b := range s.assets {
				if b.Kind == kind {
					return true
				}
			}
			return false
		}
		if s.Skill != "" && !has(AssetSkill) {
			data, err := out.readSkill(s, s.assetAnchor)
			if err != nil {
				return nil, nil, err
			}
			prompt, err := parseSkillFile(data)
			if err != nil {
				return nil, nil, CaptureIntegrity
			}
			s.SnapshotAgentPrompt = prompt
		}
		if s.AgentFile != "" && !has(AssetAgentFile) {
			data, err := out.readAsset(s, AssetAgentFile, s.AgentFile, s.assetAnchor)
			if err != nil {
				return nil, nil, err
			}
			af, err := parseAgentFile(data)
			if err != nil {
				return nil, nil, CaptureIntegrity
			}
			s.SnapshotAgentPrompt = af.Prompt
		}
		if s.OutputTemplate != "" && !has(AssetOutputTemplate) {
			data, err := out.readAsset(s, AssetOutputTemplate, s.OutputTemplate, s.assetAnchor)
			if err != nil {
				return nil, nil, err
			}
			s.SnapshotOutputTemplate = strings.TrimSpace(string(data))
		}
		if s.SchemaFile != "" && !has(AssetSchema) {
			data, err := out.readAsset(s, AssetSchema, s.SchemaFile, s.assetAnchor)
			if err != nil {
				return nil, nil, err
			}
			sc, err := ParseJSONSchema(data)
			if err != nil {
				return nil, nil, CaptureIntegrity
			}
			sc.File = s.SchemaFile
			s.Schema = sc
		}
		if s.Script != "" && !strings.Contains(s.Script, "\n") && !has(AssetScript) {
			if _, err := out.readAsset(s, AssetScript, s.Script, s.scriptAnchor); err != nil {
				return nil, nil, err
			}
		}
		if s.Validate != nil && s.Validate.OutputSchema != "" && !has(AssetValidationSchema) {
			ref := s.Validate.outputSchemaReference
			if ref != "" && s.Validate.OutputSchema != ref && s.Validate.OutputSchema != assetPath(s.assetAnchor, ref) {
				return nil, nil, CaptureIntegrity
			}
			if ref == "" {
				ref = s.Validate.OutputSchema
			}
			if _, err := out.readAsset(s, AssetValidationSchema, ref, s.assetAnchor); err != nil {
				return nil, nil, err
			}
		}
	}
	out.applyDefaults()
	a := &AuthoringAssets{manifest: AssetManifest{Version: 1, Bindings: append([]AssetBinding{}, out.authoring.sources...), Blobs: []AssetBlob{}}, blobs: map[string][]byte{}}
	for _, s := range out.Steps {
		for _, b := range s.assets {
			b.Owner = s.ID
			a.manifest.Bindings = append(a.manifest.Bindings, b)
		}
	}
	if len(a.manifest.Bindings) > 2048 {
		return nil, nil, CaptureLimit
	}
	if err := a.validateLogicalBindings(); err != nil {
		return nil, nil, err
	}
	for digest, b := range out.authoring.ledger.blobs {
		key := fmt.Sprintf("%x", digest)
		a.blobs[key] = bytes.Clone(b)
		a.manifest.Blobs = append(a.manifest.Blobs, AssetBlob{SHA256: key, Size: len(b)})
	}
	sort.Slice(a.manifest.Bindings, func(i, j int) bool {
		x, y := a.manifest.Bindings[i], a.manifest.Bindings[j]
		return bindingKey(x) < bindingKey(y)
	})
	sort.Slice(a.manifest.Blobs, func(i, j int) bool { return a.manifest.Blobs[i].SHA256 < a.manifest.Blobs[j].SHA256 })
	encoded, err := json.Marshal(a.manifest)
	if err != nil {
		return nil, nil, CaptureIntegrity
	}
	if len(encoded) > 8<<20 {
		return nil, nil, CaptureLimit
	}
	if err := a.Verify(""); err != nil {
		return nil, nil, err
	}
	if err := a.validateEffective(&out); err != nil {
		return nil, nil, err
	}
	return &out, a, nil
}
func bindingKey(b AssetBinding) string {
	return b.Owner + "\x00" + b.Declaration + "\x00" + string(b.Kind) + "\x00" + b.Declaring + "\x00" + b.Reference + "\x00" + b.Relative
}

// Content deduplication never permits ambiguous logical inputs. Bundle members
// have distinct relative names; every other step input has one binding per kind.
func (a *AuthoringAssets) validateLogicalBindings() error {
	seen := map[string]bool{}
	for _, b := range a.manifest.Bindings {
		key := bindingKey(b)
		if b.Owner != "" {
			key = b.Owner + "\x00" + string(b.Kind)
			if b.Kind == AssetSkill {
				key += "\x00" + b.Relative
			}
		}
		if seen[key] {
			return CaptureIntegrity
		}
		seen[key] = true
	}
	return nil
}

// RestoreAuthoring restores only supplied captured content; it never opens an
// authoring source. Durable readers own confinement and encoded input bounds.
func RestoreAuthoring(wf *Workflow, m AssetManifest, blobs map[string][]byte) (*AuthoringAssets, error) {
	if err := ValidateAuthoringManifest(wf, m); err != nil {
		return nil, err
	}
	if len(blobs) != len(m.Blobs) {
		return nil, CaptureIntegrity
	}
	a := &AuthoringAssets{manifest: AssetManifest{Version: 1, Bindings: append([]AssetBinding{}, m.Bindings...), Blobs: append([]AssetBlob{}, m.Blobs...)}, blobs: map[string][]byte{}}
	for _, b := range m.Blobs {
		data, ok := blobs[b.SHA256]
		if !ok {
			return nil, CaptureMissing
		}
		if len(data) != b.Size || assetDigest(data) != b.SHA256 {
			return nil, CaptureIntegrity
		}
		a.blobs[b.SHA256] = bytes.Clone(data)
	}
	return a, a.validateEffective(wf)
}

func (a *AuthoringAssets) validateEffective(wf *Workflow) error {
	for _, s := range wf.Steps {
		for _, kind := range []AssetKind{AssetSkill, AssetAgentFile, AssetOutputTemplate, AssetSchema} {
			var data []byte
			present := false
			for _, b := range a.manifest.Bindings {
				if b.Owner == s.ID && b.Kind == kind && (kind != AssetSkill || b.Relative == "SKILL.md") {
					data = a.blobs[b.SHA256]
					present = true
					break
				}
			}
			if !present {
				continue
			}
			switch kind {
			case AssetSkill:
				prompt, err := parseSkillFile(data)
				if err != nil || prompt != s.AgentPrompt() {
					return CaptureIntegrity
				}
			case AssetAgentFile:
				af, err := parseAgentFile(data)
				if err != nil || af.Prompt != s.AgentPrompt() {
					return CaptureIntegrity
				}
			case AssetOutputTemplate:
				if strings.TrimSpace(string(data)) != s.OutputTemplateBody() {
					return CaptureIntegrity
				}
			case AssetSchema:
				sc, err := ParseJSONSchema(data)
				if err != nil || s.Schema == nil {
					return CaptureIntegrity
				}
				actual := *s.Schema
				actual.File = ""
				expected, _ := json.Marshal(sc)
				got, _ := json.Marshal(actual)
				if !bytes.Equal(got, expected) {
					return CaptureIntegrity
				}
			}
		}
	}
	return nil
}
