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

func TestParseRecoveryAndConflict(t *testing.T) {
	for _, ok := range []string{"abort", "retry", "skip"} {
		if err := parseRecoveryAction(ok); err != nil {
			t.Errorf("parseRecoveryAction(%q): %v", ok, err)
		}
	}
	if err := parseRecoveryAction("resume"); err == nil {
		t.Fatal("resume should be rejected")
	}
	if err := parseConflictAction("abort"); err != nil {
		t.Fatal(err)
	}
	if err := parseConflictAction("agent"); err == nil {
		t.Fatal("agent should be rejected")
	}
}

func TestRunRun_BadOnRecoveryExit2(t *testing.T) {
	if code := runRun([]string{"--on-recovery", "resume", "x.toml"}); code != headless.ExitUsage {
		t.Fatalf("exit=%d want %d", code, headless.ExitUsage)
	}
	if code := runRun([]string{"--on-conflict", "agent", "x.toml"}); code != headless.ExitUsage {
		t.Fatalf("exit=%d want %d", code, headless.ExitUsage)
	}
}

func TestReorderRunArgs_PolicyFlags(t *testing.T) {
	got, err := reorderRunArgs([]string{"wf.toml", "--on-recovery", "skip", "--on-conflict", "abort"})
	if err != nil {
		t.Fatal(err)
	}
	want := "--on-recovery skip --on-conflict abort wf.toml"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %v want %s", got, want)
	}
}
