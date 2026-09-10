package notification

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jig/internal/workflow"
)

func TestLocalConfig(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		bad        bool
	}{
		{"empty", "", false},
		{"valid", `enabled=true
[[destination]]
id='desktop'
type='desktop'
[[destination]]
id='slack'
type='slack'
enabled=true
url_secret='slack-url'
[[destination]]
id='ops'
type='webhook'
enabled=true
url_secret='ops-url'
bearer_secret='ops-token'`, false},
		{"malformed", "enabled=", true},
		{"unknown root", "url='CANARY'", true},
		{"unknown field", "[[destination]]\nid='ops'\ntype='webhook'\nurl='CANARY'", true},
		{"duplicate alias", "[[destination]]\nid='ops'\ntype='slack'\n[[destination]]\nid='ops'\ntype='webhook'", true},
		{"duplicate type", "[[destination]]\nid='one'\ntype='webhook'\n[[destination]]\nid='two'\ntype='webhook'", true},
		{"unknown type", "[[destination]]\nid='ops'\ntype='email'", true},
		{"invalid alias", "[[destination]]\nid='bad!\u001b'\ntype='slack'", true},
		{"missing alias", "[[destination]]\ntype='slack'", true},
		{"desktop URL", "[[destination]]\nid='desktop'\ntype='desktop'\nurl_secret='unused'", true},
		{"desktop bearer", "[[destination]]\nid='desktop'\ntype='desktop'\nbearer_secret=''", true},
		{"slack bearer", "[[destination]]\nid='slack'\ntype='slack'\nbearer_secret='unused'", true},
		{"invalid reference", "[[destination]]\nid='ops'\ntype='webhook'\nurl_secret='https://CANARY'", true},
		{"empty reference", "[[destination]]\nid='ops'\ntype='webhook'\nurl_secret=''", true},
		{"disabled without secrets", "[[destination]]\nid='ops'\ntype='webhook'", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseLocalConfig([]byte(tc.data))
			if (err != nil) != tc.bad {
				t.Fatalf("got %v", err)
			}
			if err != nil && err.Error() != "config_invalid" {
				t.Fatalf("unsanitized error: %v", err)
			}
			if tc.name == "disabled without secrets" && (cfg.Enabled || cfg.Destinations[0].Enabled) {
				t.Fatal("enabled by default")
			}
		})
	}
}

func TestLocalConfigRead(t *testing.T) {
	for _, tc := range []struct {
		name, root string
		err        error
		want       string
	}{
		{"persistence off", "", errors.New("CANARY"), ""},
		{"missing", "root", os.ErrNotExist, ""},
		{"unreadable", "root", errors.New("CANARY"), "config_unreadable"},
		{"malformed", "root", nil, "config_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			_, err := LoadLocalConfig(tc.root, func(path string) ([]byte, error) {
				calls++
				if path != filepath.Join(tc.root, "notifications.toml") {
					t.Fatal(path)
				}
				return []byte("enabled=CANARY"), tc.err
			})
			if tc.root == "" && calls != 0 {
				t.Fatal("persistence-off read")
			}
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != tc.want {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestResolveBindings(t *testing.T) {
	policy := workflow.NotificationPolicy{Events: []workflow.NotificationEvent{workflow.RunFailed}, Routes: []workflow.NotificationRoute{{Destination: "ops", Events: []workflow.NotificationEvent{workflow.RunFailed}}}}
	const config = "enabled=true\n[[destination]]\nid='ops'\ntype='webhook'\nenabled=true\nurl_secret='ops-url'\nbearer_secret='ops-token'"
	for _, tc := range []struct {
		name, url, token, status                           string
		globalOff, destOff, routeOff, unrequested, missing bool
	}{
		{name: "ready", url: "https://example.invalid/CANARY", token: "CANARY", status: "ready"},
		{name: "missing URL", status: "url_secret_missing"},
		{name: "missing bearer", url: "https://example.invalid/CANARY", status: "bearer_secret_missing"},
		{name: "bearer control", url: "https://example.invalid/CANARY", token: "CANARY\r\nX-Evil: yes", status: "bearer_invalid"},
		{name: "http", url: "http://example.invalid/CANARY", status: "url_invalid"},
		{name: "no host", url: "https:///CANARY", status: "url_invalid"},
		{name: "userinfo", url: "https://CANARY@example.invalid/", status: "url_invalid"},
		{name: "fragment", url: "https://example.invalid/#CANARY", status: "url_invalid"},
		{name: "empty fragment", url: "https://example.invalid/#", status: "url_invalid"},
		{name: "bad port", url: "https://example.invalid:CANARY/", status: "url_invalid"},
		{name: "global off", globalOff: true, status: "globally_disabled"},
		{name: "destination off", destOff: true, status: "destination_disabled"},
		{name: "empty route", routeOff: true, status: "route_disabled"},
		{name: "unrequested", unrequested: true},
		{name: "missing binding", missing: true, status: "binding_missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseLocalConfig([]byte(config))
			if err != nil {
				t.Fatal(err)
			}
			p := policy
			p.Routes = append([]workflow.NotificationRoute(nil), policy.Routes...)
			if tc.globalOff {
				cfg.Enabled = false
			}
			if tc.destOff {
				cfg.Destinations[0].Enabled = false
			}
			if tc.routeOff {
				p.Routes[0].Events = nil
			}
			if tc.unrequested {
				p.Routes = nil
			}
			if tc.missing {
				cfg.Destinations = nil
			}
			calls := 0
			deps := Inspection{ResolveSecret: func(name string) (string, error) {
				calls++
				v := tc.url
				if name == "ops-token" {
					v = tc.token
				}
				if v == "" {
					return "", errors.New("CANARY\x1b\n")
				}
				return v, nil
			}, DesktopStatus: func() string { t.Fatal("unexpected desktop probe"); return "" }}
			report, bindings := ResolveBindings(p, cfg, deps)
			if tc.globalOff || tc.destOff || tc.routeOff || tc.unrequested || tc.missing {
				if calls != 0 {
					t.Fatal("resolved unnecessary secrets")
				}
			}
			if tc.status != "" && report.Destinations[0].Status != tc.status {
				t.Fatalf("report: %+v", report)
			}
			wantBindings := 0
			if tc.status == "ready" {
				wantBindings = 1
			}
			if len(bindings) != wantBindings {
				t.Fatalf("bindings=%d", len(bindings))
			}
			if wantBindings > 0 && (bindings[0].url != tc.url || bindings[0].bearer != tc.token || len(bindings[0].Events) != 1) {
				t.Fatal("incorrect bound values")
			}
			var buf bytes.Buffer
			report.Render(&buf)
			if strings.Contains(buf.String(), "CANARY") || strings.Contains(buf.String(), "https:") {
				t.Fatal("secret in report")
			}
			data, err := json.Marshal(bindings)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "CANARY") {
				t.Fatal("serialized secrets")
			}
		})
	}
}

