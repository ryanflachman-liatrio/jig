package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func savedCaptureRun(t *testing.T) (string, string, workflow.AssetManifest, workflowSnapshot) {
	t.Helper()
	dir := t.TempDir()
	writeCaptureFixture(t, dir, "script.sh", []byte("echo ORIGINAL\n"))
	writeCaptureFixture(t, dir, "future.sh", []byte("echo FUTURE\n"))
	writeCaptureFixture(t, dir, "workflow.toml", []byte("[workflow]\nname='capture'\nversion='1'\n[[step]]\nid='worker'\ntype='command'\nscript='script.sh'\n[[step]]\nid='future'\ntype='command'\nscript='future.sh'\ndepends_on=['worker']\n"))
	wf, err := workflow.Load(filepath.Join(dir, "workflow.toml"))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, ".jig")
	rd, err := datastore.RunDir(root, "saved")
	if err != nil {
		t.Fatal(err)
	}
	if err := persistWorkflowSnapshot(rd, wf); err != nil {
		t.Fatal(err)
	}
	writeJournal(t, rd, []Event{RunStarted{RunID: "saved", Workflow: "capture", Steps: []string{"worker", "future"}}, StepStatus{RunID: "saved", StepID: "worker", From: "pending", To: "running"}})
	var m workflow.AssetManifest
	var snap workflowSnapshot
	raw, err := os.ReadFile(filepath.Join(rd, "authoring/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(rd, "workflow.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	// Valid substitute checkout files must never repair a damaged capture.
	writeCaptureFixture(t, dir, "script.sh", []byte("echo SUBSTITUTE\n"))
	writeCaptureFixture(t, dir, "future.sh", []byte("echo SUBSTITUTE\n"))
	return root, rd, m, snap
}
func resignCaptureRecords(t *testing.T, rd string, m workflow.AssetManifest, snap workflowSnapshot) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	writeCaptureFixture(t, rd, "authoring/manifest.json", raw)
	snap.Authoring.SHA256 = authoringDigest(raw)
	snap.Authoring.Bindings = len(m.Bindings)
	snap.Authoring.Blobs = len(m.Blobs)
	snap.Authoring.Bytes = 0
	for _, b := range m.Blobs {
		snap.Authoring.Bytes += b.Size
	}
	snap.Checksum = ""
	raw, err = json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	snap.Checksum = authoringDigest(raw)
	raw, err = json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	writeCaptureFixture(t, rd, "workflow.json", raw)
}

