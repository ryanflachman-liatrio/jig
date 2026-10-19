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

// ProjectConfigPath resolves the project-level config file path inside root,
// the .jig directory itself. An empty root means persistence-off: this
// returns "" without joining, so callers never construct an unintended
// relative path.
func ProjectConfigPath(root string) string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, FileName)
}

// Load resolves and merges every configuration layer, lowest to highest
// precedence: built-in defaults -> user config -> project config. CLI flags
// are the caller's responsibility to apply on top of the returned Config
// (see cmd/jig's glyph-preset resolver, which consumes this Config as a
// fallback rather than letting it override an explicit flag).
//
// userPathOverride, when non-empty, substitutes the resolved user-level
// path (the --config flag). root is the caller's actual persistence root,
// including "" for persistence-off; Load never substitutes a hardcoded
// root itself.
func Load(userPathOverride, root string) (Config, error) {
	userPath := userPathOverride
	if userPath == "" {
		p, err := UserConfigPath()
		if err != nil {
			return Config{}, err
		}
		userPath = p
	}

	userCfg, err := loadFile(userPath)
	if err != nil {
		return Config{}, err
	}

	projectCfg, err := loadFile(ProjectConfigPath(root))
	if err != nil {
		return Config{}, err
	}

	// [telemetry]'s defaults layer comes from env vars, so config.toml
	// layers override env rather than the other way around.
	base := Default()
	base.Telemetry, err = telemetryDefaultsFromEnv()
	if err != nil {
		return Config{}, err
	}

	merged := Merge(base, userCfg)
	merged = Merge(merged, projectCfg)
	merged.Telemetry = applyTelemetryKillSwitch(merged.Telemetry)
	return merged, nil
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
		var sd safeDetailError
		if errors.As(err, &sd) {
			return Config{}, fmt.Errorf("%w: %s: %v", ErrConfigInvalid, path, sd)
		}
		return Config{}, fmt.Errorf("%w: %s", ErrConfigInvalid, path)
	}
	return cfg, nil
}

// safeDetailError marks a validation error whose message names only keys and
// accepted values, never configured values, so loadFile may surface it.
type safeDetailError interface {
	error
	safeDetail()
}

// validate checks schema-level constraints across every table. Each unit
// that adds fields with a restricted domain (e.g. [ui] glyph_preset) extends
// this method.
func (c Config) validate() error {
	if err := c.TUI.Opener.Validate(); err != nil {
		return err
	}
	if c.UI.GlyphPreset != "" && c.UI.GlyphPreset != GlyphPresetASCII && c.UI.GlyphPreset != GlyphPresetUnicode {
		return ErrConfigInvalid
	}
	if err := validateKeys(c.Keys); err != nil {
		return err
	}
	if err := c.Notifications.validate(); err != nil {
		return err
	}
	if err := validateBackend("helpchat.backend", c.HelpChat.Backend); err != nil {
		return err
	}
	return validateBackend("security.monitor_backend", c.Security.MonitorBackend)
}
