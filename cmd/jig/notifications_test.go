package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"jig/internal/notification"
)

// isolateGlobalConfigFlagPath points the --config-equivalent global at a
// nonexistent path for the duration of the test, so loadEffectiveConfig never
// resolves the real developer machine's $HOME/.config/jig/config.toml.
func isolateGlobalConfigFlagPath(t *testing.T) {
	t.Helper()
	original := globalConfigFlagPath
	globalConfigFlagPath = filepath.Join(t.TempDir(), "unused-user-config.toml")
	t.Cleanup(func() { globalConfigFlagPath = original })
}

// wrapNotificationsTOML nests raw notifications.toml-shaped content (the
// schema notification.LocalConfig used to own as its own file) under
// config.toml's [notifications] table, as internal/config.Load now expects.
func wrapNotificationsTOML(raw string) string {
	raw = strings.ReplaceAll(raw, "[[destination]]", "[[notifications.destination]]")
	return "[notifications]\n" + raw
}

func TestNotificationsCheck(t *testing.T) {
	isolateGlobalConfigFlagPath(t)
	var sends atomic.Int32
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { sends.Add(1) }))
	defer receiver.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.toml")
	source := `[workflow]
name='check'
version='1'
[[step]]
id='work'
type='command'
run='true'
[notification]
routes=[{destination='ops'}]
`
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	const ready = "enabled=true\n[[destination]]\nid='ops'\ntype='webhook'\nenabled=true\nurl_secret='ops-url'"
	for _, tc := range []struct {
		name, config, secret, status string
		missingFile, unreadableDir   bool
		code                         int
	}{
		{name: "ready", config: ready, secret: receiver.URL, status: "status=ready"},
		{name: "missing secret", config: ready, status: "url_secret_missing", code: 1},
		{name: "invalid URL", config: ready, secret: "http://CANARY.invalid", status: "url_invalid", code: 1},
		{name: "missing config", missingFile: true, status: "globally_disabled"},
		{name: "unreadable", unreadableDir: true, status: "config_unreadable", code: 1},
		{name: "malformed", config: "enabled=CANARY", status: "config_invalid", code: 1},
		{name: "global disabled", config: strings.Replace(ready, "enabled=true", "enabled=false", 1), status: "globally_disabled"},
		{name: "destination disabled", config: "enabled=true\n[[destination]]\nid='ops'\ntype='webhook'", status: "destination_disabled"},
		{name: "missing binding", config: "enabled=true", status: "binding_missing", code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			configPath := filepath.Join(root, "config.toml")
			switch {
			case tc.missingFile:
				// No config.toml at all: internal/config.Load treats a
				// missing project-level file as "use zero value," not an
				// error, matching the previous os.ErrNotExist behavior.
			case tc.unreadableDir:
				// A directory in place of the file makes os.ReadFile fail
				// with a non-IsNotExist error, deterministically exercising
				// the config_unreadable path without disk-read injection.
				if err := os.Mkdir(configPath, 0700); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.WriteFile(configPath, []byte(wrapNotificationsTOML(tc.config)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var out, errout bytes.Buffer
			secretCalls := 0
			deps := notification.Inspection{ResolveSecret: func(name string) (string, error) {
				secretCalls++
				return resolveNamedSecretWithLookup(name, func(key string) (string, bool) {
					if key != "JIG_SECRET_OPS_URL" {
						t.Fatal(key)
					}
					return tc.secret, tc.secret != ""
				})
			}, DesktopStatus: func() string { t.Fatal("unexpected desktop probe"); return "" }}
			code := notificationsCheck([]string{"check", path, "--root", root}, &out, &errout, deps)
			combined := out.String() + errout.String()
			if code != tc.code || !strings.Contains(combined, tc.status) {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, &out, &errout)
			}
			if tc.missingFile || tc.unreadableDir || tc.name == "malformed" {
				// Config-load failures short-circuit before readiness output.
			} else if !strings.Contains(out.String(), "delivery is not verified") || !strings.Contains(out.String(), "No notifications were sent") {
				t.Fatal("missing readiness limitations")
			}
			if strings.Contains(combined, "CANARY") || strings.Contains(out.String(), receiver.URL) {
				t.Fatal("secret leaked")
			}
			if (tc.name == "global disabled" || tc.name == "destination disabled" || tc.name == "missing config") && secretCalls != 0 {
				t.Fatal("disabled secret lookup")
			}
		})
	}
	t.Logf("receiver-spy requests: %d; inspection has no sender or helper runner", sends.Load())
	if sends.Load() != 0 {
		t.Fatal("readiness sent network request")
	}
	for _, args := range [][]string{nil, {"send"}, {"check"}, {"check", path, "extra"}, {"check", path, "--bad"}, {"check", path, "--root"}} {
		var out, errout bytes.Buffer
		code := notificationsCheck(args, &out, &errout, notification.Inspection{})
		if code != 2 {
			t.Fatalf("args %v: code %d", args, code)
		}
	}
}

func TestNotificationsCheckEmptyAndInvalidPolicy(t *testing.T) {
	isolateGlobalConfigFlagPath(t)
	for _, tc := range []struct {
		name, policy string
		code         int
		want         string
	}{
		{"absent", "", 0, "no events"},
		{"empty events", "[notification]\nevents=[]", 0, "no events"},
		{"empty routes", "[notification]\nroutes=[]", 0, "no destination routes"},
		{"invalid", "[notification]\nevents=['CANARY']", 1, "workflow_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "workflow.toml")
			if err := os.WriteFile(path, []byte("[workflow]\nname='empty'\nversion='1'\n[[step]]\nid='work'\ntype='command'\nrun='true'\n"+tc.policy), 0600); err != nil {
				t.Fatal(err)
			}
			var out, errout bytes.Buffer
			deps := notification.Inspection{ResolveSecret: func(string) (string, error) { t.Fatal("empty policy secret lookup"); return "", nil }}
			code := notificationsCheck([]string{"check", "--root=", path}, &out, &errout, deps)
			if code != tc.code || !strings.Contains(out.String()+errout.String(), tc.want) {
				t.Fatalf("code=%d out=%s err=%s", code, &out, &errout)
			}
			if strings.Contains(out.String()+errout.String(), "CANARY") {
				t.Fatal("echoed invalid policy value")
			}
		})
	}
}
