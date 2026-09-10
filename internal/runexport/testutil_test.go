package runexport

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

const fixtureRunID = "run-1"

func makeRunStore(t *testing.T) (root, runDir string) {
	t.Helper()
	root = t.TempDir()
	runDir = filepath.Join(root, "runs", fixtureRunID)
	if err := os.MkdirAll(filepath.Join(runDir, "steps", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"journal.jsonl": "{\"sequence\":1,\"private\":\"SYNTHETIC_TOKEN_DO_NOT_SHARE\"}\n",
		"workflow.json": "{\"toml\":\"private workflow source\"}",
		filepath.Join("steps", "agent", "transcript.jsonl"): "{\"role\":\"assistant\",\"content\":\"private transcript\"}\n",
	} {
		if err := os.WriteFile(filepath.Join(runDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, runDir
}

func payloadHash(t *testing.T, runDir string) string {
	t.Helper()
	h := sha256.New()
	for _, name := range []string{"journal.jsonl", "workflow.json", filepath.Join("steps", "agent", "transcript.jsonl")} {
		data, err := os.ReadFile(filepath.Join(runDir, name))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
}
