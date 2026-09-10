package workflow

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const notificationWorkflow = `[workflow]
name = "notification-test"
version = "1"
[[step]]
id = "work"
type = "command"
run = "true"
`

func TestNotificationPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, policy string
		events       []NotificationEvent
		routes       []NotificationRoute
		bad          string
	}{
		{name: "absent"},
		{name: "defaults", policy: "[notification]", events: []NotificationEvent{AttentionRequired, RunFailed}},
		{name: "empty events", policy: "[notification]\nevents=[]"},
		{name: "route defaults", policy: "[notification]\nroutes=[{destination='ops'}]", events: []NotificationEvent{AttentionRequired, RunFailed}, routes: []NotificationRoute{{Destination: "ops", Events: []NotificationEvent{AttentionRequired, RunFailed}}}},
		{name: "dedup", policy: "[notification]\nroutes=[{destination='ops',events=['run_failed']},{destination='ops'},{destination='ops',events=[]}]", events: []NotificationEvent{AttentionRequired, RunFailed}, routes: []NotificationRoute{{Destination: "ops", Events: []NotificationEvent{AttentionRequired, RunFailed}}}},
		{name: "empty route", policy: "[notification]\nroutes=[{destination='ops',events=[]}]", events: []NotificationEvent{AttentionRequired, RunFailed}, routes: []NotificationRoute{{Destination: "ops"}}},
		{name: "unknown event", policy: "[notification]\nevents=['typo']", bad: "event"},
		{name: "subset", policy: "[notification]\nroutes=[{destination='ops',events=['run_succeeded']}]", bad: "subset"},
		{name: "alias", policy: "[notification]\nroutes=[{destination='@ops'}]", bad: "alias"},
		{name: "unknown field", policy: "[notification]\nurl='https://example.invalid'", bad: "unknown"},
		{name: "unknown route field", policy: "[notification]\nroutes=[{destination='ops',url='x'}]", bad: "unknown"},
		{name: "unknown profile", policy: "[notification]\nprofile='@missing'", bad: "profile"},
		{name: "empty profile", policy: "[notification]\nprofile=''", bad: "profile"},
		{name: "step forbidden", policy: "[step.notification]", bad: "unknown"},
		{name: "defaults forbidden", policy: "[defaults.notification]", bad: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf, err := Decode(notificationWorkflow+tc.policy, "")
			if tc.bad != "" {
				if err == nil || !strings.Contains(err.Error(), tc.bad) {
					t.Fatalf("want %s error, got %v", tc.bad, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			p := wf.NotificationPolicy()
			if !reflect.DeepEqual(p.Events, tc.events) || !reflect.DeepEqual(p.Routes, tc.routes) {
				t.Fatalf("got %#v, want events %v routes %v", p, tc.events, tc.routes)
			}
			if len(p.Events) > 0 {
				p.Events[0] = RunSucceeded
				if wf.NotificationPolicy().Events[0] == RunSucceeded {
					t.Fatal("accessor aliases events")
				}
			}
			if len(p.Routes) > 0 {
				p.Routes[0].Destination = "changed"
				if len(p.Routes[0].Events) > 0 {
					p.Routes[0].Events[0] = RunSucceeded
				}
				if !reflect.DeepEqual(wf.NotificationPolicy().Routes, tc.routes) {
					t.Fatal("accessor aliases routes")
				}
			}
		})
	}
}

func writeNotificationFixture(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadNotificationProfiles(t *testing.T) {
	for _, tc := range []struct {
		name, profile, override, bad string
		events                       []NotificationEvent
		destinations                 []string
	}{
		{name: "inherit", profile: `[[notification]]
id='@operator'
routes=[{destination='desktop'},{destination='ops'}]`, events: []NotificationEvent{AttentionRequired, RunFailed}, destinations: []string{"desktop", "ops"}},
		{name: "replacement", profile: `[[notification]]
id='@operator'
routes=[{destination='desktop'}]`, override: "routes=[{destination='ops'}]", events: []NotificationEvent{AttentionRequired, RunFailed}, destinations: []string{"ops"}},
		{name: "clear routes", profile: "[[notification]]\nid='@operator'\nroutes=[{destination='ops'}]", override: "routes=[]", events: []NotificationEvent{AttentionRequired, RunFailed}},
		{name: "clear events", profile: "[[notification]]\nid='@operator'", override: "events=[]"},
		{name: "replace events", profile: "[[notification]]\nid='@operator'", override: "events=['run_succeeded']", events: []NotificationEvent{RunSucceeded}},
		{name: "duplicate", profile: "[[notification]]\nid='@operator'\n[[notification]]\nid='@operator'", bad: "duplicate"},
		{name: "missing id", profile: "[[notification]]", bad: "id"},
		{name: "bare id", profile: "[[notification]]\nid='operator'", bad: "id"},
		{name: "inherit forbidden", profile: "[[notification]]\nid='@operator'\nprofile='@other'", bad: "unknown"},
		{name: "unknown", profile: "[[notification]]\nid='@operator'\nurl='x'", bad: "unknown"},
		{name: "invalid event", profile: "[[notification]]\nid='@operator'\nevents=['bad']", bad: "event"},
		{name: "invalid alias", profile: "[[notification]]\nid='@operator'\nroutes=[{destination='bad!'}]", bad: "alias"},
		{name: "malformed", profile: "[[notification]", bad: "parse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeNotificationFixture(t, filepath.Join(dir, ".git"), "")
			writeNotificationFixture(t, filepath.Join(dir, ".agents/jig/notification-profiles/operator.toml"), tc.profile)
			path := filepath.Join(dir, "examples/work.toml")
			writeNotificationFixture(t, path, notificationWorkflow+"[notification]\nprofile='@operator'\n"+tc.override)
			wf, err := Load(path)
			if tc.bad != "" {
				if err == nil || !strings.Contains(err.Error(), tc.bad) {
					t.Fatalf("want %s, got %v", tc.bad, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			p := wf.NotificationPolicy()
			if !reflect.DeepEqual(p.Events, tc.events) {
				t.Fatalf("events: %v", p.Events)
			}
			var dests []string
			for _, r := range p.Routes {
				dests = append(dests, r.Destination)
			}
			if !reflect.DeepEqual(dests, tc.destinations) {
				t.Fatalf("routes: %v", dests)
			}
		})
	}
}

func TestNotificationModules(t *testing.T) {
	root := `[workflow]
name='root'
version='1'
[notification]
routes=[{destination='ops'}]
[[step]]
id='nested'
type='subworkflow'
module='outer.toml'
`
	outer := `[module]
schema_version=1
[module.exports.result]
ref='@inner.result'
[[step]]
id='inner'
type='subworkflow'
module='inner.toml'
`
	inner := `[module]
schema_version=1
[module.exports.result]
ref='@work'
[[step]]
id='work'
type='command'
run='true'
`
	for _, placement := range []string{"none", "outer", "inner"} {
		t.Run(placement, func(t *testing.T) {
			dir := t.TempDir()
			a, b := outer, inner
			if placement == "outer" {
				a += "[notification]\n"
			}
			if placement == "inner" {
				b += "[notification]\n"
			}
			writeNotificationFixture(t, filepath.Join(dir, "root.toml"), root)
			writeNotificationFixture(t, filepath.Join(dir, "outer.toml"), a)
			writeNotificationFixture(t, filepath.Join(dir, "inner.toml"), b)
			wf, err := Load(filepath.Join(dir, "root.toml"))
			if placement != "none" {
				if err == nil || !strings.Contains(err.Error(), "root workflow") {
					t.Fatalf("got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(wf.Steps) != 1 || wf.Steps[0].ID != "nested__inner__work" || len(wf.NotificationPolicy().Routes) != 1 {
				t.Fatalf("expanded workflow: %+v", wf)
			}
		})
	}
}

func TestLoadNotificationProfilesFiles(t *testing.T) {
	dir := t.TempDir()
	profiles := filepath.Join(dir, ".agents/jig/notification-profiles")
	writeNotificationFixture(t, filepath.Join(profiles, "a.toml"), "[[notification]]\nid='@operator'\nevents=[]\nroutes=[]")
	// No Git marker: profiles fall back to the workflow directory.
	wf, err := Decode(notificationWorkflow+"[notification]\nprofile='@operator'", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(wf.NotificationPolicy().Events) != 0 {
		t.Fatal("empty inherited events regained defaults")
	}
	writeNotificationFixture(t, filepath.Join(profiles, "b.toml"), "[[notification]]\nid='@operator'")
	if _, err := loadNotificationProfiles(dir); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("cross-file duplicate: %v", err)
	}
	if err := os.Remove(filepath.Join(profiles, "b.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(profiles, "b.toml")); err != nil {
		t.Fatal(err)
	}
	if _, err := loadNotificationProfiles(dir); err == nil || !strings.Contains(err.Error(), "read notification profile") {
		t.Fatalf("unreadable file: %v", err)
	}
}

func TestLoadNotificationProfilesUnusedInvalid(t *testing.T) {
	dir := t.TempDir()
	writeNotificationFixture(t, filepath.Join(dir, ".agents/jig/notification-profiles/invalid.toml"), "[[notification]]\nid='@unused'\nevents=['invalid']")
	if _, err := Decode(notificationWorkflow, dir); err == nil {
		t.Fatal("invalid project profile did not fail validation")
	}
}
