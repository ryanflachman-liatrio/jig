package config

import (
	"slices"
	"strings"
)

// OpenerConfig describes a literal argv template. Nil means absent; an empty
// Argv explicitly restores the EDITOR fallback. Terminal defaults to true.
type OpenerConfig struct {
	Argv     []string `toml:"argv"`
	Terminal *bool    `toml:"terminal"`
}

func (c OpenerConfig) TerminalOrDefault() bool {
	return c.Terminal == nil || *c.Terminal
}

// Validate returns only a fixed reason, never operator configuration contents.
// A terminal-only overlay is valid independently of lower-layer argv.
func (c OpenerConfig) Validate() error {
	if len(c.Argv) == 0 {
		return nil
	}
	if strings.TrimSpace(c.Argv[0]) == "" {
		return ErrConfigInvalid
	}
	hasPath := false
	for i, arg := range c.Argv {
		if strings.ContainsRune(arg, 0) {
			return ErrConfigInvalid
		}
		if i > 0 && strings.Contains(arg, "{path}") {
			hasPath = true
		}
		if i > 0 {
			arg = strings.NewReplacer("{path}", "", "{line}", "", "{column}", "").Replace(arg)
		}
		if strings.ContainsAny(arg, "{}") {
			return ErrConfigInvalid
		}
	}
	if !hasPath {
		return ErrConfigInvalid
	}
	return nil
}

func mergeOpener(base, overlay OpenerConfig) OpenerConfig {
	out := base
	if overlay.Argv != nil {
		out.Argv = overlay.Argv
	}
	out.Argv = slices.Clone(out.Argv)
	if overlay.Terminal != nil {
		out.Terminal = overlay.Terminal
	}
	if out.Terminal != nil {
		v := *out.Terminal
		out.Terminal = &v
	}
	return out
}
