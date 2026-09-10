package scaffold

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnknownTemplate identifies template selections that a CLI should report
// as usage.
var ErrUnknownTemplate = errors.New("unknown scaffold template")

// Template describes one embedded scaffold. Adding a scaffold should require
// only its assets and one registry entry, not changes to planning or writing.
type Template struct {
	Name        string
	Description string
	AssetDir    string
	SkillRefs   []string
}

var registry = []Template{
	{
		Name:        "minimal",
		Description: "A credential-free command and review workflow.",
		AssetDir:    "templates/minimal",
	},
}

// Lookup returns the named scaffold template.
func Lookup(name string) (Template, error) {
	for _, candidate := range registry {
		if candidate.Name == name {
			return cloneTemplate(candidate), nil
		}
	}

	names := make([]string, len(registry))
	for i, candidate := range registry {
		names[i] = candidate.Name
	}
	return Template{}, fmt.Errorf("%w %q (valid templates: %s)", ErrUnknownTemplate, name, strings.Join(names, ", "))
}

// All returns every scaffold template in display order.
func All() []Template {
	templates := make([]Template, len(registry))
	for i, candidate := range registry {
		templates[i] = cloneTemplate(candidate)
	}
	return templates
}

func cloneTemplate(source Template) Template {
	cloned := source
	cloned.SkillRefs = append([]string(nil), source.SkillRefs...)
	return cloned
}
