package workflow

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// LoadMeta reads only a file's [workflow] table, skipping the full parse and
// validation Load performs. It exists so a caller listing many files (e.g. the
// TUI workflow picker) can cheaply get each file's name/description without
// paying for step decoding, agent-file resolution, or file-existence checks —
// and without treating a structurally-invalid workflow as unlistable.
//
// ok is false when the file has no [workflow] table with a non-empty name,
// which is how the picker decides a .toml is a workflow at all.
func LoadMeta(path string) (meta Meta, ok bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Meta{}, false, err
	}
	return DecodeMeta(string(data))
}

// DecodeMeta is the string-based core of LoadMeta, split out so it can be
// tested without touching the filesystem. It decodes into a struct carrying
// only Meta, so unrelated tables and malformed steps don't cause a failure —
// the point is a tolerant peek, not validation.
func DecodeMeta(data string) (meta Meta, ok bool, err error) {
	var doc struct {
		Meta Meta `toml:"workflow"`
	}
	if _, err := toml.Decode(data, &doc); err != nil {
		return Meta{}, false, fmt.Errorf("parse workflow meta: %w", err)
	}
	if doc.Meta.Name == "" {
		return Meta{}, false, nil
	}
	return doc.Meta, true, nil
}
