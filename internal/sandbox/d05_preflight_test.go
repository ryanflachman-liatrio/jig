package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// This is metadata preflight, not execution/containment conformance. An explicit
// opt-in with missing configuration fails; default tests never contact Docker.
func TestD05DockerPreflight(t *testing.T) {
	if os.Getenv("JIG_DOCKER_INTEGRATION") != "1" {
		t.Skip("set JIG_DOCKER_INTEGRATION=1 for local Docker metadata preflight")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	policyPath := os.Getenv("JIG_D05_TEST_POLICY")
	if !filepath.IsAbs(policyPath) {
		t.Fatal("JIG_D05_TEST_POLICY must name an explicit absolute operator policy")
	}
	policy, err := Load(Selection{ExplicitPath: policyPath, Cwd: root, Project: root})
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.Load(filepath.Join(root, "examples", "docker-build-jig.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := policy.Authorize(&wf.Steps[0], []string{root}, nil); err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := SelectEndpoint(EndpointInput{Cwd: root, Project: root, Home: home, Context: os.Getenv("JIG_D05_TEST_CONTEXT")}, policy)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := Inspect(context.Background(), endpoint, policy, wf.Steps, nil)
	if err != nil {
		t.Fatal(err)
	}
	if observed.Binding == nil || observed.Capabilities == nil || !observed.Capabilities.Ready {
		t.Fatal("required compute capability metadata unavailable")
	}
	for _, image := range observed.Images {
		if image.ID == "" || image.Platform != observed.Binding.NativePlatform || image.Status != "cached_unverified" {
			t.Fatal("explicitly prepare the fixture image before running preflight")
		}
	}
	if len(observed.Images) != 1 {
		t.Fatal("expected exactly one inspected fixture image")
	}
	for _, st := range observed.Steps {
		if st.Reason != "" {
			t.Fatalf("unsupported fixture request: %s", st.Reason)
		}
	}
	t.Logf("negotiated API=%s native=%s enforcement=%s", observed.Binding.API, observed.Binding.NativePlatform, observed.Capabilities.Enforcement)
}
