package runexport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func TestD04OfflineSandboxProjection(t *testing.T) {
	root, dir := buildIntactFixture(t)
	path := filepath.Join(dir, "workflow.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	var steps []workflow.Step
	if err := json.Unmarshal(snap["expanded_steps"], &steps); err != nil {
		t.Fatal(err)
	}
	var source, digest string
	if err := json.Unmarshal(snap["source_path"], &source); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(snap["sha256"], &digest); err != nil {
		t.Fatal(err)
	}
	requests := map[string]workflow.SandboxRequest{}
	for i := range steps {
		r := workflow.SandboxRequest{Reference: "host", Origin: "implicit", Source: source, SourceSHA256: digest}
		if i == 0 {
			r.Reference = "private-profile"
			r.Origin = "explicit"
			r.Profile = &workflow.SandboxProfile{Name: r.Reference, Runtime: "docker", Image: "private.example.test/team/image:v1", NormalizedImage: "private.example.test/team/image:v1", Pull: "if_missing", Network: "none", Env: map[string]string{"COLOR": "D04_PRIVATE_ENV_SENTINEL"}}
		}
		steps[i].SnapshotSandbox = &r
		requests[steps[i].ID] = r
	}
	encoded, err := json.Marshal(requests)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	put := func(key string, v any) {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		snap[key] = b
	}
	put("sandbox_version", 1)
	put("sandbox", map[string]any{"version": 1, "requests": requests, "sha256": hex.EncodeToString(sum[:])})
	put("expanded_steps", steps)
	put("public_steps", steps)
	raw, err = json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	// Ensure the test is exporting a valid sandbox snapshot, not merely omitting
	// an unreadable source through an integrity gap.
	if _, err := engine.DecodeWorkflowSnapshot(raw); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, textMode := range []bool{false, true} {
		dest := filepath.Join(t.TempDir(), "bundle.zip")
		if _, err := Export(context.Background(), Options{Root: root, RunID: fixtureRunName, Destination: dest, IncludeText: textMode}); err != nil {
			t.Fatal(err)
		}
		members := readZip(t, dest)
		var manifest Manifest
		if err := json.Unmarshal(members[manifestMember], &manifest); err != nil {
			t.Fatal(err)
		}
		for _, gap := range manifest.Gaps {
			if gap.Source == "workflow" {
				t.Fatalf("workflow was excluded: %+v", gap)
			}
		}
		archive, err := os.ReadFile(dest)
		if err != nil {
			t.Fatal(err)
		}
		members["raw-archive"] = archive
		for name, body := range members {
			for _, private := range []string{"D04_PRIVATE_ENV_SENTINEL", "private.example.test", "private-profile", "snapshot_sandbox"} {
				if bytes.Contains(body, []byte(private)) {
					t.Fatalf("%s exposed %s", name, private)
				}
			}
		}
	}
	t.Log("valid sandbox metadata excluded from decoded archive members and raw ZIP in both export modes")
}
