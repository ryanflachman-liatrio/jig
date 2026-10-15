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
