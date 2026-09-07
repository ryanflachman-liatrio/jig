package main

import (
	"strings"
	"testing"

	"jig/internal/headless"
)

func TestParseOutputMode(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want headless.OutputMode
		ok   bool
	}{
		{"text", headless.OutputText, true},
		{"json", headless.OutputJSON, true},
		{"jsonl", headless.OutputJSONL, true},
		{"yaml", "", false},
		{"", "", false},
	} {
		got, err := parseOutputMode(tc.in)
		if tc.ok {
			if err != nil || got != tc.want {
				t.Errorf("parseOutputMode(%q)=%q,%v want %q,nil", tc.in, got, err, tc.want)
			}
		} else if err == nil {
			t.Errorf("parseOutputMode(%q) expected error", tc.in)
		}
	}
}

func TestRunRun_UsageExit2(t *testing.T) {
	if code := runRun(nil); code != headless.ExitUsage {
		t.Fatalf("no args: exit=%d want %d", code, headless.ExitUsage)
	}
	if code := runRun([]string{"--bogus"}); code != headless.ExitUsage {
		t.Fatalf("bad flag: exit=%d want %d", code, headless.ExitUsage)
	}
	if code := runRun([]string{"--approve-merge", "--discard-merge", "x.toml"}); code != headless.ExitUsage {
		t.Fatalf("mutual exclusive: exit=%d want %d", code, headless.ExitUsage)
	}
}

func TestRunRun_LoadErrorExit1(t *testing.T) {
	code := runRun([]string{"/nonexistent/workflow.toml"})
	if code != headless.ExitFailed {
		t.Fatalf("missing file: exit=%d want %d", code, headless.ExitFailed)
	}
}

func TestReorderRunArgs(t *testing.T) {
	got, err := reorderRunArgs([]string{"wf.toml", "--ci", "--timeout", "5m"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--ci", "--timeout", "5m", "wf.toml"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v want %v", got, want)
	}
	got, err = reorderRunArgs([]string{"--ci", "wf.toml"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "--ci wf.toml" {
		t.Fatalf("got %v", got)
	}
}