func TestResumeRejectsAuthoringAssetDamage(t *testing.T) {
	for _, damage := range []string{"missing_manifest", "truncated_manifest", "missing_blob", "same_size_blob", "swapped_blobs", "total_bytes", "future_blob", "size", "hash", "swapped_bindings", "missing_binding", "unknown_kind", "unknown_owner", "duplicate", "reference", "declaring", "source_escape", "execute_bit", "manifest_symlink", "blob_symlink", "directory_symlink", "snapshot_symlink", "count", "bytes", "version", "legacy", "graph_mismatch", "blob_name"} {
		t.Run(damage, func(t *testing.T) {
			root, rd, m, snap := savedCaptureRun(t)
			script := -1
			future := -1
			for i, b := range m.Bindings {
				if b.Owner == "worker" {
					script = i
				}
				if b.Owner == "future" {
					future = i
				}
			}
			path := filepath.Join(rd, "authoring/blobs", m.Bindings[script].SHA256)
			resign := false
			switch damage {
			case "missing_manifest":
				os.Remove(filepath.Join(rd, "authoring/manifest.json"))
			case "truncated_manifest":
				writeCaptureFixture(t, rd, "authoring/manifest.json", []byte("{"))
			case "missing_blob":
				os.Remove(path)
			case "same_size_blob":
				writeCaptureFixture(t, rd, "authoring/blobs/"+m.Bindings[script].SHA256, []byte(strings.Repeat("x", m.Bindings[script].Size)))
			case "swapped_blobs":
				other := filepath.Join(rd, "authoring/blobs", m.Bindings[future].SHA256)
				first, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				second, err := os.ReadFile(other)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, second, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(other, first, 0600); err != nil {
					t.Fatal(err)
				}
			case "total_bytes":
				m.Blobs = nil
				for i := 0; i < 17; i++ {
					m.Blobs = append(m.Blobs, workflow.AssetBlob{SHA256: fmt.Sprintf("%064x", i+1), Size: 1 << 20})
				}
				resign = true
			case "future_blob":
				os.Remove(filepath.Join(rd, "authoring/blobs", m.Bindings[future].SHA256))
			case "size":
				m.Bindings[script].Size++
				resign = true
			case "hash":
				m.Bindings[script].SHA256 = strings.Repeat("a", 64)
				resign = true
			case "swapped_bindings":
				m.Bindings[script].SHA256, m.Bindings[future].SHA256 = m.Bindings[future].SHA256, m.Bindings[script].SHA256
				resign = true
			case "missing_binding":
				m.Bindings = append(m.Bindings[:script], m.Bindings[script+1:]...)
				resign = true
			case "unknown_kind":
				m.Bindings[script].Kind = "unknown"
				resign = true
			case "unknown_owner":
				m.Bindings[script].Owner = "unknown"
				resign = true
			case "duplicate":
				m.Bindings = append(m.Bindings, m.Bindings[script])
				resign = true
			case "reference":
				m.Bindings[script].Reference = "future.sh"
				resign = true
			case "declaring":
				m.Bindings[script].Declaring = "/synthetic/other.toml"
				resign = true
			case "source_escape":
				m.Bindings[script].Source = "/outside/script.sh"
				resign = true
			case "execute_bit":
				m.Bindings[script].Executable = !m.Bindings[script].Executable
				raw, _ := json.Marshal(m)
				writeCaptureFixture(t, rd, "authoring/manifest.json", raw)
			case "manifest_symlink", "blob_symlink", "snapshot_symlink":
				target := filepath.Join(rd, "authoring/manifest.json")
				if damage == "blob_symlink" {
					target = path
				}
				if damage == "snapshot_symlink" {
					target = filepath.Join(rd, "workflow.json")
				}
				outside := filepath.Join(t.TempDir(), "outside")
				raw, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(outside, raw, 0600); err != nil {
					t.Fatal(err)
				}
				os.Remove(target)
				if err := os.Symlink(outside, target); err != nil {
					t.Fatal(err)
				}
			case "directory_symlink":
				old := filepath.Join(rd, "authoring/blobs")
				outside := filepath.Join(t.TempDir(), "blobs")
				if err := os.Rename(old, outside); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, old); err != nil {
					t.Fatal(err)
				}
			case "count":
				for len(m.Bindings) < 2049 {
					m.Bindings = append(m.Bindings, m.Bindings[script])
				}
				resign = true
			case "bytes":
				for i := range m.Blobs {
					m.Blobs[i].Size = (1 << 20) + 1
				}
				resign = true
			case "version":
				snap.Authoring.Version = 2
				resign = true
			case "legacy":
				snap.Authoring = nil
				snap.Checksum = ""
				raw, _ := json.Marshal(snap)
				writeCaptureFixture(t, rd, "workflow.json", raw)
			case "graph_mismatch":
				snap.ExpandedSteps[0].Script = "future.sh"
				resign = true
			case "blob_name":
				m.Blobs[0].SHA256 = "../../outside"
				resign = true
			}
			if resign {
				sort.Slice(m.Bindings, func(i, j int) bool { return bindingSortKey(m.Bindings[i]) < bindingSortKey(m.Bindings[j]) })
				resignCaptureRecords(t, rd, m, snap)
			}
			var starts atomic.Int32
			mngr := NewManager(captureExecFunc(func(_ context.Context, _ StepRequest, _ Reporter) (*step.Result, error) {
				starts.Add(1)
				return &step.Result{Status: step.StatusSucceeded}, nil
			}), root)
			run, err := mngr.Resume("saved")
			var capture workflow.CaptureError
			if run != nil || err == nil || !errors.As(err, &capture) || starts.Load() != 0 || len(mngr.Runs()) != 0 {
				t.Fatalf("run=%v err=%v starts=%d", run, err, starts.Load())
			}
			held, err := RunLockState(rd)
			if err != nil || held {
				t.Fatalf("lease=%v err=%v", held, err)
			}
			t.Logf("%s: typed=%s workers=0 registered=0 lease_released=true", damage, capture)
		})
	}
}
func bindingSortKey(b workflow.AssetBinding) string {
	return b.Owner + "\x00" + b.Declaration + "\x00" + string(b.Kind) + "\x00" + b.Declaring + "\x00" + b.Reference + "\x00" + b.Relative
}

