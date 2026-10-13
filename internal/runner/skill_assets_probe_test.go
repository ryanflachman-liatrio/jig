//go:build darwin || linux

package runner

// Test-only P3 delivery adapter. It exercises the proposed seam through the
// concrete agent runner without enabling frozen-helper delivery in production.
import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/harness"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

var p3OwnerUnknown = errors.New("authoring helper: owner termination unconfirmed")
var p3CopyChanged = errors.New("authoring helper: inventory changed")

type p3HelperCopy struct {
	root       string
	source     map[string][]byte
	executable map[string]bool
	ownerDone  <-chan struct{}
}

func (c *p3HelperCopy) ownerExited() bool {
	if c.ownerDone == nil {
		return true
	}
	select {
	case <-c.ownerDone:
		return true
	default:
		return false
	}
}
func (c *p3HelperCopy) prepare() error {
	if !c.ownerExited() {
		return p3OwnerUnknown
	}
	if err := os.RemoveAll(c.root); err != nil {
		return err
	}
	if err := os.MkdirAll(c.root, 0700); err != nil {
		return err
	}
	for name, data := range c.source {
		if !fs.ValidPath(name) {
			return p3CopyChanged
		}
		path := filepath.Join(c.root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		mode := fs.FileMode(0400)
		if c.executable[name] {
			mode = 0500
		}
		if err := os.WriteFile(path, bytes.Clone(data), mode); err != nil {
			return err
		}
	}
	return c.verify()
}
func (c *p3HelperCopy) verify() error {
	if !c.ownerExited() {
		return p3OwnerUnknown
	}
	expected := make(map[string]bool)
	for name := range c.source {
		expected[name] = false
		for dir := filepath.Dir(name); dir != "."; dir = filepath.Dir(dir) {
			expected[dir] = true
		}
	}
	seen := 0
	err := filepath.WalkDir(c.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return p3CopyChanged
		}
		if path == c.root {
			if !entry.IsDir() {
				return p3CopyChanged
			}
			return nil
		}
		rel, err := filepath.Rel(c.root, path)
		if err != nil {
			return p3CopyChanged
		}
		isDir, ok := expected[rel]
		if !ok || entry.IsDir() != isDir {
			return p3CopyChanged
		}
		seen++
		info, err := entry.Info()
		if err != nil {
			return p3CopyChanged
		}
		if isDir {
			if info.Mode().Perm() != 0700 {
				return p3CopyChanged
			}
			return nil
		}
		if !info.Mode().IsRegular() || info.Size() != int64(len(c.source[rel])) {
			return p3CopyChanged
		}
		mode := fs.FileMode(0400)
		if c.executable[rel] {
			mode = 0500
		}
		if info.Mode().Perm() != mode {
			return p3CopyChanged
		}
		data, err := os.ReadFile(path)
		if err != nil || sha256.Sum256(data) != sha256.Sum256(c.source[rel]) {
			return p3CopyChanged
		}
		return nil
	})
	if err != nil {
		return err
	}
	if seen != len(expected) {
		return p3CopyChanged
	}
	return nil
}
func (c *p3HelperCopy) cleanup() error {
	if !c.ownerExited() {
		return p3OwnerUnknown
	}
	return os.RemoveAll(c.root)
}

const p3BindingPrefix = "Frozen skill directory (JSON): "

func p3BoundRequest(req engine.StepRequest, root string) engine.StepRequest {
	// Supply a detached step with its effective body. Do not mutate the caller's
	// loaded step or hand the copied helper root back as authoritative storage.
	cloned := *req.Step
	cloned.SnapshotAgentPrompt = strings.ReplaceAll(req.Step.AgentPrompt(), "{{skill_dir}}", root)
	// A JSON round trip drops private loader caches, retaining exported snapshots.
	raw, _ := json.Marshal(cloned)
	var effective workflow.Step
	_ = json.Unmarshal(raw, &effective)
	req.Step = &effective
	encoded, _ := json.Marshal(root)
	binding := p3BindingPrefix + string(encoded) + "\nResolve relative skill references from this directory.\n"
	req.WorkflowContext = binding + req.WorkflowContext
	if req.ResumeSessionID != "" {
		req.Message = binding + req.Message
	}
	return req
}

type p3ReadingHarness struct {
	specs      []harness.SessionSpec
	outputs    []string
	remembered string
}

func (h *p3ReadingHarness) Name() string { return "p3-synthetic" }
func (h *p3ReadingHarness) Capabilities() harness.CapabilitySet {
	return harness.NewCapabilitySet(harness.CapPermissionCallback, harness.CapSessionResume)
}
func (h *p3ReadingHarness) Open(ctx context.Context, spec harness.SessionSpec) (harness.Session, error) {
	h.specs = append(h.specs, spec)
	var root string
	for _, line := range strings.Split(spec.Prompt, "\n") {
		if strings.HasPrefix(line, p3BindingPrefix) {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, p3BindingPrefix)), &root); err != nil {
				return nil, err
			}
			break
		}
	}
	if root == "" || !filepath.IsAbs(root) {
		return nil, errors.New("missing frozen root binding")
	}
	if strings.Contains(spec.Prompt, "{{skill_dir}}") {
		return nil, errors.New("unrendered skill marker")
	}
	helper := filepath.Join(root, "helpers", "say.sh")
	if h.remembered == "" {
		h.remembered = helper
	} else if h.remembered != helper {
		return nil, errors.New("root moved across conversation resume")
	}
	data, err := os.ReadFile(helper)
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(data, []byte("ORIGINAL")) {
		return nil, errors.New("substituted helper")
	}
	cmd := exec.CommandContext(ctx, h.remembered)
	cmd.Dir = spec.Cwd
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	h.outputs = append(h.outputs, string(output))
	return harness.NewFakeSession([]harness.Event{{Type: harness.EventText, Text: string(output)}, {Type: harness.EventResult, SessionID: "p3-conversation"}}), nil
}

