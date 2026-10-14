package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// Scheduler fixtures formerly used unresolved fake skill names. Give those
// declarations real synthetic authoring files now that Start captures the whole
// graph. Execution remains controlled by each test's fake executor.
func decodeEngineFixture(t *testing.T, source string) (*workflow.Workflow, error) {
	t.Helper()
	wf, err := workflow.Decode(source, "")
	if err != nil {
		return nil, err
	}
	base := t.TempDir()
	for _, s := range wf.Steps {
		if s.Skill != "" {
			dir := filepath.Join(base, s.Skill)
			if err := os.MkdirAll(dir, 0700); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: fixture\ndescription: Synthetic scheduler fixture\n---\nFixture instructions.\n"), 0600); err != nil {
				return nil, err
			}
		}
		if s.AgentFile != "" {
			p := filepath.Join(base, s.AgentFile)
			if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
				return nil, err
			}
			if err := os.WriteFile(p, []byte("---\nname: fixture\ndescription: Synthetic fixture\n---\nFixture instructions.\n"), 0600); err != nil {
				return nil, err
			}
		}
	}
	return workflow.Decode(source, base)
}
