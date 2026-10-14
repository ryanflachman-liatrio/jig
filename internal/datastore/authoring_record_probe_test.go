//go:build darwin || linux

package datastore_test

// P3 record prototype only. No production writer or restore path calls this.
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
	"golang.org/x/sys/unix"
)

const recordManifestLimit = 8 << 20
const recordSnapshotLimit = 32 << 20

var errRecord = errors.New("authoring record integrity")

type recordBinding struct {
	Owner      string `json:"owner"`
	Kind       string `json:"kind"`
	Reference  string `json:"reference"`
	Declaring  string `json:"declaring"`
	Anchor     string `json:"anchor"`
	Canonical  string `json:"canonical"`
	Relative   string `json:"relative"`
	Size       int    `json:"size"`
	Executable bool   `json:"executable"`
	Digest     string `json:"digest"`
}
type recordBlob struct {
	Digest string `json:"digest"`
	Size   int    `json:"size"`
}
type recordManifest struct {
	Bindings []recordBinding `json:"bindings"`
	Blobs    []recordBlob    `json:"blobs"`
}
type recordReference struct {
	Version  int    `json:"version"`
	Path     string `json:"path"`
	Digest   string `json:"digest"`
	Bindings int    `json:"bindings"`
	Blobs    int    `json:"blobs"`
	Bytes    int    `json:"bytes"`
}
type recordPayload struct {
	Workflow json.RawMessage `json:"workflow"`
	Assets   recordReference `json:"assets"`
}
type recordEnvelope struct {
	Payload  recordPayload `json:"payload"`
	Checksum string        `json:"checksum"`
}

func recordHash(data []byte) string    { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func recordJSON(v any) ([]byte, error) { return json.Marshal(v) }
func recordDigest(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && strings.ToLower(s) == s
}
func recordKey(b recordBinding) string {
	return b.Owner + "\x00" + b.Kind + "\x00" + b.Relative + "\x00" + b.Reference
}

// Token preflight bounds containers before allocating typed record slices.
// Strings remain bounded by the encoded byte cap; path fields have a stricter
// post-decode bound. Duplicate object keys are rejected, including nested keys.
func recordPreflight(data []byte, limit int) error {
	if len(data) > limit {
		return errRecord
	}
	d := json.NewDecoder(bytes.NewReader(data))
	type frame struct {
		kind  json.Delim
		count int
		keys  map[string]bool
		key   bool
	}
	var stack []frame
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errRecord
		}
		if len(stack) > 0 {
			f := &stack[len(stack)-1]
			if f.kind == '{' && f.key {
				if s, ok := tok.(string); ok {
					if f.keys[s] {
						return errRecord
					}
					f.keys[s] = true
					f.key = false
					continue
				}
			}
			if delim, ok := tok.(json.Delim); !ok || delim != '}' && delim != ']' {
				f.count++
				if f.kind == '[' && f.count > 2048 {
					return errRecord
				}
				if f.kind == '{' {
					f.key = true
				}
			}
		}
		if delim, ok := tok.(json.Delim); ok {
			if delim == '{' || delim == '[' {
				// The prototype wire records have a finite known structure. A deeper
				// arbitrary JSON document is never part of the asset manifest contract.
				if len(stack) >= 64 {
					return errRecord
				}
				stack = append(stack, frame{kind: delim, keys: map[string]bool{}, key: delim == '{'})
			} else {
				if len(stack) == 0 {
					return errRecord
				}
				stack = stack[:len(stack)-1]
			}
		}
	}
	if !json.Valid(data) || len(stack) != 0 {
		return errRecord
	}
	return nil
}
func recordDecode(data []byte, limit int, v any) error {
	if err := recordPreflight(data, limit); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return errRecord
	}
	return nil
}
func recordMake(bindings []recordBinding, blobs map[string][]byte, wf json.RawMessage) ([]byte, []byte, error) {
	m := recordManifest{Bindings: append([]recordBinding(nil), bindings...)}
	sort.Slice(m.Bindings, func(i, j int) bool { return recordKey(m.Bindings[i]) < recordKey(m.Bindings[j]) })
	total := 0
	for digest, data := range blobs {
		m.Blobs = append(m.Blobs, recordBlob{digest, len(data)})
		total += len(data)
	}
	sort.Slice(m.Blobs, func(i, j int) bool { return m.Blobs[i].Digest < m.Blobs[j].Digest })
	manifest, err := recordJSON(m)
	if err != nil {
		return nil, nil, err
	}
	payload := recordPayload{wf, recordReference{1, "authoring/manifest.json", recordHash(manifest), len(bindings), len(blobs), total}}
	raw, err := recordJSON(payload)
	if err != nil {
		return nil, nil, err
	}
	snapshot, err := recordJSON(recordEnvelope{payload, recordHash(raw)})
	return manifest, snapshot, err
}