func p3NewHelperCopy(t *testing.T) *p3HelperCopy {
	t.Helper()
	// t.TempDir supplies the private absolute ephemeral owner used when durable
	// persistence is off. Its basename deliberately exercises spaces and quotes.
	root := filepath.Join(t.TempDir(), "skill 'quoted' space")
	return &p3HelperCopy{root: root, source: map[string][]byte{
		"SKILL.md":       []byte("Read {{skill_dir}}/helpers/say.sh"),
		"helpers/say.sh": []byte("#!/bin/sh\nprintf ORIGINAL\n"),
	}, executable: map[string]bool{"helpers/say.sh": true}}
}

func TestP3FrozenHelperDelivery(t *testing.T) {
	copy := p3NewHelperCopy(t)
	workspace := t.TempDir()
	h := new(p3ReadingHarness)
	runner := NewAgentExecutorFixed(h)
	req := engine.StepRequest{Step: &workflow.Step{ID: "agent", Type: workflow.StepAgent, SnapshotAgentPrompt: string(copy.source["SKILL.md"])}, ExecutionDir: workspace}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for attempt := 0; attempt < 2; attempt++ {
		if err := copy.prepare(); err != nil {
			t.Fatal(err)
		}
		if attempt == 1 {
			req.ResumeSessionID = "p3-conversation"
			req.Message = "Use the helper command you remembered."
		}
		result, err := runner.Execute(ctx, p3BoundRequest(req, copy.root), &captureReporter{})
		if err != nil || result.Status != step.StatusSucceeded {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		if err := copy.verify(); err != nil {
			t.Fatal(err)
		}
		if err := copy.cleanup(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(copy.root); !os.IsNotExist(err) {
			t.Fatal("copy not cleaned")
		}
	}
	if len(h.outputs) != 2 || h.outputs[0] != "ORIGINAL" || h.outputs[1] != "ORIGINAL" {
		t.Fatalf("outputs=%q", h.outputs)
	}
	if h.specs[0].Cwd != workspace || h.specs[1].Cwd != workspace || h.specs[1].Resume != "p3-conversation" {
		t.Fatal("CWD/conversation changed")
	}
	if req.Step.AgentPrompt() != string(copy.source["SKILL.md"]) {
		t.Fatal("caller prompt mutated")
	}
	t.Log("fresh/resume helper output=ORIGINAL; remembered path stable; CWD and conversation preserved; copies removed")
}

func TestP3FrozenHelperInventory(t *testing.T) {
	for _, mutation := range []string{"add", "remove", "rename", "modify", "link", "empty_directory"} {
		t.Run(mutation, func(t *testing.T) {
			copy := p3NewHelperCopy(t)
			if err := copy.prepare(); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(copy.root, "helpers", "say.sh")
			var err error
			switch mutation {
			case "add":
				err = os.WriteFile(filepath.Join(copy.root, "extra"), []byte("extra"), 0400)
			case "remove":
				err = os.Remove(file)
			case "rename":
				err = os.Rename(file, filepath.Join(copy.root, "renamed"))
			case "modify":
				if err = os.Chmod(file, 0600); err == nil {
					err = os.WriteFile(file, []byte("MODIFIED"), 0600)
				}
			case "link":
				if err = os.Remove(file); err == nil {
					err = os.Symlink("/outside", file)
				}
			case "empty_directory":
				err = os.Mkdir(filepath.Join(copy.root, "added"), 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := copy.verify(); !errors.Is(err, p3CopyChanged) {
				t.Fatalf("mutation accepted: %v", err)
			}
			if !bytes.Contains(copy.source["helpers/say.sh"], []byte("ORIGINAL")) {
				t.Fatal("source changed")
			}
			if err := copy.prepare(); err != nil {
				t.Fatal(err)
			}
			if err := copy.verify(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestP3FrozenHelperOwnership(t *testing.T) {
	copy := p3NewHelperCopy(t)
	if err := copy.prepare(); err != nil {
		t.Fatal(err)
	}
	// This helper has no descendants: it reports readiness, then blocks on its
	// stdin. An explicit pipe handshake replaces sleep-based timing.
	cmd := exec.Command("sh", "-c", "printf 'READY\\n'; read release; exit 7")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Process.Kill() })
	ready := make(chan error, 1)
	go func() {
		line, e := bufio.NewReader(stdout).ReadString('\n')
		if e == nil && line != "READY\n" {
			e = fmt.Errorf("bad readiness")
		}
		ready <- e
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper readiness timed out")
	}
	exited := make(chan struct{})
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait(); close(exited) }()
	copy.ownerDone = exited
	// A failed Close leaves no termination evidence. Even a cancelled request
	// cannot authorize another materialization or automatic cleanup.
	if err := copy.prepare(); !errors.Is(err, p3OwnerUnknown) {
		t.Fatal("active owner replaced")
	}
	if err := copy.cleanup(); !errors.Is(err, p3OwnerUnknown) {
		t.Fatal("active owner deleted")
	}
	if _, err := os.Stat(copy.root); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(stdin, "release\n"); err != nil {
		t.Fatal(err)
	}
	_ = stdin.Close()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("termination not confirmed")
	}
	var exitError *exec.ExitError
	if err := <-waitErr; !errors.As(err, &exitError) || exitError.ExitCode() != 7 {
		t.Fatalf("expected confirmed nonzero exit: %v", err)
	}
	if err := copy.cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := copy.prepare(); err != nil {
		t.Fatal(err)
	}
	t.Log("active owner fenced; Wait confirmed exit 7; cleanup and stable-root rebuild allowed")
}
