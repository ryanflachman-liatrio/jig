package workflow

import (
	"path/filepath"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type assetDeclaration struct {
	step      Step
	declaring string
	rebased   bool
}
type moduleDeclaration struct {
	ref, declaring string
	source         ModuleSource
}

// ValidateAuthoringManifest checks bounds and graph coverage before durable blob
// allocation. All provenance is supplied metadata; source paths are never opened.
func ValidateAuthoringManifest(wf *Workflow, m AssetManifest) error {
	if wf == nil || m.Version != 1 {
		return CaptureIntegrity
	}
	if err := wf.validate(""); err != nil {
		return CaptureIntegrity
	}
	if len(wf.Steps) > 1024 || len(m.Bindings) > 2048 || len(m.Blobs) > 1024 {
		return CaptureLimit
	}
	descriptors := map[string]int{}
	total := 0
	last := ""
	for _, b := range m.Blobs {
		if !validAssetDigest(b.SHA256) || b.SHA256 <= last || b.Size < 0 || b.Size > 1<<20 {
			return CaptureIntegrity
		}
		last = b.SHA256
		total += b.Size
		if total > 16<<20 {
			return CaptureLimit
		}
		descriptors[b.SHA256] = b.Size
	}
	a := &AuthoringAssets{manifest: m}
	if err := a.validateLogicalBindings(); err != nil {
		return err
	}
	declarations, modules, err := wf.assetDeclarations()
	if err != nil {
		return err
	}
	graph := map[string]Step{}
	for _, s := range wf.Steps {
		if s.ID == "" || graph[s.ID].ID != "" {
			return CaptureIntegrity
		}
		graph[s.ID] = s
	}
	used := map[string]bool{}
	covered := map[string]bool{}
	coveredModules := map[string]bool{}
	namespaces := map[string]map[string]bool{}
	bundleBytes := map[string]int{}
	bundleCount := map[string]int{}
	bundleAnchors := map[string]string{}
	bundlePrefixes := map[string]map[string]string{}
	prompts := map[string]bool{}
	rootSeen := false
	readBytes := 0
	last = ""
	for _, b := range m.Bindings {
		readBytes += b.Size
		if readBytes > 32<<20 {
			return CaptureLimit
		}
		key := bindingKey(b)
		if key <= last {
			return CaptureIntegrity
		}
		last = key
		size, ok := descriptors[b.SHA256]
		if !ok || b.Size != size {
			return CaptureIntegrity
		}
		used[b.SHA256] = true
		for _, p := range []string{b.Reference, b.Declaring, b.Anchor, b.Source, b.Relative, b.Declaration} {
			if err := captureValidatePath(p); err != nil {
				return err
			}
		}
		if b.Reference == "" || !cleanAbsolute(b.Anchor) || !cleanAbsolute(b.Source) || b.Source == b.Anchor || !withinDir(b.Anchor, b.Source) {
			return CaptureIntegrity
		}
		rel, _ := filepath.Rel(b.Anchor, b.Source)
		if len(strings.Split(rel, string(filepath.Separator))) > 64 {
			return CaptureLimit
		}
		if b.Declaring != "" && !cleanAbsolute(b.Declaring) {
			return CaptureIntegrity
		}
		if b.Owner == "" {
			if b.Relative != "" {
				return CaptureIntegrity
			}
			switch b.Kind {
			case AssetWorkflow:
				if rootSeen || b.Declaration != "" || wf.sourcePath == "" || b.Declaring != wf.sourcePath || b.SHA256 != assetDigest([]byte(wf.sourceTOML)) {
					return CaptureIntegrity
				}
				rootSeen = true
			case AssetModule:
				expected, ok := modules[b.Declaration]
				if !ok || b.Reference != expected.ref || b.Declaring != expected.declaring || b.SHA256 != expected.source.SHA256 {
					return CaptureIntegrity
				}
				coveredModules[b.Declaration] = true
			default:
				return CaptureIntegrity
			}
			continue
		}
		s, ok := graph[b.Owner]
		if !ok || b.Declaration != "" {
			return CaptureIntegrity
		}
		d, ok := declarations[b.Owner]
		if !ok || b.Declaring != d.declaring {
			return CaptureIntegrity
		}
		ref := declaredAsset(d.step, b.Kind)
		if ref == "" || b.Reference != ref {
			return CaptureIntegrity
		}
		effective := declaredAsset(s, b.Kind)
		if b.Kind != AssetScript && d.rebased {
			ref = assetPath(filepath.Dir(d.declaring), ref)
		}
		if effective != ref {
			return CaptureIntegrity
		}
		covered[b.Owner+"\x00"+string(b.Kind)] = true
		if b.Kind != AssetSkill {
			if b.Relative != "" {
				return CaptureIntegrity
			}
			continue
		}
		if b.Relative == "" || filepath.IsAbs(b.Relative) || filepath.Clean(b.Relative) != b.Relative || b.Relative == "." || strings.HasPrefix(b.Relative, "../") || !norm.NFC.IsNormalString(b.Relative) {
			return CaptureIntegrity
		}
		if len(strings.Split(b.Relative, "/")) > 17 {
			return CaptureLimit
		}
		if namespaces[b.Owner] == nil {
			namespaces[b.Owner] = map[string]bool{}
			bundlePrefixes[b.Owner] = map[string]string{}
			bundleAnchors[b.Owner] = b.Anchor
		}
		if bundleAnchors[b.Owner] != b.Anchor {
			return CaptureIntegrity
		}
		folded := cases.Fold().String(b.Relative)
		prefix := ""
		for _, component := range strings.Split(b.Relative, "/") {
			prefix = filepath.Join(prefix, component)
			key := cases.Fold().String(prefix)
			if old, exists := bundlePrefixes[b.Owner][key]; exists && old != prefix {
				return CaptureIntegrity
			}
			bundlePrefixes[b.Owner][key] = prefix
		}
		for old := range namespaces[b.Owner] {
			if folded == old || strings.HasPrefix(folded, old+"/") || strings.HasPrefix(old, folded+"/") {
				return CaptureIntegrity
			}
		}
		namespaces[b.Owner][folded] = true
		if b.Relative == "SKILL.md" {
			prompts[b.Owner] = true
		}
		bundleCount[b.Owner]++
		bundleBytes[b.Owner] += b.Size
		if bundleCount[b.Owner] > 256 || bundleBytes[b.Owner] > 4<<20 {
			return CaptureLimit
		}
	}
	if (wf.sourcePath != "") != rootSeen || len(coveredModules) != len(modules) || len(used) != len(m.Blobs) {
		return CaptureMissing
	}
	for _, s := range wf.Steps {
		for _, kind := range []AssetKind{AssetScript, AssetSkill, AssetAgentFile, AssetSchema, AssetValidationSchema, AssetOutputTemplate} {
			if declaredAsset(s, kind) != "" && !covered[s.ID+"\x00"+string(kind)] {
				return CaptureMissing
			}
		}
		if s.Skill != "" && !prompts[s.ID] {
			return CaptureMissing
		}
	}
	return nil
}
func validAssetDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func cleanAbsolute(s string) bool { return filepath.IsAbs(s) && filepath.Clean(s) == s }
func declaredAsset(s Step, kind AssetKind) string {
	switch kind {
	case AssetScript:
		if !strings.Contains(s.Script, "\n") {
			return s.Script
		}
	case AssetSkill:
		return s.Skill
	case AssetAgentFile:
		return s.AgentFile
	case AssetSchema:
		return s.SchemaFile
	case AssetOutputTemplate:
		return s.OutputTemplate
	case AssetValidationSchema:
		if s.Validate != nil {
			return s.Validate.OutputSchema
		}
	}
	return ""
}
func (wf *Workflow) assetDeclarations() (map[string]assetDeclaration, map[string]moduleDeclaration, error) {
	leaves := map[string]assetDeclaration{}
	modules := map[string]moduleDeclaration{}
	sources := map[string]ModuleSource{}
	for _, s := range wf.moduleSources {
		if sources[s.Path].Path != "" || !cleanAbsolute(s.Path) || len(s.TOML) > 1<<20 || assetDigest([]byte(s.TOML)) != s.SHA256 {
			return nil, nil, CaptureIntegrity
		}
		sources[s.Path] = s
	}
	var walk func([]Step, string, string, int) error
	walk = func(steps []Step, declaring, prefix string, depth int) error {
		if depth > 32 {
			return CaptureLimit
		}
		for _, s := range steps {
			id := prefix + s.ID
			if s.Type != StepSubworkflow {
				if len(leaves) >= 1024 {
					return CaptureLimit
				}
				if leaves[id].step.ID != "" {
					return CaptureIntegrity
				}
				leaves[id] = assetDeclaration{s, declaring, prefix != ""}
				continue
			}
			if depth >= 32 || len(modules) >= 2048 {
				return CaptureLimit
			}
			base := filepath.Dir(declaring)
			if prefix == "" && wf.sourceBaseDir != "" {
				base = wf.sourceBaseDir
			}
			path := assetPath(base, s.Module)
			if s.Module == "" || filepath.IsAbs(s.Module) || path == base || !withinDir(base, path) {
				return CaptureForbidden
			}
			source, ok := sources[path]
			if !ok {
				return CaptureMissing
			}
			modules[id] = moduleDeclaration{s.Module, declaring, source}
			child, err := decodeMetadataTOML(source.TOML)
			if err != nil {
				return err
			}
			if err := walk(child.Steps, path, id+"__", depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(wf.PublicSteps(), wf.sourcePath, "", 0); err != nil {
		return nil, nil, err
	}
	return leaves, modules, nil
}