func TestWorkflowSnapshotDecodeIsMetadataOnly(t *testing.T) {
	root, rd, _, snap := savedCaptureRun(t)
	// Display decode may inspect an old modern graph with no authority, but Start
	// must never recapture it from replacement authoring files.
	snap.Authoring = nil
	snap.Checksum = ""
	raw, _ := json.Marshal(snap)
	display, err := DecodeWorkflowSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager(nil, "").Start(display); !errors.Is(err, workflow.CaptureMissing) {
		t.Fatalf("display authorized Start: %v", err)
	}
	// A legacy command with unavailable script/schema/profile paths still decodes.
	source := "[workflow]\nname='legacy'\nversion='1'\n[[step]]\nid='worker'\ntype='command'\nscript='missing.sh'\noutput='out.json'\n[step.validate]\noutput_schema='missing.json'\n"
	legacy := workflowSnapshot{TOML: source, SHA256: authoringDigest([]byte(source)), SourcePath: "/unavailable/workflow.toml", BaseDir: "/unavailable"}
	raw, _ = json.Marshal(legacy)
	if _, err := DecodeWorkflowSnapshot(raw); err != nil {
		t.Fatalf("legacy performed source IO: %v", err)
	}
	writeCaptureFixture(t, rd, "workflow.json", raw)
	if _, err := NewManager(nil, root).Resume("saved"); err == nil || !strings.Contains(err.Error(), "new run") {
		t.Fatalf("legacy execution: %v", err)
	}
	if _, err := NewManager(nil, "").Resume("saved"); err == nil || !strings.Contains(err.Error(), "persistence") {
		t.Fatalf("memory reopen: %v", err)
	}
}

func TestSnapshotReferenceDoesNotAuthorizeReads(t *testing.T) {
	root, rd, _, snap := savedCaptureRun(t)
	snap.Authoring.Path = "/unavailable/private-manifest.json"
	snap.Checksum = ""
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	snap.Checksum = authoringDigest(raw)
	raw, err = json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeWorkflowSnapshot(raw); err != nil {
		t.Fatalf("display followed manifest reference: %v", err)
	}
	writeCaptureFixture(t, rd, "workflow.json", raw)
	if _, err := NewManager(nil, root).Resume("saved"); !errors.Is(err, workflow.CaptureMissing) {
		t.Fatalf("strict accepted reference: %v", err)
	}
}

func TestResumeRequiresFutureFamilyCapture(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "skill"), 0700); err != nil {
		t.Fatal(err)
	}
	writeCaptureFixture(t, dir, "skill/SKILL.md", []byte("---\nname: fixture\ndescription: Synthetic\n---\nDiscover synthetic items.\n"))
	writeCaptureFixture(t, dir, "future.sh", []byte("echo ORIGINAL\n"))
	source := "[workflow]\nname='future-family'\nversion='1'\n[[step]]\nid='discover'\ntype='agent'\nskill='skill'\n[step.schema]\nitems={list='text'}\n[[step]]\nid='family'\ntype='command'\nscript='future.sh'\ndepends_on=['discover']\n[step.foreach]\nitems='@discover.items'\nas='item'\nmax_items=2\n"
	writeCaptureFixture(t, dir, "workflow.toml", []byte(source))
	wf, err := workflow.Load(filepath.Join(dir, "workflow.toml"))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, ".jig")
	rd, err := datastore.RunDir(root, "family")
	if err != nil {
		t.Fatal(err)
	}
	if err := persistWorkflowSnapshot(rd, wf); err != nil {
		t.Fatal(err)
	}
	writeJournal(t, rd, []Event{RunStarted{RunID: "family", Workflow: wf.Meta.Name, Steps: []string{"discover", "family"}}})
	_, a, err := loadAuthoringSnapshot(rd)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range a.assets.Manifest().Bindings {
		if b.Owner == "family" {
			if err := os.Remove(filepath.Join(rd, "authoring/blobs", b.SHA256)); err != nil {
				t.Fatal(err)
			}
		}
	}
	m := NewManager(nil, root)
	if run, err := m.Resume("family"); run != nil || !errors.Is(err, workflow.CaptureStorage) || len(m.Runs()) != 0 {
		t.Fatalf("future family: %v %v", run, err)
	}
	held, err := RunLockState(rd)
	if err != nil || held {
		t.Fatal("failed restore retained lease")
	}
	t.Log("missing future fan-out template blob: workers=0 Open=0 lease released")
}
