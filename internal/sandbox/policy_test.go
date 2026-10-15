package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func policyFixture(t *testing.T) (Selection, string) {
	t.Helper()
	root := t.TempDir()
	in := Selection{Cwd: root, Project: filepath.Join(root, "project"), Home: root, ExplicitPath: filepath.Join(root, "policy.toml")}
	if err := os.Mkdir(in.Project, 0700); err != nil {
		t.Fatal(err)
	}
	return in, fmt.Sprintf(`version = 1
allow_host = true
[docker]
allowed_endpoints = ["unix:///synthetic/docker.sock"]
allowed_repositories = ["alpine"]
allowed_images = []
allowed_networks = ["none"]
workspace_roots = [%q]
storage_roots = [%q]
authentication = "none"
[docker.defaults]
cpus = 1
memory = "512MiB"
pids = 64
[docker.ceilings]
cpus = 2
memory = "2GiB"
pids = 256
`, in.Project, root)
}
func loadFixture(t *testing.T, in Selection, data string) *Policy {
	t.Helper()
	if err := os.WriteFile(in.ExplicitPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Load(in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func dockerStep(t *testing.T) *workflow.Step {
	t.Helper()
	wf, err := workflow.Decode(`[workflow]
name = "synthetic-policy"
version = "1"
[[sandbox]]
name = "box"
runtime = "docker"
image = "alpine"
[[step]]
id = "work"
type = "command"
run = "true"
sandbox = "box"
`, "")
	if err != nil {
		t.Fatal(err)
	}
	return &wf.Steps[0]
}
func requireReason(t *testing.T, err error, reason, field string) {
	t.Helper()
	var f *Failure
	if !errors.As(err, &f) || f.Reason != reason || field != "" && f.Field != field {
		t.Fatalf("got %v; want %s/%s", err, reason, field)
	}
	if strings.Contains(err.Error(), "PRIVATE_SENTINEL") {
		t.Fatal("input leaked")
	}
}
func TestD04PolicyDiscovery(t *testing.T) {
	in, _ := policyFixture(t)
	in.ExplicitPath = ""
	p, err := Load(in)
	if err != nil || p.Source().Kind != "builtin" {
		t.Fatalf("absence: %v %v", p, err)
	}
	if err := p.Authorize(&workflow.Step{ID: "host"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	requireReason(t, p.Authorize(dockerStep(t), nil, nil), "sandbox_policy_denied", "docker")
	in.ExplicitPath = "missing.toml"
	_, err = Load(in)
	requireReason(t, err, "sandbox_policy_unavailable", "source")
	in.ExplicitPath = ""
	in.XDGConfigHome = "relative"
	_, err = Load(in)
	requireReason(t, err, "sandbox_policy_invalid", "xdg_config_home")
	in.XDGConfigHome = in.Project
	_, err = Load(in)
	requireReason(t, err, "sandbox_policy_unsafe", "source")
	in.XDGConfigHome = filepath.Join(in.Home, "config-link")
	if err := os.Symlink(in.Project, in.XDGConfigHome); err != nil {
		t.Fatal(err)
	}
	_, err = Load(in)
	requireReason(t, err, "sandbox_policy_unsafe", "source")
	in.ExplicitPath = filepath.Join(in.Project, "explicit.toml")
	p = loadFixture(t, in, "version = 1\nallow_host = false\n")
	requireReason(t, p.Authorize(&workflow.Step{ID: "host"}, nil, nil), "sandbox_policy_denied", "allow_host")
}
func TestD04PolicySafeRead(t *testing.T) {
	for _, kind := range []string{"writable-file", "writable-parent", "symlink", "directory", "oversize", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			in, data := policyFixture(t)
			if err := os.WriteFile(in.ExplicitPath, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "writable-file":
				if err := os.Chmod(in.ExplicitPath, 0666); err != nil {
					t.Fatal(err)
				}
			case "writable-parent":
				if err := os.Chmod(in.Cwd, 0777); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := in.ExplicitPath
				in.ExplicitPath += ".link"
				if err := os.Symlink(target, in.ExplicitPath); err != nil {
					t.Fatal(err)
				}
			case "directory":
				in.ExplicitPath = in.Project
			case "oversize":
				if err := os.WriteFile(in.ExplicitPath, []byte(strings.Repeat("#", policyLimit+1)), 0600); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := os.WriteFile(in.ExplicitPath, []byte("PRIVATE_SENTINEL = ["), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := Load(in)
			if err == nil {
				t.Fatal("unsafe input allowed")
			}
			if strings.Contains(err.Error(), "PRIVATE_SENTINEL") {
				t.Fatal("raw parser error leaked")
			}
		})
	}
}
func TestD04PolicyStrictShape(t *testing.T) {
	for _, tc := range []struct{ name, old, new string }{
		{"version", "version = 1", "version = 2"}, {"missing-host", "allow_host = true", ""},
		{"duplicate-key", "allow_host = true", "allow_host = true\nallow_host = false"},
		{"unknown", "allow_host = true", "allow_host = true\nPRIVATE_SENTINEL = true"},
		{"missing-list", "allowed_images = []", ""}, {"duplicate-list", "[\"none\"]", "[\"none\",\"none\"]"},
		{"duplicate-normalized", "[\"alpine\"]", "[\"alpine\",\"docker.io/library/alpine\"]"},
		{"wrong-type", "allow_host = true", "allow_host = 1"},
		{"empty-auth", "authentication = \"none\"", "authentication = \"\""},
		{"zero-cpu", "cpus = 1", "cpus = 0"}, {"fraction-cpu", "cpus = 1", "cpus = 0.0000000001"},
		{"small-memory", "memory = \"512MiB\"", "memory = \"5MiB\""},
		{"pids-overflow", "pids = 64", "pids = 2147483648"},
		{"missing-ceiling", "pids = 256", ""}, {"default-over-ceiling", "cpus = 1", "cpus = 3"},
		{"zero-storage", "allow_host = true", "allow_host = true\nhard_storage_bytes = 0"},
		{"negative-storage", "allow_host = true", "allow_host = true\nhard_storage_bytes = -1"},
		{"wildcard-image", "allowed_images = []", "allowed_images = [\"alpine:*\"]"},
		{"tagged-repository", "[\"alpine\"]", "[\"alpine:latest\"]"},
		{"invalid-network", "[\"none\"]", "[\"host\"]"},
		{"remote-endpoint", "unix:///synthetic/docker.sock", "tcp://PRIVATE_SENTINEL:1234"},
		{"list-bound", "allowed_images = []", "allowed_images = [" + strings.Repeat("\"alpine\",", 257) + "]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, data := policyFixture(t)
			data = strings.Replace(data, tc.old, tc.new, 1)
			if err := os.WriteFile(in.ExplicitPath, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(in)
			requireReason(t, err, "sandbox_policy_invalid", "")
		})
	}
}
func TestD04PolicyDecisions(t *testing.T) {
	for _, tc := range []struct{ name, old, new, reason, field string }{
		{"images-empty", "[\"alpine\"]", "[]", "sandbox_policy_denied", "docker.allowed_images"},
		{"exact-repository", "[\"alpine\"]", "[\"alpine-other\"]", "sandbox_policy_denied", "docker.allowed_images"},
		{"digest", "allowed_images = []", "allowed_images = []\nrequire_digest = true", "sandbox_policy_denied", "docker.require_digest"},
		{"network-empty", "[\"none\"]", "[]", "sandbox_policy_denied", "docker.allowed_networks"},
		{"auth", "authentication = \"none\"", "authentication = \"PRIVATE_SENTINEL\"", "sandbox_authentication_unsupported", "docker.authentication"},
		{"all-agents", "allow_host = true", "allow_host = true\nrequire_all_agents = true", "sandbox_guarantee_unsupported", "require_all_agents"},
		{"hard-storage", "allow_host = true", "allow_host = true\nhard_storage_bytes = 1", "sandbox_guarantee_unsupported", "hard_storage_bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, data := policyFixture(t)
			p := loadFixture(t, in, strings.Replace(data, tc.old, tc.new, 1))
			requireReason(t, p.Authorize(dockerStep(t), nil, nil), tc.reason, tc.field)
		})
	}
	in, data := policyFixture(t)
	p := loadFixture(t, in, data)
	st := dockerStep(t)
	got, err := p.Evaluate(st, []string{in.Project}, []string{"", filepath.Join(in.Cwd, "absent", "child")}, nil)
	if err != nil || got.NanoCPUs != 1e9 || got.MemoryBytes != 512<<20 || got.PIDs != 64 || got.CPUSource != "policy_default" {
		t.Fatalf("defaults: %+v %v", got, err)
	}
	if st.SnapshotSandbox.Profile.CPUs != nil {
		t.Fatal("mutated authored omission")
	}
	cpu := workflow.SandboxCPU(3e9)
	st.SnapshotSandbox.Profile.CPUs = &cpu
	requireReason(t, p.Authorize(st, nil, nil), "sandbox_policy_denied", "docker.ceilings.cpus")
	if *st.SnapshotSandbox.Profile.CPUs != 3e9 {
		t.Fatal("clamped request")
	}
	st = dockerStep(t)
	st.Secrets = []string{"PRIVATE_SENTINEL"}
	requireReason(t, p.Authorize(st, nil, nil), "sandbox_authentication_unsupported", "docker.authentication")
	st.Secrets = nil
	st.Type = workflow.StepAgent
	requireReason(t, p.Authorize(st, nil, nil), "sandbox_authentication_unsupported", "docker.authentication")
	st = dockerStep(t)
	p2 := loadFixture(t, in, strings.Replace(data, "cpus = 1", "cpus = 2", 1))
	locked, err := p2.Evaluate(st, nil, nil, &got)
	if err != nil || locked.NanoCPUs != 1e9 {
		t.Fatalf("re-defaulted: %+v %v", locked, err)
	}
	got.NanoCPUs = 3e9
	requireReason(t, func() error { _, err := p2.Evaluate(st, nil, nil, &got); return err }(), "sandbox_policy_denied", "docker.ceilings.cpus")
	if p.Fingerprint() == p2.Fingerprint() {
		t.Fatal("changed policy hash unchanged")
	}
	endpoints := p.Endpoints()
	endpoints[0] = "changed"
	if p.Endpoints()[0] == "changed" {
		t.Fatal("mutable policy")
	}
	if len(p.Fingerprint()) != 64 {
		t.Fatal("missing digest")
	}
}
func TestD04PolicyPaths(t *testing.T) {
	in, data := policyFixture(t)
	p := loadFixture(t, in, data)
	st := dockerStep(t)
	for _, path := range []string{in.Project + "-escape", filepath.Join(in.Cwd, "outside"), "relative"} {
		requireReason(t, p.Authorize(st, []string{path}, nil), "sandbox_policy_denied", "docker.workspace_roots")
	}
	if err := os.Symlink(in.Cwd, filepath.Join(in.Project, "escape")); err != nil {
		t.Fatal(err)
	}
	requireReason(t, p.Authorize(st, []string{filepath.Join(in.Project, "escape", "missing")}, nil), "sandbox_policy_denied", "docker.workspace_roots")
	requireReason(t, p.Authorize(st, nil, []string{in.Cwd + "-escape"}), "sandbox_policy_denied", "docker.storage_roots")
	if err := p.Authorize(&workflow.Step{ID: "host"}, []string{"relative"}, []string{"relative"}); err != nil {
		t.Fatal("host filesystem incorrectly constrained", err)
	}
	if len(Display(strings.Repeat("é", 300))) > 512 || strings.ContainsAny(Display("a\n\x1b"), "\n\x1b") {
		t.Fatal("unsafe display")
	}
}

func TestD04PolicyDiscoveryAndCanonicalFingerprint(t *testing.T) {
	in, data := policyFixture(t)
	p := loadFixture(t, in, data)
	equivalent := strings.Replace(data, "[\"alpine\"]", "[\"docker.io/library/alpine\"]", 1)
	if q := loadFixture(t, in, equivalent); q.Fingerprint() != p.Fingerprint() {
		t.Fatal("equivalent policy changed fingerprint")
	}
	for _, xdg := range []bool{false, true} {
		implicit := in
		implicit.ExplicitPath = ""
		root := filepath.Join(in.Home, ".config")
		if xdg {
			root = filepath.Join(in.Home, "xdg")
			implicit.XDGConfigHome = root
		}
		path := filepath.Join(root, "jig", "sandbox-policy.toml")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("version=1\nallow_host=false\n"), 0600); err != nil {
			t.Fatal(err)
		}
		q, err := Load(implicit)
		if err != nil || q.Source().Kind != "implicit" {
			t.Fatalf("discovery=%v %v", q, err)
		}
		requireReason(t, q.Authorize(&workflow.Step{ID: "host"}, nil, nil), "sandbox_policy_denied", "allow_host")
		requireReason(t, q.Authorize(&workflow.Step{ID: "host"}, []string{root}, nil), "sandbox_policy_unsafe", "source")
	}
	data += strings.Repeat("#", policyLimit-len(data))
	p = loadFixture(t, in, data)
	if p.Fingerprint() == "" {
		t.Fatal("exact byte bound rejected")
	}
	requireReason(t, (&Policy{}).Authorize(&workflow.Step{ID: "host"}, nil, nil), "sandbox_policy_invalid", "document")
}
