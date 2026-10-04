package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/toolcall"
	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

func originExchange(id, kind, path, origin string) []transcript.Entry {
	entries := toolExchange(id, kind, map[string]any{"path": path}, 2, 3, 4, false)
	for i := range entries {
		entries[i].ExecutionDir = origin
	}
	return entries
}

func TestOpenLocationOriginResolution(t *testing.T) {
	origin := t.TempDir()
	for _, tc := range []struct{ name, use, result, path, want, reason string }{
		{"own", origin, "", "src/a.go", filepath.Join(origin, "src/a.go"), ""},
		{"borrow paired", "", origin, "src/a.go", filepath.Join(origin, "src/a.go"), ""},
		{"both agree", origin, origin, "src/a.go", filepath.Join(origin, "src/a.go"), ""},
		{"unknown", "", "", "src/a.go", "src/a.go", "unavailable"},
		{"relative metadata", "relative", "", "src/a.go", "src/a.go", "invalid"},
		{"malformed metadata", origin + "\n", "", "src/a.go", "src/a.go", "invalid"},
		{"conflict", origin, t.TempDir(), "src/a.go", "src/a.go", "conflicting"},
		{"absolute bypass conflict", origin, t.TempDir(), "/synthetic/a.go", "/synthetic/a.go", ""},
		{"spaces unicode colons", origin, "", " 雪  file:8.go ", filepath.Join(origin, " 雪  file:8.go "), ""},
		{"uri", origin, "", "https://example.test/a", "https://example.test/a", "URI"},
		{"file uri", origin, "", "file:relative", "file:relative", "URI"},
		{"control", origin, "", "bad\nname", "bad\nname", "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := originExchange("a", "edit", tc.path, tc.use)
			entries[1].ExecutionDir = tc.result
			m := newTranscriptClickModel(t, toolGroupEntries(entries))
			got := m.selectedLocationTargets()
			if len(got) != 1 {
				t.Fatalf("targets=%+v", got)
			}
			if got[0].target.Path != tc.want || (tc.reason == "" && got[0].unavailable != "") || !strings.Contains(got[0].unavailable, tc.reason) {
				t.Fatalf("target=%+v", got[0])
			}
		})
	}
}

func TestOpenLocationOriginCoordinates(t *testing.T) {
	for _, field := range []string{"generation", "iteration", "attempt"} {
		t.Run(field, func(t *testing.T) {
			entries := originExchange("reused", "edit", "same.go", "")
			entries[1].ExecutionDir = t.TempDir()
			switch field {
			case "generation":
				entries[1].Generation++
			case "iteration":
				entries[1].Iteration++
			case "attempt":
				entries[1].Attempt++
			}
			m := newTranscriptClickModel(t, toolGroupEntries(entries))
			got := m.selectedLocationTargets()
			if len(got) != 1 || !strings.Contains(got[0].unavailable, "unavailable") {
				t.Fatalf("borrowed another execution's cwd: %+v", got)
			}
		})
	}
}

func TestOpenLocationTargetsArguments(t *testing.T) {
	for _, kind := range []string{"read", "edit", "write", "notebookedit"} {
		keys := []string{"file_path", "path"}
		if kind == "notebookedit" {
			keys = []string{"notebook_path", "target_notebook", "path"}
		}
		for _, arg := range keys {
			t.Run(kind+"/"+arg, func(t *testing.T) {
				input, _ := json.Marshal(map[string]string{arg: "/synthetic/ 雪  file:8.go "})
				for _, blockType := range []transcript.BlockType{transcript.BlockToolUse, transcript.BlockToolResult} {
					m := newTranscriptClickModel(t, []transcript.Entry{{Seq: 1, Role: transcript.RoleAssistant, Blocks: []transcript.Block{{Type: blockType, Tool: &toolcall.Activity{ID: "a", Kind: kind, Input: input}}}}})
					got := m.selectedLocationTargets()
					if len(got) != 1 || got[0].target.Path != "/synthetic/ 雪  file:8.go " {
						t.Fatalf("%s: targets=%+v", blockType, got)
					}
				}
			})
		}
	}
	for _, kind := range []string{"bash", "glob", "grep", "search", "webfetch", "unknown"} {
		t.Run("excluded/"+kind, func(t *testing.T) {
			m := newTranscriptClickModel(t, toolGroupEntries(originExchange("a", kind, "/synthetic/a.go", t.TempDir())))
			if got := m.selectedLocationTargets(); len(got) != 0 {
				t.Fatalf("extracted %s args: %+v", kind, got)
			}
		})
	}
	for _, input := range []string{`not json`, `{"path":7}`, `{"path":null}`, `{"path":""}`} {
		m := newTranscriptClickModel(t, []transcript.Entry{{Seq: 1, Blocks: []transcript.Block{{Type: transcript.BlockToolUse, Tool: &toolcall.Activity{ID: "a", Kind: "read", Input: json.RawMessage(input), Output: json.RawMessage(`{"path":"/synthetic/output"}`), Content: []toolcall.Content{{Type: "text", Text: "/synthetic/prose"}}}}}}})
		if got := m.selectedLocationTargets(); len(got) != 0 {
			t.Fatalf("extracted malformed/prose/output: %+v", got)
		}
	}
}

