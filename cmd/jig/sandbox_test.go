package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This subprocess executes the real main dispatcher and exit behavior without
// initializing a TUI or runtime in the test process.
func TestD04OfflineCLIHelper(t *testing.T) {
	if os.Getenv("JIG_D04_CLI_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"jig"}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	os.Exit(99)
}

func TestD04OfflineCLI(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "external-called")
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"docker", "docker-credential-test", "claude", "codex", "cursor-agent"} {
		script := "#!/bin/sh\nprintf called >> '" + marker + "'\nexit 97\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	// Any accidental policy/config decode fails. The selected Docker URI is also
	// unavailable; successful structural validation must not depend on it.
	for _, relative := range []string{"jig/sandbox-policy.toml", "docker/config.json"} {
		path := filepath.Join(dir, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("INVALID PRIVATE CONFIG"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	source := `[workflow]
name='offline-synthetic'
version='1'
[[sandbox]]
name='box'
runtime='docker'
image='alpine'
[[step]]
id='work'
type='command'
sandbox='box'
run='exit 98'
`
	good := filepath.Join(dir, "valid.toml")
	bad := filepath.Join(dir, "invalid.toml")
	if err := os.WriteFile(good, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte(strings.Replace(source, "image='alpine'", "image=''", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args []string
		code int
	}{
		{"valid", []string{"validate", good}, 0},
		{"invalid", []string{"validate", bad}, 1},
	}
	for _, flag := range []string{"sandbox-policy", "docker-host", "docker-context", "docker-config", "probe-store-write"} {
		cases = append(cases, struct {
			name string
			args []string
			code int
		}{flag, []string{"validate", good, "--" + flag + "=poison"}, 2})
		cases = append(cases, struct {
			name string
			args []string
			code int
		}{flag + "_alone", []string{"validate", "--" + flag + "=poison"}, 2})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			args := append([]string{"-test.run=^TestD04OfflineCLIHelper$", "--"}, tc.args...)
			cmd := exec.CommandContext(ctx, os.Args[0], args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "JIG_D04_CLI_HELPER=1", "HOME="+dir, "XDG_CONFIG_HOME="+dir, "DOCKER_CONFIG="+filepath.Join(dir, "docker"), "DOCKER_HOST=unix:///nonexistent-d04-synthetic.sock", "DOCKER_CONTEXT=missing", "PATH="+bin)
			out, err := cmd.CombinedOutput()
			code := 0
			if err != nil {
				if e, ok := err.(*exec.ExitError); ok {
					code = e.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			if ctx.Err() != nil || code != tc.code {
				t.Fatalf("exit=%d want=%d err=%v output=%s", code, tc.code, err, out)
			}
			if strings.Contains(string(out), "INVALID PRIVATE CONFIG") {
				t.Fatal("operator input disclosed")
			}
			t.Log("exit=" + strconv.Itoa(code) + " " + strings.TrimSpace(string(out)))
		})
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("external executable was invoked")
	}
	t.Log("all poisoned executables untouched; invalid policy/Docker config and unavailable endpoint do not affect validate")
}

func TestD04PolicyCLI(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	operator := filepath.Join(root, "operator")
	for _, dir := range []string{project, operator} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	policy := filepath.Join(operator, "deny.toml")
	if err := os.WriteFile(policy, []byte("version=1\nallow_host=false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(operator, "bad.toml")
	if err := os.WriteFile(bad, []byte("PRIVATE_SENTINEL = ["), 0600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(project, "executed")
	wf := filepath.Join(project, "work.toml")
	source := "[workflow]\nname='policy-cli'\nversion='1'\n[[step]]\nid='work'\ntype='command'\nrun='touch " + marker + "'\nsecrets=['synthetic']\n"
	if err := os.WriteFile(wf, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		args   []string
		code   int
		reason string
	}{
		{"run", []string{"run", wf, "--sandbox-policy", policy, "--root=", "--ci"}, 1, "sandbox_policy_denied"},
		{"doctor-json", []string{"doctor", wf, "--sandbox-policy=" + policy, "--root=", "--output=json"}, 1, "sandbox_policy_denied"},
		{"doctor-text", []string{"doctor", wf, "--sandbox-policy", policy, "--root="}, 1, "auxiliary_host"},
		{"malformed", []string{"run", wf, "--sandbox-policy", bad, "--root="}, 1, "sandbox_policy_invalid"},
		{"resume-missing", []string{"resume", "synthetic", "--on-recovery=retry", "--sandbox-policy", bad}, 1, "sandbox_policy_invalid"},
		{"startup-malformed", []string{"--sandbox-policy", bad}, 1, "sandbox_policy_invalid"},
		{"run-empty", []string{"run", wf, "--sandbox-policy="}, 2, "nonempty"},
		{"doctor-empty", []string{"doctor", "--sandbox-policy="}, 2, "nonempty"},
		{"resume-duplicate", []string{"resume", "synthetic", "--on-recovery=retry", "--sandbox-policy", policy, "--sandbox-policy=" + policy}, 2, "once"},
		{"run-duplicate", []string{"run", wf, "--sandbox-policy", policy, "--sandbox-policy=" + policy}, 2, "once"},
		{"startup-empty", []string{"--sandbox-policy="}, 2, "nonempty"},
		{"validate", []string{"validate", wf, "--sandbox-policy", policy}, 2, "usage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestD04OfflineCLIHelper$", "--"}, tc.args...)...)
			cmd.Dir = project
			cmd.Env = append(os.Environ(), "JIG_D04_CLI_HELPER=1", "HOME="+operator, "XDG_CONFIG_HOME="+operator, "DOCKER_CONFIG="+bad, "DOCKER_HOST=tcp://PRIVATE_SENTINEL", "JIG_SECRET_SYNTHETIC=PRIVATE_SENTINEL")
			out, err := cmd.CombinedOutput()
			code := 0
			if err != nil {
				if e, ok := err.(*exec.ExitError); ok {
					code = e.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			if ctx.Err() != nil || code != tc.code || !strings.Contains(string(out), tc.reason) {
				t.Fatalf("exit %d want %d output %s", code, tc.code, out)
			}
			if strings.Contains(string(out), "PRIVATE_SENTINEL") {
				t.Fatal("private value disclosed")
			}
			t.Logf("exit=%d reason=%s", code, tc.reason)
		})
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("denied workload executed")
	}
}
