package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

func loadNotificationProfiles(baseDir string) (map[string]NotificationConfig, error) {
	profiles := map[string]NotificationConfig{}
	if baseDir == "" {
		return profiles, nil
	}
	root := RepoRoot(baseDir)
	if root == "" {
		root = baseDir
	}
	dir := filepath.Join(root, ".agents", "jig", "notification-profiles")
	entries, err := os.ReadDir(dir) // ReadDir returns names in sorted order.
	if os.IsNotExist(err) {
		return profiles, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read notification profiles: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read notification profile %q: %w", entry.Name(), err)
		}
		var file struct {
			Notifications []struct {
				ID     string              `toml:"id"`
				Events []NotificationEvent `toml:"events"`
				Routes []NotificationRoute `toml:"routes"`
			} `toml:"notification"`
		}
		md, err := toml.Decode(string(data), &file)
		if err != nil {
			return nil, fmt.Errorf("parse notification profile %q: %w", entry.Name(), err)
		}
		if keys := md.Undecoded(); len(keys) > 0 {
			return nil, fmt.Errorf("notification profile %q: unknown keys: %s", entry.Name(), formatKeys(keys))
		}
		for _, p := range file.Notifications {
			if !strings.HasPrefix(p.ID, "@") || !isIdent(strings.TrimPrefix(p.ID, "@")) {
				return nil, fmt.Errorf("invalid notification profile id %q", p.ID)
			}
			if _, ok := profiles[p.ID]; ok {
				return nil, fmt.Errorf("duplicate notification profile id %q", p.ID)
			}
			raw := NotificationConfig{Events: p.Events, Routes: p.Routes}
			if _, err := resolveNotificationPolicy(raw, NotificationConfig{}); err != nil {
				return nil, fmt.Errorf("notification profile %q: %w", p.ID, err)
			}
			profiles[p.ID] = raw
		}
	}
	return profiles, nil
}