// Pure: the only input is bytes. Require expanded graph data to avoid the
// historical decoder's legacy source-reading branch, now replaced by pure metadata decoding.
func recordDisplay(data []byte) (recordEnvelope, *workflow.Workflow, error) {
	var e recordEnvelope
	if err := recordDecode(data, recordSnapshotLimit, &e); err != nil {
		return e, nil, err
	}
	raw, err := recordJSON(e.Payload)
	if err != nil || recordHash(raw) != e.Checksum {
		return e, nil, errRecord
	}
	r := e.Payload.Assets
	if r.Version != 1 || r.Path != "authoring/manifest.json" || !recordDigest(r.Digest) || r.Bindings < 0 || r.Bindings > 2048 || r.Blobs < 0 || r.Blobs > 1024 || r.Bytes < 0 || r.Bytes > 16<<20 {
		return e, nil, errRecord
	}
	var header struct {
		Expanded []json.RawMessage `json:"expanded_steps"`
	}
	if json.Unmarshal(e.Payload.Workflow, &header) != nil || len(header.Expanded) == 0 || len(header.Expanded) > 1024 {
		return e, nil, errRecord
	}
	wf, err := engine.DecodeWorkflowSnapshot(e.Payload.Workflow)
	return e, wf, err
}

// Reads fixed record paths relative to an already pinned run handle. Every
// component is opened without following links; nonregular files never block.
func recordRead(root *os.File, path string, limit int) ([]byte, error) {
	if !filepath.IsLocal(path) {
		return nil, errRecord
	}
	fd, err := unix.Openat(int(root.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(path, "/")
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, e := unix.Openat(fd, part, flags, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	f := os.NewFile(uintptr(fd), "record")
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > int64(limit) {
		return nil, errRecord
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil || len(data) > limit {
		return nil, errRecord
	}
	return data, nil
}
func recordRestore(root *os.File) (recordManifest, error) {
	var m recordManifest
	snap, err := recordRead(root, "workflow.json", recordSnapshotLimit)
	if err != nil {
		return m, err
	}
	e, wf, err := recordDisplay(snap)
	if err != nil {
		return m, err
	}
	data, err := recordRead(root, "authoring/manifest.json", recordManifestLimit)
	if err != nil {
		return m, err
	}
	r := e.Payload.Assets
	if recordHash(data) != r.Digest {
		return m, errRecord
	}
	if err = recordDecode(data, recordManifestLimit, &m); err != nil {
		return m, err
	}
	if len(m.Bindings) != r.Bindings || len(m.Blobs) != r.Blobs {
		return m, errRecord
	}
	blobs := map[string]int{}
	total := 0
	last := ""
	for _, b := range m.Blobs {
		if !recordDigest(b.Digest) || b.Digest <= last || b.Size < 0 || b.Size > 1<<20 {
			return m, errRecord
		}
		last = b.Digest
		total += b.Size
		if total > 16<<20 {
			return m, errRecord
		}
		blobs[b.Digest] = b.Size
		content, err := recordRead(root, "authoring/blobs/"+b.Digest, 1<<20)
		if err != nil || len(content) != b.Size || recordHash(content) != b.Digest {
			return m, errRecord
		}
	}
	if total != r.Bytes {
		return m, errRecord
	}
	owners := map[string]bool{"": true}
	for _, s := range wf.Steps {
		owners[s.ID] = true
	}
	kinds := map[string]bool{"workflow": true, "module": true, "script": true, "skill": true, "agent_file": true, "schema": true, "validation_schema": true, "output_template": true}
	last = ""
	used := map[string]bool{}
	for _, b := range m.Bindings {
		key := recordKey(b)
		size, ok := blobs[b.Digest]
		if !owners[b.Owner] || !kinds[b.Kind] || !ok || b.Size != size || key <= last {
			return m, errRecord
		}
		last = key
		used[b.Digest] = true
		for _, s := range []string{b.Owner, b.Kind, b.Reference, b.Declaring, b.Anchor, b.Canonical, b.Relative} {
			if len(s) > 4096 || strings.ContainsRune(s, 0) {
				return m, errRecord
			}
		}
		if b.Reference == "" || !filepath.IsAbs(b.Declaring) || !filepath.IsAbs(b.Anchor) || !filepath.IsAbs(b.Canonical) {
			return m, errRecord
		}
		if b.Relative != "" && !filepath.IsLocal(b.Relative) {
			return m, errRecord
		}
	}
	if len(used) != len(blobs) {
		return m, errRecord
	}
	// The fixture's expected inventory is independently asserted by the tests.
	// Full graph-derived production coverage remains task 4, not this prototype.
	return m, nil
}

// Test-only fault seam surrounds actual writes, syncs, closes and renames.
// A successful hook never substitutes for the real filesystem operation.
func recordPublish(dir string, m, s []byte, blobs map[string][]byte, hook func(string) error) error {
	call := func(s string) error {
		if hook != nil {
			return hook(s)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Join(dir, "authoring/blobs"), 0700); err != nil {
		return err
	}
	publish := func(name string, data []byte) error {
		parent := filepath.Dir(filepath.Join(dir, name))
		f, err := os.CreateTemp(parent, ".pending-")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		defer f.Close()
		for _, stage := range []string{"write", "sync", "close", "rename"} {
			if err = call(name + ":" + stage); err != nil {
				return err
			}
			switch stage {
			case "write":
				var n int
				n, err = f.Write(data)
				if err == nil && n != len(data) {
					err = io.ErrShortWrite
				}
			case "sync":
				err = f.Sync()
			case "close":
				err = f.Close()
			case "rename":
				err = os.Rename(f.Name(), filepath.Join(dir, name))
			}
			if err != nil {
				return err
			}
		}
		d, err := os.Open(parent)
		if err != nil {
			return err
		}
		err = d.Sync()
		closeErr := d.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		return call(name + ":published")
	}
	keys := make([]string, 0, len(blobs))
	for k := range blobs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !recordDigest(k) || recordHash(blobs[k]) != k {
			return errRecord
		}
		if err := publish("authoring/blobs/"+k, blobs[k]); err != nil {
			return err
		}
	}
	if err := publish("authoring/manifest.json", m); err != nil {
		return err
	}
	// Commit newly created subdirectory entries before publishing workflow.json.
	for _, path := range []string{"authoring", "."} {
		d, err := os.Open(filepath.Join(dir, path))
		if err != nil {
			return err
		}
		err = d.Sync()
		ce := d.Close()
		if err != nil {
			return err
		}
		if ce != nil {
			return ce
		}
	}
	return publish("workflow.json", s)
}

func recordFixture(t *testing.T) ([]recordBinding, map[string][]byte, json.RawMessage) {
	t.Helper()
	source := "[workflow]\nname='record-probe'\nversion='1'\n[[step]]\nid='worker'\ntype='command'\nrun='true'\n"
	wf, err := workflow.Decode(source, "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"toml": source, "sha256": recordHash([]byte(source)), "meta": wf.Meta, "defaults": wf.Defaults, "expanded_steps": wf.Steps, "source_path": "/unavailable/original/workflow.toml", "base_dir": "/unavailable/original"})
	if err != nil {
		t.Fatal(err)
	}
	blobs := map[string][]byte{}
	var bindings []recordBinding
	for _, kind := range []string{"workflow", "module", "script", "skill", "agent_file", "schema", "validation_schema", "output_template"} {
		data := []byte("ORIGINAL " + kind)
		if kind == "skill" {
			data = []byte("ORIGINAL script")
		}
		digest := recordHash(data)
		blobs[digest] = data
		relative := ""
		if kind == "skill" {
			relative = "helpers/run.sh"
		}
		owner := "worker"
		if kind == "workflow" || kind == "module" {
			owner = ""
		}
		bindings = append(bindings, recordBinding{owner, kind, kind + ".fixture", "/source/module.toml", "/source", "/source/" + kind + ".fixture", relative, len(data), kind == "script" || kind == "skill", digest})
	}
	return bindings, blobs, raw
}
func recordRoot(t *testing.T, dir string) *os.File {
	t.Helper()
	f, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}
func recordMustMake(t *testing.T, b []recordBinding, blobs map[string][]byte, w json.RawMessage) ([]byte, []byte) {
	t.Helper()
	m, s, e := recordMake(b, blobs, w)
	if e != nil {
		t.Fatal(e)
	}
	return m, s
}
func recordMustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestAuthoringAssetRecordRoundTrip(t *testing.T) {
	b, blobs, w := recordFixture(t)
	m, s := recordMustMake(t, b, blobs, w)
	t.Run("provenance_dedup_and_pure_display", func(t *testing.T) {
		dir := t.TempDir()
		var published []string
		if err := recordPublish(dir, m, s, blobs, func(stage string) error {
			if strings.HasSuffix(stage, ":published") {
				published = append(published, stage)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if published[len(published)-1] != "workflow.json:published" {
			t.Fatal(published)
		}
		got, err := recordRestore(recordRoot(t, dir))
		if err != nil {
			t.Fatal(err)
		}
		sort.Slice(b, func(i, j int) bool { return recordKey(b[i]) < recordKey(b[j]) })
		if !reflect.DeepEqual(got.Bindings, b) || len(got.Blobs) != 7 {
			t.Fatalf("round trip bindings=%d blobs=%d", len(got.Bindings), len(got.Blobs))
		}
		for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
			b[i], b[j] = b[j], b[i]
		}
		m2, s2 := recordMustMake(t, b, blobs, w)
		if !bytes.Equal(m, m2) || !bytes.Equal(s, s2) {
			t.Fatal("encoding depends on input order")
		}
		if err := os.RemoveAll(filepath.Join(dir, "authoring")); err != nil {
			t.Fatal(err)
		}
		if _, wf, err := recordDisplay(s); err != nil || len(wf.Steps) != 1 {
			t.Fatalf("pure decode err=%v", err)
		}
		if _, err := recordRestore(recordRoot(t, dir)); err == nil {
			t.Fatal("strict restore repaired missing capture")
		}
		t.Logf("bindings=8 blobs=7 manifest=%s snapshot=%s; publication workflow-last; display works with absent captures/source", recordHash(m), recordHash(s))
	})
	t.Run("damage_rejected_without_substitution", func(t *testing.T) {
		for _, damage := range []string{"blob_missing", "same_size", "manifest_truncated", "snapshot_checksum", "blob_link", "directory_link", "fifo", "manifest_link"} {
			t.Run(damage, func(t *testing.T) {
				dir := t.TempDir()
				if err := recordPublish(dir, m, s, blobs, nil); err != nil {
					t.Fatal(err)
				}
				key := b[0].Digest
				path := filepath.Join(dir, "authoring/blobs", key)
				switch damage {
				case "blob_missing":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				case "same_size":
					recordMustWrite(t, path, bytes.Repeat([]byte("X"), len(blobs[key])))
				case "manifest_truncated":
					recordMustWrite(t, filepath.Join(dir, "authoring/manifest.json"), m[:len(m)/2])
				case "snapshot_checksum":
					var e recordEnvelope
					if err := json.Unmarshal(s, &e); err != nil {
						t.Fatal(err)
					}
					e.Payload.Assets.Bytes++
					bad, _ := json.Marshal(e)
					recordMustWrite(t, filepath.Join(dir, "workflow.json"), bad)
				case "blob_link", "manifest_link":
					if damage == "manifest_link" {
						path = filepath.Join(dir, "authoring/manifest.json")
					}
					target := filepath.Join(t.TempDir(), "valid")
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					recordMustWrite(t, target, data)
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, path); err != nil {
						t.Fatal(err)
					}
				case "directory_link":
					old := filepath.Join(dir, "authoring/blobs")
					target := filepath.Join(dir, "moved")
					if err := os.Rename(old, target); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink("../moved", old); err != nil {
						t.Fatal(err)
					}
				case "fifo":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := unix.Mkfifo(path, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := recordRestore(recordRoot(t, dir)); err == nil {
					t.Fatal("damage accepted")
				}
			})
		}
	})
	t.Run("metadata_bounds_and_bindings", func(t *testing.T) {
		for _, damage := range []string{"duplicate", "unknown_owner", "unknown_kind", "size", "path", "unsorted", "unused_blob"} {
			t.Run(damage, func(t *testing.T) {
				bb := append([]recordBinding(nil), b...)
				copyBlobs := map[string][]byte{}
				for k, v := range blobs {
					copyBlobs[k] = v
				}
				switch damage {
				case "duplicate":
					bb = append(bb, bb[0])
				case "unknown_owner":
					bb[0].Owner = "missing"
				case "unknown_kind":
					bb[0].Kind = "unknown"
				case "size":
					bb[0].Size++
				case "path":
					bb[0].Relative = "../escape"
				case "unused_blob":
					copyBlobs[recordHash([]byte("unused"))] = []byte("unused")
				}
				mm, ss := recordMustMake(t, bb, copyBlobs, w)
				if damage == "unsorted" {
					var manifest recordManifest
					json.Unmarshal(mm, &manifest)
					manifest.Bindings[0], manifest.Bindings[1] = manifest.Bindings[1], manifest.Bindings[0]
					mm, _ = json.Marshal(manifest)
					var e recordEnvelope
					json.Unmarshal(ss, &e)
					e.Payload.Assets.Digest = recordHash(mm)
					raw, _ := json.Marshal(e.Payload)
					e.Checksum = recordHash(raw)
					ss, _ = json.Marshal(e)
				}
				dir := t.TempDir()
				if err := recordPublish(dir, mm, ss, copyBlobs, nil); err != nil {
					t.Fatal(err)
				}
				if _, err := recordRestore(recordRoot(t, dir)); err == nil {
					t.Fatal("bad metadata accepted")
				}
			})
		}
		for _, n := range []int{2048, 2049} {
			data := []byte(`{"bindings":[` + strings.TrimSuffix(strings.Repeat("{},", n), ",") + `]}`)
			err := recordPreflight(data, recordManifestLimit)
			if (err == nil) != (n == 2048) {
				t.Fatalf("bindings=%d err=%v", n, err)
			}
		}
		for _, limit := range []int{recordManifestLimit, recordSnapshotLimit} {
			data := append([]byte("{}"), bytes.Repeat([]byte(" "), limit-2)...)
			if err := recordPreflight(data, limit); err != nil {
				t.Fatal(err)
			}
			if err := recordPreflight(append(data, ' '), limit); err == nil {
				t.Fatal("oversized metadata accepted")
			}
		}
		if err := recordPreflight([]byte(`{"bindings":[],"bindings":[]}`), recordManifestLimit); err == nil {
			t.Fatal("duplicate JSON key accepted")
		}
	})
	t.Run("publication_failure_never_commits_early", func(t *testing.T) {
		var stages []string
		dir := t.TempDir()
		if err := recordPublish(dir, m, s, blobs, func(stage string) error { stages = append(stages, stage); return nil }); err != nil {
			t.Fatal(err)
		}
		for _, stage := range stages {
			if stage == "workflow.json:published" {
				continue
			}
			t.Run(stage, func(t *testing.T) {
				dir := t.TempDir()
				injected := errors.New("synthetic I/O failure")
				err := recordPublish(dir, m, s, blobs, func(at string) error {
					if at == stage {
						return injected
					}
					return nil
				})
				if !errors.Is(err, injected) {
					t.Fatalf("error=%v", err)
				}
				if _, err := os.Stat(filepath.Join(dir, "workflow.json")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("committed before %s: %v", stage, err)
				}
				if _, err := recordRestore(recordRoot(t, dir)); err == nil {
					t.Fatal("incomplete publication restored")
				}
			})
		}
	})
}

func TestAuthoringAssetRecordEffectiveSnapshot(t *testing.T) {
	sourceDir := t.TempDir()
	source := "[workflow]\nname='effective-probe'\nversion='1'\n[[step]]\nid='worker'\ntype='agent'\nskill='skill'\noutput_template='template.md'\n"
	if err := os.Mkdir(filepath.Join(sourceDir, "skill"), 0700); err != nil {
		t.Fatal(err)
	}
	prompt := []byte("---\nname: probe\ndescription: synthetic proof\n---\nPROMPT ORIGINAL\n")
	template := []byte("  TEMPLATE ORIGINAL\n")
	recordMustWrite(t, filepath.Join(sourceDir, "workflow.toml"), []byte(source))
	recordMustWrite(t, filepath.Join(sourceDir, "skill/SKILL.md"), prompt)
	recordMustWrite(t, filepath.Join(sourceDir, "template.md"), template)
	wf, err := workflow.Load(filepath.Join(sourceDir, "workflow.toml"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"toml": source, "sha256": recordHash([]byte(source)), "meta": wf.Meta, "defaults": wf.Defaults, "expanded_steps": wf.Steps, "source_path": filepath.Join(sourceDir, "workflow.toml"), "base_dir": sourceDir})
	if err != nil {
		t.Fatal(err)
	}
	blobs := map[string][]byte{recordHash(prompt): prompt, recordHash(template): template}
	bindings := []recordBinding{
		{"worker", "skill", "skill", filepath.Join(sourceDir, "workflow.toml"), sourceDir, filepath.Join(sourceDir, "skill/SKILL.md"), "SKILL.md", len(prompt), false, recordHash(prompt)},
		{"worker", "output_template", "template.md", filepath.Join(sourceDir, "workflow.toml"), sourceDir, filepath.Join(sourceDir, "template.md"), "", len(template), false, recordHash(template)},
	}
	m, s := recordMustMake(t, bindings, blobs, raw)
	dir := t.TempDir()
	if err := recordPublish(dir, m, s, blobs, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(sourceDir); err != nil {
		t.Fatal(err)
	}
	if _, err := recordRestore(recordRoot(t, dir)); err != nil {
		t.Fatal(err)
	}
	_, restored, err := recordDisplay(s)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Steps[0].AgentPrompt() != "PROMPT ORIGINAL" || restored.Steps[0].OutputTemplateBody() != "TEMPLATE ORIGINAL" {
		t.Fatal("effective values were not retained")
	}
	// Compare independent fixture expectations to the stored raw representation.
	root := recordRoot(t, dir)
	for digest, want := range blobs {
		got, err := recordRead(root, "authoring/blobs/"+digest, 1<<20)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("raw/effective fixture mismatch: %v", err)
		}
	}
	t.Log("actual Load prompt/template snapshots and raw bytes survive source removal; legacy decode is excluded by prototype")
}
