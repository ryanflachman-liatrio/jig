package workflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareAuthoringBasesAndOwnership(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "start_cwd", true: "supplied_base"}[explicit], func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "script.sh"), []byte("ORIGINAL"), 0600); err != nil {
				t.Fatal(err)
			}
			wf, err := Decode("[workflow]\nname='capture'\nversion='1'\n[[step]]\nid='worker'\ntype='command'\nscript='script.sh'\n", "")
			if err != nil {
				t.Fatal(err)
			}
			base := ""
			if explicit {
				base = dir
			} else {
				t.Chdir(dir)
			}
			owned, a, err := PrepareAuthoring(wf, base)
			if err != nil {
				t.Fatal(err)
			}
			_, other, err := PrepareAuthoring(wf, base)
			if err != nil {
				t.Fatal(err)
			}
			wf.Steps[0].Script = "replacement"
			b, _ := a.Bytes("worker", AssetScript)
			b[0] = 'X'
			m := a.Manifest()
			m.Bindings[0].SHA256 = "changed"
			if got, _ := other.Bytes("worker", AssetScript); string(got) != "ORIGINAL" {
				t.Fatal("run authority aliased caller")
			}
			if owned.Steps[0].Script != "script.sh" {
				t.Fatal("run graph aliased caller")
			}
			if err := os.Remove(filepath.Join(dir, "script.sh")); err != nil {
				t.Fatal(err)
			}
			_, again, err := PrepareAuthoring(owned, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if b, _ := again.Bytes("worker", AssetScript); string(b) != "ORIGINAL" {
				t.Fatal("prepared authority recaptured")
			}
		})
	}
}
func TestPrepareAuthoringRetainsLoadCapture(t *testing.T) {
	dir := t.TempDir()
	skill := filepath.Join(dir, "skill")
	if err := os.Mkdir(skill, 0700); err != nil {
		t.Fatal(err)
	}
	prompt := "---\nname: fixture\ndescription: Synthetic skill\n---\nORIGINAL prompt.\n"
	for name, body := range map[string]string{"SKILL.md": prompt, "helper.sh": "ORIGINAL helper"} {
		if err := os.WriteFile(filepath.Join(skill, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	source := "[workflow]\nname='capture'\nversion='1'\n[[step]]\nid='worker'\ntype='agent'\nskill='skill'\n"
	if err := os.WriteFile(filepath.Join(dir, "workflow.toml"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	wf, err := Load(filepath.Join(dir, "workflow.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(skill); err != nil {
		t.Fatal(err)
	}
	owned, a, err := PrepareAuthoring(wf, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(owned.Steps[0].AgentPrompt(), "ORIGINAL prompt") {
		t.Fatal("lost prompt")
	}
	if len(a.Manifest().Bindings) != 3 {
		t.Fatalf("bindings=%d", len(a.Manifest().Bindings))
	}
	for _, binding := range a.Manifest().Bindings {
		if binding.Relative == "helper.sh" {
			b, _ := a.Blob(binding.SHA256)
			if string(b) != "ORIGINAL helper" || !binding.Executable {
				t.Fatal("bundle changed")
			}
		}
	}
	// Private-memory damage is detected by the same boundary verifier.
	for key := range a.blobs {
		a.blobs[key] = []byte("damaged")
		break
	}
	if !errors.Is(a.Verify(""), CaptureIntegrity) {
		t.Fatal("memory corruption accepted")
	}
	wf.Steps[0].SnapshotAgentPrompt = "caller replacement"
	wf.Steps[0].agentPrompt = "caller replacement"
	if _, _, err := PrepareAuthoring(wf, ""); !errors.Is(err, CaptureIntegrity) {
		t.Fatalf("conflicting raw/effective prompt accepted: %v", err)
	}
}

func TestAuthoringExpandedGraphLimit(t *testing.T) {
	dir := t.TempDir()
	child := "[module]\nschema_version=1\n[module.exports.result]\nref='@b'\n[[step]]\nid='a'\ntype='command'\nrun='true'\n[[step]]\nid='b'\ntype='command'\nrun='true'\ndepends_on=['a']\n"
	if err := os.WriteFile(filepath.Join(dir, "child.toml"), []byte(child), 0600); err != nil {
		t.Fatal(err)
	}
	for _, count := range []int{512, 513} {
		var source strings.Builder
		source.WriteString("[workflow]\nname='bounded'\nversion='1'\n")
		for i := 0; i < count; i++ {
			fmt.Fprintf(&source, "[[step]]\nid='module%d'\ntype='subworkflow'\nmodule='child.toml'\n", i)
		}
		if err := os.WriteFile(filepath.Join(dir, "workflow.toml"), []byte(source.String()), 0600); err != nil {
			t.Fatal(err)
		}
		wf, err := Load(filepath.Join(dir, "workflow.toml"))
		if count == 512 {
			if err != nil || len(wf.Steps) != 1024 {
				t.Fatalf("exact bound err=%v", err)
			}
			_, assets, err := PrepareAuthoring(wf, dir)
			if err != nil {
				t.Fatal(err)
			}
			m := assets.Manifest()
			if len(m.Bindings) != 513 || len(m.Blobs) != 2 {
				t.Fatalf("logical bindings=%d blobs=%d", len(m.Bindings), len(m.Blobs))
			}
			declarations := map[string]bool{}
			for _, binding := range m.Bindings {
				if binding.Kind == AssetModule {
					declarations[binding.Declaration] = true
				}
			}
			if len(declarations) != 512 {
				t.Fatalf("module declarations=%d", len(declarations))
			}

		} else if !errors.Is(err, CaptureLimit) {
			t.Fatalf("over expanded limit: %v", err)
		}
	}
}
