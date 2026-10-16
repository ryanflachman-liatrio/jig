package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestD04ReopenPolicySelectionAndDefaults(t *testing.T) {
	in, raw := policyFixture(t)
	p := loadFixture(t, in, raw)
	st := dockerStep(t)
	original, err := p.Evaluate(st, []string{in.Project}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	p = loadFixture(t, in, strings.Replace(raw, "cpus = 1", "cpus = 0.5", 1))
	retained, err := p.Evaluate(st, []string{in.Project}, nil, &original)
	if err != nil || retained.NanoCPUs != original.NanoCPUs || retained.CPUSource != original.CPUSource {
		t.Fatalf("redefaulted: %+v %v", retained, err)
	}
	src := p.Source()
	in.ExplicitPath = ""
	if err := os.Remove(src.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := Reload(in, src); err == nil {
		t.Fatal("lost selected source fell back")
	}
	in.ExplicitPath = filepath.Join(in.Home, "replacement.toml")
	replacement := loadFixture(t, in, raw)
	if _, err := Reload(in, src); err != nil {
		t.Fatal("explicit reselection refused", err)
	}
	if replacement.Fingerprint() == p.Fingerprint() {
		t.Fatal("fixture did not change fingerprint")
	}
	t.Log("default change retains effective values/provenance; source disappearance fails; explicit trusted reselection succeeds")
}
