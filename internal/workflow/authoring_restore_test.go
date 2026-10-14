package workflow

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
)

func TestAuthoringManifestStrictCoverage(t *testing.T) {
	for _, damage := range []string{"missing_root", "missing_module", "missing_prompt", "lowercase_prompt", "bundle_prefix", "unknown_kind", "unknown_graph", "blob_size", "blob_order", "graph_dependency", "module_escape"} {
		t.Run(damage, func(t *testing.T) {
			_, wf := moduleAssetFixture(t)
			owned, a, err := PrepareAuthoring(wf, "")
			if err != nil {
				t.Fatal(err)
			}
			m := a.Manifest()
			target := -1
			for i, b := range m.Bindings {
				if b.Owner == "first__nested__agent" && b.Kind == AssetSkill && b.Relative == "SKILL.md" {
					target = i
				}
			}
			switch damage {
			case "missing_root", "missing_module":
				kind := AssetWorkflow
				if damage == "missing_module" {
					kind = AssetModule
				}
				for i, b := range m.Bindings {
					if b.Kind == kind {
						m.Bindings = append(m.Bindings[:i], m.Bindings[i+1:]...)
						break
					}
				}
			case "missing_prompt":
				m.Bindings = append(m.Bindings[:target], m.Bindings[target+1:]...)
			case "lowercase_prompt":
				m.Bindings[target].Relative = "skill.md"
			case "bundle_prefix":
				for i, b := range m.Bindings {
					if b.Owner == "first__nested__agent" && b.Relative == "helper.sh" {
						m.Bindings[i].Relative = "Dir/helper.sh"
					}
					if b.Owner == "first__nested__agent" && b.Relative == "same.sh" {
						m.Bindings[i].Relative = "dir/same.sh"
					}
				}
			case "unknown_kind":
				m.Bindings[target].Kind = "unknown"
			case "unknown_graph":
				m.Bindings[target].Owner = "unknown"
			case "blob_size":
				m.Blobs[0].Size++
			case "blob_order":
				m.Blobs[0], m.Blobs[1] = m.Blobs[1], m.Blobs[0]
			case "module_escape":
				owned.publicSteps[0].Module = owned.moduleSources[0].Path
			case "graph_dependency":
				owned.Steps[0].DependsOn = []string{"unknown"}
			}
			sort.Slice(m.Bindings, func(i, j int) bool { return bindingKey(m.Bindings[i]) < bindingKey(m.Bindings[j]) })
			if err := ValidateAuthoringManifest(owned, m); err == nil {
				t.Fatal("damaged inventory accepted")
			}
		})
	}
}

func TestAuthoringManifestReadBudget(t *testing.T) {
	for _, count := range []int{32, 33} {
		var text strings.Builder
		text.WriteString("[workflow]\nname='bounded'\nversion='1'\n")
		digest := assetDigest(make([]byte, 1<<20))
		m := AssetManifest{Version: 1, Blobs: []AssetBlob{{SHA256: digest, Size: 1 << 20}}}
		for i := 0; i < count; i++ {
			id := fmt.Sprintf("worker%02d", i)
			fmt.Fprintf(&text, "[[step]]\nid='%s'\ntype='command'\nscript='script.sh'\n", id)
			m.Bindings = append(m.Bindings, AssetBinding{Owner: id, Kind: AssetScript, Reference: "script.sh", Anchor: "/synthetic", Source: "/synthetic/script.sh", Size: 1 << 20, SHA256: digest})
		}
		wf, err := Decode(text.String(), "")
		if err != nil {
			t.Fatal(err)
		}
		err = ValidateAuthoringManifest(wf, m)
		if count == 32 && err != nil {
			t.Fatalf("exact read budget: %v", err)
		}
		if count == 33 && !errors.Is(err, CaptureLimit) {
			t.Fatalf("one-over read budget: %v", err)
		}
	}
}
