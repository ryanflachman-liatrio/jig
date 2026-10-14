package workflow

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthoringParseDiagnostics(t *testing.T) {
	for _, kind := range []string{"skill", "agent_file", "schema_file"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			decl := ""
			invalid := "---\nPRIVATE_SOURCE_MARKER\n---\nInstructions.\n"
			switch kind {
			case "skill":
				mustWrite(t, filepath.Join(dir, "PRIVATE_PATH_MARKER/SKILL.md"), invalid)
				decl = "skill='PRIVATE_PATH_MARKER'"
			case "agent_file":
				mustWrite(t, filepath.Join(dir, "PRIVATE_PATH_MARKER.md"), invalid)
				decl = "agent_file='PRIVATE_PATH_MARKER.md'"
			case "schema_file":
				mustWrite(t, filepath.Join(dir, "skill/SKILL.md"), "---\nname: fixture\ndescription: Synthetic\n---\nInstructions.\n")
				mustWrite(t, filepath.Join(dir, "PRIVATE_PATH_MARKER.json"), `{"type":"PRIVATE_SOURCE_MARKER"}`)
				decl = "skill='skill'\nschema_file='PRIVATE_PATH_MARKER.json'"
			}
			path := filepath.Join(dir, "workflow.toml")
			if err := os.WriteFile(path, []byte("[workflow]\nname='diagnostic'\nversion='1'\n[[step]]\nid='worker'\ntype='agent'\n"+decl+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if !errors.Is(err, CaptureIntegrity) {
				t.Fatalf("want typed integrity rejection, got %v", err)
			}
			for _, value := range []string{"PRIVATE_SOURCE_MARKER", "PRIVATE_PATH_MARKER", dir} {
				if strings.Contains(err.Error(), value) {
					t.Fatalf("private value disclosed: %s", err)
				}
			}
			if !strings.Contains(err.Error(), `step "worker"`) || len(err.Error()) > 128 {
				t.Fatalf("missing bounded step context: %s", err)
			}
		})
	}
}
