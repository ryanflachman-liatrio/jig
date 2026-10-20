package workflow

import "strings"

func (v *validator) checkWorkspaceRequirements(s *Step) {
	seen := map[string]bool{}
	for _, requirement := range s.WorkspaceRequirements {
		switch requirement {
		case "source_history", "submodules", "lfs":
		default:
			v.errf("step %q: unknown workspace_requirements value %q", s.ID, requirement)
		}
		if seen[requirement] {
			v.errf("step %q: duplicate workspace_requirements value %q", s.ID, requirement)
		}
		seen[requirement] = true
	}
}

// InputEnvironmentName is the common command input binding convention. Callers
// prepend JIG_INPUT_; validation rejects collisions before file delivery.
func InputEnvironmentName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func (v *validator) checkContainerFileInputs(s *Step) {
	if s.Type != StepCommand || s.ResolvedSandbox().Profile == nil {
		return
	}
	aliases := map[string]bool{}
	for _, in := range s.Inputs {
		name := InputEnvironmentName(in.As)
		if strings.TrimSpace(in.As) == "" || len(in.As) > 128 {
			v.errf("step %q: Docker file input requires a nonempty alias of at most 128 bytes", s.ID)
		}
		if aliases[name] {
			v.errf("step %q: Docker file input aliases collide at JIG_INPUT_%s", s.ID, name)
		}
		aliases[name] = true
		if s.ForEach != nil && name == InputEnvironmentName(s.ForEach.As) {
			v.errf("step %q: Docker file input alias collides with foreach binding", s.ID)
		}
		if in.Ref == "" || in.Artifact != "" || len(in.RefField) != 0 || in.Inline || in.From != "" || in.Path != "" || in.Once {
			v.errf("step %q: Docker file input requires a named whole command output reference", s.ID)
			continue
		}
		if index, ok := v.wf.index[in.Ref]; ok {
			producer := &v.wf.Steps[index]
			if producer.Type != StepCommand || producer.Output == "" || producer.Schema != nil || producer.OutputType.Kind != OutputText || producer.ForEach != nil {
				v.errf("step %q: Docker file input @%s requires a single declared command file output", s.ID, in.Ref)
			}
		}
	}
}
