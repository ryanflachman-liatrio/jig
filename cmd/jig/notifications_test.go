package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"jig/internal/notification"
)

func TestNotificationsCheck(t *testing.T) {
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
		readErr                      error
		code                         int
	}{
		{name: "ready", config: ready, secret: receiver.URL, status: "status=ready"},
		{name: "missing secret", config: ready, status: "url_secret_missing", code: 1},
		{name: "invalid URL", config: ready, secret: "http://CANARY.invalid", status: "url_invalid", code: 1},
		{name: "missing config", readErr: os.ErrNotExist, status: "globally_disabled"},
		{name: "unreadable", readErr: errors.New("CANARY\x1b"), status: "config_unreadable", code: 1},
		{name: "malformed", config: "enabled=CANARY", status: "config_invalid", code: 1},
		{name: "global disabled", config: strings.Replace(ready, "enabled=true", "enabled=false", 1), status: "globally_disabled"},
		{name: "destination disabled", config: "enabled=true\n[[destination]]\nid='ops'\ntype='webhook'", status: "destination_disabled"},
		{name: "missing binding", config: "enabled=true", status: "binding_missing", code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errout bytes.Buffer
			secretCalls := 0
			deps := notification.Inspection{ReadFile: func(p string) ([]byte, error) {
				if p != filepath.Join("custom", "notifications.toml") {
					t.Fatal(p)
				}
				return []byte(tc.config), tc.readErr
			}, ResolveSecret: func(name string) (string, error) {
				secretCalls++
				return resolveNamedSecretWithLookup(name, func(key string) (string, bool) {
					if key != "JIG_SECRET_OPS_URL" {
						t.Fatal(key)
					}
					return tc.secret, tc.secret != ""
				})
			}, DesktopStatus: func() string { t.Fatal("unexpected desktop probe"); return "" }}
			code := notificationsCheck([]string{"check", path, "--root", "custom"}, &out, &errout, deps)
			if code != tc.code || !strings.Contains(out.String(), tc.status) {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, &out, &errout)
			}
			if !strings.Contains(out.String(), "delivery is not verified") || !strings.Contains(out.String(), "No notifications were sent") {
				t.Fatal("missing readiness limitations")
			}
			if strings.Contains(out.String()+errout.String(), "CANARY") || strings.Contains(out.String(), receiver.URL) {
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
		code := notificationsCheck(args, &out, &errout, notification.Inspection{ReadFile: func(string) ([]byte, error) { t.Fatal("usage performed I/O"); return nil, nil }})
		if code != 2 {
			t.Fatalf("args %v: code %d", args, code)
		}
	}
}

func TestNotificationsCheckEmptyAndInvalidPolicy(t *testing.T) {
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
			deps := notification.Inspection{ReadFile: func(string) ([]byte, error) { t.Fatal("empty root read config"); return nil, nil }, ResolveSecret: func(string) (string, error) { t.Fatal("empty policy secret lookup"); return "", nil }}
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