func TestReadinessDesktop(t *testing.T) {
	for _, tc := range []struct {
		platform    string
		helper, bus bool
		want        string
	}{
		{"darwin", true, false, "ready"}, {"darwin", false, false, "desktop_helper_missing"},
		{"linux", true, true, "ready"}, {"linux", false, true, "desktop_helper_missing"}, {"linux", true, false, "desktop_session_missing"},
		{"windows", true, true, "desktop_unsupported"},
	} {
		t.Run(tc.platform+tc.want, func(t *testing.T) {
			status := desktopStatus(tc.platform, func(name string) (string, error) {
				if tc.platform == "darwin" && name != "/usr/bin/osascript" {
					t.Fatal(name)
				}
				if tc.platform == "linux" && name != "notify-send" {
					t.Fatal(name)
				}
				if !tc.helper {
					return "", errors.New("CANARY")
				}
				return name, nil
			}, func(string) string {
				if tc.bus {
					return "session"
				}
				return ""
			})
			if status != tc.want {
				t.Fatalf("got %s", status)
			}
		})
	}
	p := workflow.NotificationPolicy{Events: []workflow.NotificationEvent{workflow.RunFailed}, Routes: []workflow.NotificationRoute{{Destination: "desktop", Events: []workflow.NotificationEvent{workflow.RunFailed}}}}
	cfg := LocalConfig{Enabled: true, Destinations: []Destination{{ID: "desktop", Type: Desktop, Enabled: true}}}
	report, _ := ResolveBindings(p, cfg, Inspection{DesktopStatus: func() string { return "CANARY\x1b" }})
	if !report.Problem || report.Destinations[0].Status != "desktop_unavailable" {
		t.Fatalf("unsanitized probe: %+v", report)
	}
}

func TestResolveBindingsFailureIsolation(t *testing.T) {
	p := workflow.NotificationPolicy{Events: []workflow.NotificationEvent{workflow.AttentionRequired, workflow.RunFailed}, Routes: []workflow.NotificationRoute{
		{Destination: "desktop", Events: []workflow.NotificationEvent{workflow.AttentionRequired}},
		{Destination: "slack", Events: []workflow.NotificationEvent{workflow.RunFailed}},
	}}
	urlSecret := "slack-url"
	cfg := LocalConfig{Enabled: true, Destinations: []Destination{
		{ID: "desktop", Type: Desktop, Enabled: true},
		{ID: "slack", Type: Slack, Enabled: true, URLSecret: &urlSecret},
		{ID: "unused", Type: Webhook, Enabled: true},
	}}
	calls := 0
	report, bindings := ResolveBindings(p, cfg, Inspection{
		DesktopStatus: func() string { return "desktop_helper_missing" },
		ResolveSecret: func(name string) (string, error) {
			calls++
			if name != "slack-url" {
				t.Fatal(name)
			}
			return "https://example.invalid/synthetic", nil
		},
	})
	if !report.Problem || len(bindings) != 1 || bindings[0].Alias != "slack" || calls != 1 {
		t.Fatalf("report=%+v bindings=%d calls=%d", report, len(bindings), calls)
	}
	if len(bindings[0].Events) != 1 || bindings[0].Events[0] != workflow.RunFailed {
		t.Fatal("operator expanded policy events")
	}
}

func TestReadinessConfigErrorRetainsPolicy(t *testing.T) {
	p := workflow.NotificationPolicy{Events: []workflow.NotificationEvent{workflow.RunFailed}, Routes: []workflow.NotificationRoute{{Destination: "ops", Events: []workflow.NotificationEvent{workflow.RunFailed}}}}
	r := Inspect(p, "root", Inspection{ReadFile: func(string) ([]byte, error) { return nil, errors.New("CANARY") }})
	if !r.Problem || len(r.Destinations) != 1 || r.Destinations[0].Alias != "ops" || r.Destinations[0].Status != "config_unreadable" {
		t.Fatalf("lost requested policy: %+v", r)
	}
}
