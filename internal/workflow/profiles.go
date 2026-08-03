package workflow

// builtinProfiles returns the hard-coded profiles shipped with jig. These are
// always available to any workflow without any .agents/jig/profiles/ directory.
//
// Project-local profiles cannot redefine these ids.
func builtinProfiles() []AgentProfile {
	return []AgentProfile{
		{
			// @interactive: the agent may pause mid-run to ask the user a
			// question. AskUserQuestion is injected into AllowedTools so the
			// SDK exposes the tool; the engine intercepts calls to it and
			// surfaces the question in the TUI.
			ID:              "@interactive",
			AskUserQuestion: true,
		},
		{
			// @autonomous: the agent must never pause for human input.
			// AskUserQuestion is explicitly disallowed so the model cannot
			// call it even if it appears in a broader allowlist.
			ID:              "@autonomous",
			DisallowedTools: []string{"AskUserQuestion"},
		},
	}
}

// builtinProfileIDs returns the set of ids reserved by built-in profiles.
func builtinProfileIDs() map[string]bool {
	builtins := builtinProfiles()
	ids := make(map[string]bool, len(builtins))
	for _, p := range builtins {
		ids[p.ID] = true
	}
	return ids
}
