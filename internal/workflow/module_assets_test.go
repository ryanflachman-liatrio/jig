package workflow

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func moduleAssetFixture(t *testing.T) (string, *Workflow) {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
	files := map[string]string{
		"workflow.toml":                "[workflow]\nname='module-assets'\nversion='1'\n[[step]]\nid='first'\ntype='subworkflow'\nmodule='modules/a.toml'\n[[step]]\nid='second'\ntype='subworkflow'\nmodule='modules/a.toml'\n",
		"modules/a.toml":               "[module]\nschema_version=1\n[module.exports.result]\nref='@nested.result'\n[[step]]\nid='nested'\ntype='subworkflow'\nmodule='deep/b.toml'\n",
		"modules/deep/b.toml":          "[module]\nschema_version=1\n[module.exports.result]\nref='@shell'\n[[step]]\nid='agent'\ntype='agent'\nskill='skill'\noutput_template='template.md'\nschema_file='schema.json'\noutput='result.md'\n[step.validate]\noutput_schema='validation.json'\n[[step]]\nid='file_agent'\ntype='agent'\nagent_file='agent.md'\n[[step]]\nid='shell'\ntype='command'\nscript='root.sh'\ndepends_on=['agent','file_agent']\n",
		"modules/deep/skill/SKILL.md":  "---\nname: fixture\ndescription: Synthetic fixture\n---\nORIGINAL prompt {{skill_dir}}\n",
		"modules/deep/skill/helper.sh": "ORIGINAL helper\n",
		"modules/deep/skill/same.sh":   "ORIGINAL helper\n",
		"modules/deep/agent.md":        "---\nname: fixture\ndescription: Synthetic agent\n---\nORIGINAL file prompt\n",
		"modules/deep/template.md":     "# ORIGINAL template\n",
		"modules/deep/schema.json":     `{"type":"object","properties":{"original":{"type":"string"}}}`,
		"modules/deep/validation.json": `{"title":"ORIGINAL validation"}`,
		"root.sh":                      "echo ORIGINAL\n",
	}
	for path, body := range files {
		p := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	wf, err := Load(filepath.Join(dir, "workflow.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return dir, wf
}

func TestAuthoringAssetProvenance(t *testing.T) {
	dir, wf := moduleAssetFixture(t)
	_, assets, err := PrepareAuthoring(wf, "")
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	var helperDigest string
	owners := map[string]bool{}
	for _, b := range assets.Manifest().Bindings {
		if b.Owner == "" {
			continue
		}
		owners[b.Owner] = true
		if b.Declaring != filepath.Join(dir, "modules/deep/b.toml") {
			t.Fatalf("declaring %s: %+v", b.Owner, b)
		}
		if b.Kind == AssetScript {
			if b.Reference != "root.sh" || b.Source != filepath.Join(canonical, "root.sh") {
				t.Fatalf("script provenance: %+v", b)
			}
		} else {
			if b.Anchor != filepath.Join(canonical, "modules/deep") && b.Kind != AssetSkill {
				t.Fatalf("asset anchor: %+v", b)
			}
			if filepath.IsAbs(b.Reference) {
				t.Fatalf("lost original reference: %+v", b)
			}
		}
		if b.Kind == AssetValidationSchema && b.Reference != "validation.json" {
			t.Fatalf("schema reference: %+v", b)
		}
		if b.Kind == AssetSkill && strings.HasSuffix(b.Relative, ".sh") {
			if helperDigest != "" && b.SHA256 != helperDigest {
				t.Fatal("identical content not deduplicated")
			}
			helperDigest = b.SHA256
		}
		t.Logf("owner=%s kind=%s ref=%s relative=%s sha256=%s", b.Owner, b.Kind, b.Reference, b.Relative, b.SHA256)
	}
	if len(owners) != 6 || helperDigest == "" {
		t.Fatalf("owners=%d helper=%s", len(owners), helperDigest)
	}
	for _, s := range wf.Steps {
		if s.Validate != nil && s.Validate.OutputSchema != filepath.Join(dir, "modules/deep/validation.json") {
			t.Fatal("validation path not rebased")
		}
	}
}

func TestModuleAuthoringCaptureTiming(t *testing.T) {
	dir, wf := moduleAssetFixture(t)
	deep := filepath.Join(dir, "modules/deep")
	for path, body := range map[string]string{"skill/SKILL.md": "---\nname: fixture\ndescription: Synthetic\n---\nEDITED prompt\n", "skill/helper.sh": "EDITED helper\n", "template.md": "EDITED template", "schema.json": `{"type":"object","properties":{"edited":{"type":"string"}}}`, "validation.json": "EDITED validation"} {
		if err := os.WriteFile(filepath.Join(deep, path), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "root.sh"), []byte("echo EDITED\n"), 0700); err != nil {
		t.Fatal(err)
	}
	owned, a, err := PrepareAuthoring(wf, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range owned.Steps {
		if strings.HasSuffix(s.ID, "__agent") {
			if !strings.Contains(s.AgentPrompt(), "ORIGINAL") || !strings.Contains(s.OutputTemplateBody(), "ORIGINAL") || s.Schema.Fields[0].Name != "original" {
				t.Fatal("load snapshot changed")
			}
			raw, _ := a.Bytes(s.ID, AssetValidationSchema)
			if string(raw) != "EDITED validation" {
				t.Fatal("validation not Start-captured")
			}
		}
		if s.Type == StepCommand {
			raw, _ := a.Bytes(s.ID, AssetScript)
			if string(raw) != "echo EDITED\n" {
				t.Fatal("script not Start-captured")
			}
		}
	}
	fresh, err := Load(filepath.Join(dir, "workflow.toml"))
	if err != nil {
		t.Fatal(err)
	}
	fresh, _, err = PrepareAuthoring(fresh, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fresh.Steps[0].AgentPrompt(), "EDITED") || fresh.Steps[0].Schema.Fields[0].Name != "edited" {
		t.Fatal("reload did not see edit")
	}
	// Returned copies and the caller's pointer-bearing graph cannot mutate authority.
	wf.Steps[0].Schema.Fields[0].Name = "CALLER"
	wf.Steps[0].Validate.OutputSchema = "replacement"
	if owned.Steps[0].Schema.Fields[0].Name != "original" || strings.Contains(owned.Steps[0].Validate.OutputSchema, "replacement") {
		t.Fatal("graph alias")
	}
}

func TestModuleAuthoringRejectsAmbiguousBindings(t *testing.T) {
	_, wf := moduleAssetFixture(t)
	wf.Steps[0].assets = append(wf.Steps[0].assets, wf.Steps[0].assets[0])
	if _, _, err := PrepareAuthoring(wf, ""); !errors.Is(err, CaptureIntegrity) {
		t.Fatalf("duplicate: %v", err)
	}
	dir, wf := moduleAssetFixture(t)
	_, a, err := PrepareAuthoring(wf, "")
	if err != nil {
		t.Fatal(err)
	}
	manifest := a.Manifest()
	blobs := map[string][]byte{}
	for _, b := range manifest.Blobs {
		blobs[b.SHA256], _ = a.Blob(b.SHA256)
	}
	for _, b := range manifest.Bindings {
		if b.Owner == wf.Steps[0].ID && b.Kind == AssetSchema {
			b.Reference = "other-schema.json"
			manifest.Bindings = append(manifest.Bindings, b)
			break
		}
	}
	if _, err := RestoreAuthoring(wf, manifest, blobs); !errors.Is(err, CaptureIntegrity) {
		t.Fatalf("conflicting singleton: %v", err)
	}
	wf.Steps[0].Validate.outputSchemaReference = ".env"
	wf.Steps[0].Validate.OutputSchema = ".env"
	if err := os.WriteFile(filepath.Join(dir, "modules/deep/.env"), []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PrepareAuthoring(wf, ""); !errors.Is(err, CaptureForbidden) {
		t.Fatalf("forbidden: %v", err)
	}
}
