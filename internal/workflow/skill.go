package workflow

import (
	"fmt"
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

		data, err := wf.readSkill(s, baseDir)
		if err != nil {
			return assetCaptureError(s.ID, AssetSkill, err)
		}
		prompt, err := parseSkillFile(data)
		if err != nil {
			return assetCaptureError(s.ID, AssetSkill, CaptureIntegrity)
		}
		s.agentPrompt = prompt
		s.SnapshotAgentPrompt = prompt
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
