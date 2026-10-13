//go:build darwin || linux

package workflow

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// This test-only load seam derives the prompt from the very bytes captured in
// the bundle. It deliberately does not call resolveSkills for a second read.
// Production Load is unchanged until the full P3 gate is satisfied.
func p3LoadSkillProbe(project string) (*Workflow, map[string]p3Capture, *p3Ledger, error) {
	root, err := p3Pin(project)
	if err != nil {
		return nil, nil, nil, err
	}
	defer root.Close()
	ledger := new(p3Ledger)
	source, err := ledger.capture(root, "workflow.toml", nil)
	if err != nil {
		return nil, nil, nil, err
	}
	wf, err := Decode(string(source.bytes), "")
	if err != nil {
		return nil, nil, nil, err
	}
	if len(wf.Steps) != 1 || wf.Steps[0].Skill == "" {
		return nil, nil, nil, p3Integrity
	}
	skillRoot, err := p3SelectBundle(filepath.Join(project, wf.Steps[0].Skill), project)
	if err != nil {
		return nil, nil, nil, err
	}
	bundle, err := ledger.bundle(skillRoot)
	closeErr := skillRoot.Close()
	if err != nil {
		return nil, nil, nil, err
	}
	if closeErr != nil {
		return nil, nil, nil, p3Storage
	}
	prompt, err := parseSkillFile(bundle["SKILL.md"].bytes)
	if err != nil {
		return nil, nil, nil, err
	}
	wf.Steps[0].agentPrompt = prompt
	wf.Steps[0].SnapshotAgentPrompt = prompt
	return wf, bundle, ledger, nil
}

func TestAuthoringAssetPolicyBundleSnapshot(t *testing.T) {
	project := t.TempDir()
	source := []byte("[workflow]\nname='bundle-probe'\nversion='1'\n[[step]]\nid='agent'\ntype='agent'\nskill='skills/with spaces'\n")
	prompt := []byte("---\nname: probe\ndescription: synthetic capture probe\n---\nRead {{skill_dir}}/nested/helper.txt. ORIGINAL\n")
	p3Write(t, project, "workflow.toml", source)
	p3Write(t, project, "skills/with spaces/SKILL.md", prompt)
	p3Write(t, project, "skills/with spaces/nested/helper.txt", []byte("ORIGINAL"))
	p3Write(t, project, "skills/neighbor/do-not-discover", []byte("SIBLING"))
	helper := filepath.Join(project, "skills/with spaces/nested/helper.txt")
	if err := os.Chmod(helper, 0700); err != nil {
		t.Fatal(err)
	}
	wf, bundle, ledger, err := p3LoadSkillProbe(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle) != 2 || ledger.bindings != 3 || len(ledger.blobs) != 3 {
		t.Fatalf("bundle=%d bindings=%d blobs=%d", len(bundle), ledger.bindings, len(ledger.blobs))
	}
	got := bundle["nested/helper.txt"]
	const digest = "962f85a27854336f3286ad54b49321f62525043b5f3508b2ecc887fdd6e7550e"
	if fmt.Sprintf("%x", got.digest) != digest || !got.executable {
		t.Fatalf("helper digest=%x executable=%t", got.digest, got.executable)
	}
	p3Write(t, project, "skills/with spaces/SKILL.md", []byte("EDITED"))
	if err := os.Remove(helper); err != nil {
		t.Fatal(err)
	}
	if wf.Steps[0].AgentPrompt() != "Read {{skill_dir}}/nested/helper.txt. ORIGINAL" || !bytes.Equal(bundle["SKILL.md"].bytes, prompt) || string(got.bytes) != "ORIGINAL" {
		t.Fatal("source edits changed captured prompt or helper")
	}
	got.bytes[0] = 'X'
	if string(ledger.blobs[got.digest]) != "ORIGINAL" {
		t.Fatal("returned bytes alias authority")
	}
	for _, data := range ledger.blobs {
		if bytes.Contains(data, []byte("SIBLING")) {
			t.Fatal("discovered undeclared sibling")
		}
	}
	t.Logf("captured files=2 logical bindings=3 helper SHA-256=%s; source deleted; authority unchanged", digest)
	// Reject the whole bundle, not a partially usable prompt, on an exclusion.
	p3Write(t, project, "skills/with spaces/SKILL.md", prompt)
	p3Write(t, project, "skills/with spaces/.env", []byte("SYNTHETIC_EXCLUDED"))
	wf, bundle, _, err = p3LoadSkillProbe(project)
	if err != p3Forbidden || wf != nil || bundle != nil {
		t.Fatalf("excluded bundle published: wf=%v files=%d err=%v", wf != nil, len(bundle), err)
	}
}
