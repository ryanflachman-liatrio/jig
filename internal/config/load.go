package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Fixed reason codes deliberately discard parser and filesystem errors:
// those errors can embed arbitrary local file content, including
// credentials. As a narrow, deliberate exception to that discard-everything
// rule, callers wrap the sentinel with the offending file's path (not its
// contents) via %w, so a user with two possible config files can tell which
// one failed. See internal/notification/config.go for the precedent this
// mirrors.
var (
	ErrConfigUnreadable = errors.New("config_unreadable")
	ErrConfigInvalid    = errors.New("config_invalid")
)

// FileName is the config file name under both the user and project config
// directories.
const FileName = "config.toml"

// UserConfigPath resolves the default user-level config file path:
// $XDG_CONFIG_HOME/jig/config.toml, falling back to ~/.config/jig/config.toml
// when XDG_CONFIG_HOME is unset, matching XDG convention used by yazi and
// lazygit.
func UserConfigPath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "jig", FileName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "jig", FileName), nil
}

// ProjectConfigPath resolves the project-level config file path relative to
// root, which is already the .jig directory itself in every existing
// caller (Runtime.root, runRun's --root), matching prefs.Path's join
// convention (and notification.LoadLocalConfig's, before Unit 3 folded that
// disk read into this package). An empty root means persistence-off: this
// returns "" without joining, so callers never construct an unintended
// relative path (AGENTS.md).
func ProjectConfigPath(root string) string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, FileName)
}

// loadFile reads and parses the TOML file at path into a Config. A missing
// file or an empty path (persistence-off, or a layer with no file) is "use
// zero value for this layer," not an error. A present file that cannot be
// read or fails to parse/validate returns a sanitized error wrapping the
// fixed sentinel with path via %w; the underlying parser/filesystem error is
// discarded, never wrapped or logged.
func loadFile(path string) (Config, error) {
	if path == "" {
		return Config{}, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("%w: %s", ErrConfigUnreadable, path)
	}
	var cfg Config
	md, err := toml.Decode(string(data), &cfg)
	if err != nil || len(md.Undecoded()) > 0 {
		return Config{}, fmt.Errorf("%w: %s", ErrConfigInvalid, path)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("%w: %s", ErrConfigInvalid, path)
	}
	return cfg, nil
}

// validate checks schema-level constraints across every table. Each unit
// that adds fields with a restricted domain (e.g. [ui] glyph_preset) extends
// this method.
func (c Config) validate() error {
	if c.UI.GlyphPreset != "" && c.UI.GlyphPreset != "ascii" && c.UI.GlyphPreset != "unicode" {
		return ErrConfigInvalid
	}
	if err := c.Notifications.validate(); err != nil {
		return err
	}
	return nil
}
