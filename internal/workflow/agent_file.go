package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// agentFile is the parsed form of a Claude Code agent definition file: YAML-ish
// frontmatter (name/description/tools/model) followed by a system-prompt body.
// jig reads it so an `agent` step can be driven by an existing agent file
// instead of a skill directory — an agent file is really just a bundled
// (prompt, tools, model) triple.
type agentFile struct {
	Name        string
	Description string
	Tools       []string
	Model       string
	Prompt      string // the body after the frontmatter
}

// resolveAgentFiles reads and parses every agent step's `agent_file`, folding
// the file's tools/model into the step when the step leaves them unset (explicit
// step fields win) and stashing the body as the step's system prompt. It runs
// before applyDefaults so file-derived tools drive worktree isolation and a
// file-derived model outranks [defaults]. baseDir == "" (structural-only mode)
// skips file reads, mirroring how schema_file is handled.
func (wf *Workflow) resolveAgentFiles(baseDir string) error {
	if baseDir == "" {
		return nil
	}
	for i := range wf.Steps {
		s := &wf.Steps[i]
		if s.AgentFile == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(baseDir, s.AgentFile))
		if err != nil {
			return fmt.Errorf("agent step %q: agent_file %q not found", s.ID, s.AgentFile)
		}
		af, err := parseAgentFile(data)
		if err != nil {
			return fmt.Errorf("agent step %q: agent_file %q is invalid: %w", s.ID, s.AgentFile, err)
		}
		s.agentPrompt = af.Prompt
		if len(s.AllowedTools) == 0 {
			s.AllowedTools = af.Tools
		}
		if s.Model == "" {
			s.Model = af.Model
		}
	}
	return nil
}

// resolveOutputTemplates reads each agent step's output_template file and stores
// the body in outputTemplateBody. baseDir == "" skips file reads (structural-only
// mode), mirroring the resolveAgentFiles pattern.
func (wf *Workflow) resolveOutputTemplates(baseDir string) error {
	if baseDir == "" {
		return nil
	}
	for i := range wf.Steps {
		s := &wf.Steps[i]
		if s.OutputTemplate == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(baseDir, s.OutputTemplate))
		if err != nil {
			return fmt.Errorf("agent step %q: output_template %q not found", s.ID, s.OutputTemplate)
		}
		s.outputTemplateBody = strings.TrimSpace(string(data))
	}
	return nil
}

// parseAgentFile splits a Claude agent file into its frontmatter fields and body.
// The frontmatter is the small flat subset jig needs (name/description/tools/
// model); unknown keys are ignored so richer agent files still load. `tools`
// accepts a comma list ("Read, Grep") or an inline array ("[Read, Grep]").
func parseAgentFile(data []byte) (*agentFile, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")

	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i >= len(lines) || strings.TrimSpace(lines[i]) != "---" {
		return nil, fmt.Errorf("missing YAML frontmatter (expected a leading '---')")
	}
	i++
	start := i
	for i < len(lines) && strings.TrimSpace(lines[i]) != "---" {
		i++
	}
	if i >= len(lines) {
		return nil, fmt.Errorf("unterminated frontmatter (missing closing '---')")
	}

	af := &agentFile{Prompt: strings.TrimSpace(strings.Join(lines[i+1:], "\n"))}
	for _, ln := range lines[start:i] {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, val, ok := strings.Cut(trimmed, ":")
		if !ok {
			return nil, fmt.Errorf("frontmatter line %q is not `key: value`", trimmed)
		}
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		switch strings.TrimSpace(key) {
		case "name":
			af.Name = val
		case "description":
			af.Description = val
		case "model":
			af.Model = val
		case "tools":
			af.Tools = parseToolList(val)
		}
	}
	return af, nil
}

// ParseAgentFileContent parses a Claude agent .md file and returns its model
// (empty string when unset) and prompt body. Exported for use by the runner's
// monitor dispatcher, which needs to read monitor agent files directly.
func ParseAgentFileContent(data []byte) (model, prompt string, err error) {
	af, err := parseAgentFile(data)
	if err != nil {
		return "", "", err
	}
	return af.Model, af.Prompt, nil
}

// parseToolList reads a frontmatter tool list, accepting both "Read, Grep, Glob"
// and "[Read, Grep, Glob]".
func parseToolList(v string) []string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "[")
	v = strings.TrimSuffix(v, "]")
	if v == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if t := strings.Trim(strings.TrimSpace(p), `"'`); t != "" {
			out = append(out, t)
		}
	}
	return out
}
