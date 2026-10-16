package runexport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func TestD04OfflineSandboxProjection(t *testing.T) { testSandboxProjection(t, false) }
func TestD04ReopenSandboxProjection(t *testing.T)  { testSandboxProjection(t, true) }
func testSandboxProjection(t *testing.T, complete bool) {
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
	if complete {
		policy := &engine.SandboxPolicyState{Source: engine.SandboxPolicySource{Kind: "explicit", Path: "/PRIVATE_POLICY_SOURCE/policy.toml"}, Fingerprint: strings.Repeat("a", 64), Effective: map[string]engine.SandboxEffective{}, Binding: &engine.SandboxBinding{URI: "unix:///PRIVATE_ENDPOINT/docker.sock", EndpointSource: "flag", ConfigRoot: "/PRIVATE_CONFIG_ROOT", DaemonID: "PRIVATE_DAEMON_ID", API: "1.54", NativePlatform: "linux/amd64"}}
		for _, st := range steps {
			e := engine.SandboxEffective{}
			if st.ResolvedSandbox().Profile != nil {
				e = engine.SandboxEffective{NanoCPUs: 1000000000, MemoryBytes: 512 << 20, PIDs: 64, CPUSource: "policy_default", MemorySource: "policy_default", PIDsSource: "policy_default", Platform: "linux/amd64"}
			}
			policy.Effective[st.ID] = e
		}
		payload, _ := json.Marshal(struct {
			Requests map[string]workflow.SandboxRequest
			Policy   *engine.SandboxPolicyState
		}{requests, policy})
		digest := sha256.Sum256(payload)
		put("sandbox_version", 2)
		put("sandbox", map[string]any{"version": 2, "requests": requests, "policy": policy, "sha256": hex.EncodeToString(digest[:])})
		ev := engine.SandboxPolicyBlocked{RunID: fixtureRunName, Epoch: 20, Reason: "sandbox_policy_denied"}
		grantLine, _ := engine.MarshalEnvelope(20, engine.SandboxPolicyCleared{RunID: fixtureRunName, Epoch: 19, Policy: policy})
		line, _ := engine.MarshalEnvelope(21, ev)
		f, err := os.OpenFile(filepath.Join(dir, "journal.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		f.Write(append(grantLine, '\n'))
		f.Write(append(line, '\n'))
		f.Close()
	}

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
			for _, private := range []string{"D04_PRIVATE_ENV_SENTINEL", "private.example.test", "private-profile", "snapshot_sandbox", "PRIVATE_POLICY_SOURCE", "PRIVATE_ENDPOINT", "PRIVATE_CONFIG_ROOT", "PRIVATE_DAEMON_ID"} {
				if bytes.Contains(body, []byte(private)) {
					t.Fatalf("%s exposed %s", name, private)
				}
			}
		}
	}
	t.Log("valid sandbox metadata excluded from decoded archive members and raw ZIP in both export modes")
}
