package workflow

import (
	"fmt"
	"os"
	"path/filepath"
)

// resolveSkills snapshots skill instructions while loading the workflow so
// execution never depends on a backend discovering or rereading authoring
// files from its own filesystem context.
func (wf *Workflow) resolveSkills(baseDir string) error {
	if baseDir == "" {
		return nil
	}
	for i := range wf.Steps {
		s := &wf.Steps[i]
		if s.Skill == "" {
			continue
		}

		dir := filepath.Join(baseDir, s.Skill)
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			return fmt.Errorf("agent step %q: skill dir %q not found", s.ID, s.Skill)
		}
		data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			return fmt.Errorf("agent step %q: %s/SKILL.md not found", s.ID, s.Skill)
		}
		prompt, err := parseSkillFile(data)
		if err != nil {
			return fmt.Errorf("agent step %q: %s/SKILL.md is invalid: %w", s.ID, s.Skill, err)
		}
		s.agentPrompt = prompt
	}
	return nil
}

func parseSkillFile(data []byte) (string, error) {
	parsed, err := parseAgentFile(data)
	if err != nil {
		return "", err
	}
	if parsed.Name == "" {
		return "", fmt.Errorf("frontmatter field `name` is required")
	}
	if parsed.Description == "" {
		return "", fmt.Errorf("frontmatter field `description` is required")
	}
	if parsed.Prompt == "" {
		return "", fmt.Errorf("instruction body is required")
	}
	return parsed.Prompt, nil
}
