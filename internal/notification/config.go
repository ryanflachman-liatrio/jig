// Package notification owns operator notification bindings and local readiness.
// Workflow policy is secret-free; resolved bindings remain process-local.
package notification

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"jig/internal/workflow"
)

type DestinationType string

const (
	Desktop DestinationType = "desktop"
	Slack   DestinationType = "slack"
	Webhook DestinationType = "webhook"
)

type LocalConfig struct {
	Enabled      bool          `toml:"enabled"`
	Destinations []Destination `toml:"destination"`
}

type Destination struct {
	ID           string          `toml:"id"`
	Type         DestinationType `toml:"type"`
	Enabled      bool            `toml:"enabled"`
	URLSecret    *string         `toml:"url_secret"`
	BearerSecret *string         `toml:"bearer_secret"`
}

// Fixed reason codes deliberately discard parser, filesystem and secret errors:
// those errors can embed arbitrary local content, including credentials.
var (
	ErrConfigUnreadable = errors.New("config_unreadable")
	ErrConfigInvalid    = errors.New("config_invalid")
)

// LoadLocalConfig never joins paths for an empty persistence root.
// readFile may be injected for callers without a disk-backed configuration.
func LoadLocalConfig(root string, readFile func(string) ([]byte, error)) (LocalConfig, error) {
	if root == "" {
		return LocalConfig{}, nil
	}
	if readFile == nil {
		readFile = os.ReadFile
	}
	data, err := readFile(filepath.Join(root, "notifications.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return LocalConfig{}, nil
	}
	if err != nil {
		return LocalConfig{}, ErrConfigUnreadable
	}
	return ParseLocalConfig(data)
}

func ParseLocalConfig(data []byte) (LocalConfig, error) {
	var cfg LocalConfig
	md, err := toml.Decode(string(data), &cfg)
	if err != nil || len(md.Undecoded()) > 0 {
		return LocalConfig{}, ErrConfigInvalid
	}
	if err := cfg.validate(); err != nil {
		return LocalConfig{}, err
	}
	return cfg, nil
}

func (cfg LocalConfig) validate() error {
	aliases := map[string]bool{}
	types := map[DestinationType]bool{}
	for _, d := range cfg.Destinations {
		if !workflow.ValidNotificationAlias(d.ID) || aliases[d.ID] || types[d.Type] {
			return ErrConfigInvalid
		}
		aliases[d.ID] = true
		types[d.Type] = true
		switch d.Type {
		case Desktop:
			if d.URLSecret != nil || d.BearerSecret != nil {
				return ErrConfigInvalid
			}
		case Slack:
			if d.BearerSecret != nil {
				return ErrConfigInvalid
			}
		case Webhook:
		default:
			return ErrConfigInvalid
		}
		for _, ref := range []*string{d.URLSecret, d.BearerSecret} {
			if ref != nil && !workflow.ValidNotificationAlias(*ref) {
				return ErrConfigInvalid
			}
		}
	}
	return nil
}

func validHTTPS(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.Hostname() != "" && u.User == nil && !strings.Contains(raw, "#")
}

// Binding holds only the events allowed by both policy and operator enablement.
// Secrets are private so default JSON serialization cannot persist them.
type Binding struct {
	Alias  string
	Type   DestinationType
	Events []workflow.NotificationEvent
	url    string
	bearer string
}