func TestOpenLocationTargetsDeduplication(t *testing.T) {
	origin := t.TempDir()
	path := filepath.Join(origin, "a.go")
	entries := originExchange("a", "edit", "a.go", origin)
	lineA, lineB, col, negative := 5, 9, 1, -1
	entries[0].Blocks[0].Tool.Locations = []toolcall.Location{{Path: "a.go"}, {Path: "a.go", Line: &lineA}, {Path: path, Line: &lineA, Column: &col}, {Path: path, Line: &lineB}, {Path: filepath.Join(origin, "b.go"), Line: &negative}}
	entries[1].Blocks[0].Tool.Content = []toolcall.Content{{Diff: &toolcall.Diff{Path: path}}}
	before, _ := json.Marshal(entries)
	m := newTranscriptClickModel(t, toolGroupEntries(entries))
	got := m.selectedLocationTargets()
	want := []shared.OpenLocationTarget{{Path: path, Line: 5}, {Path: path, Line: 9}, {Path: filepath.Join(origin, "b.go")}}
	var targets []shared.OpenLocationTarget
	for _, c := range got {
		targets = append(targets, c.target)
	}
	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("targets=%+v want=%+v", targets, want)
	}
	after, _ := json.Marshal(entries)
	if string(before) != string(after) {
		t.Fatal("mutated recorded blocks")
	}
	// An explicit location from a later group member upgrades an earlier fallback
	// while retaining the earlier member's display order.
	entries = toolGroupEntries(originExchange("a", "read", "a.go", origin), originExchange("b", "read", "b.go", origin), originExchange("c", "read", "a.go", origin))
	entries[4].Blocks[0].Tool.Locations = []toolcall.Location{{Path: "a.go", Line: &lineA}}
	m = newTranscriptClickModel(t, entries)
	got = m.selectedLocationTargets()
	if len(got) != 2 || got[0].target.Line != 5 || got[1].target.Path != filepath.Join(origin, "b.go") {
		t.Fatalf("group order=%+v", got)
	}
}

func TestOpenLocationTargetsDistinctOriginsAndPageScope(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	entries := toolGroupEntries(originExchange("a", "read", "same.go", first), originExchange("b", "read", "same.go", second))
	m := newTranscriptClickModel(t, entries)
	got := m.selectedLocationTargets()
	if len(got) != 2 || got[0].target.Path != filepath.Join(first, "same.go") || got[1].target.Path != filepath.Join(second, "same.go") {
		t.Fatalf("origins merged: %+v", got)
	}
	m.setChatPage(transcript.Page{Entries: entries[:2], HasLater: true})
	if got := m.selectedLocationTargets(); len(got) != 1 {
		t.Fatalf("scanned off-page: %+v", got)
	}
	entries[0].ExecutionDir = ""
	entries[1].ExecutionDir = ""
	entries[2].ExecutionDir = ""
	entries[3].ExecutionDir = ""
	m.setChatPage(transcript.Page{Entries: entries})
	if got := m.selectedLocationTargets(); len(got) != 2 {
		t.Fatalf("unknown origins merged: %+v", got)
	}
}

func TestOpenLocationOriginRemovedAndPersistenceOff(t *testing.T) {
	origin := t.TempDir()
	decoy := t.TempDir()
	t.Chdir(decoy)
	path := filepath.Join(origin, "same.go")
	for _, file := range []string{path, filepath.Join(decoy, "same.go")} {
		if err := os.WriteFile(file, []byte("synthetic"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := newTranscriptClickModel(t, toolGroupEntries(originExchange("a", "edit", "same.go", origin)))
	if m.RunDir != "" {
		t.Fatal("fixture should have persistence off")
	}
	_, cmd := m.openLocation()
	req := cmd().(shared.OpenLocationRequest)
	if req.Target.Path != path {
		t.Fatalf("used incidental cwd: %+v", req)
	}
	if err := os.RemoveAll(origin); err != nil {
		t.Fatal(err)
	}
	if err := shared.ValidateOpenLocationPath(req.Target.Path); err == nil {
		t.Fatal("redirected to decoy")
	}
	files, err := os.ReadDir(decoy)
	if err != nil || len(files) != 1 {
		t.Fatalf("unexpected persistence writes: %v %v", files, err)
	}
}
