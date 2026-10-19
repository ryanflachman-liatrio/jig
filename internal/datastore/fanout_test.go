package datastore

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func mustItem(index int, raw string) FanOutItem {
	item := json.RawMessage(raw)
	return FanOutItem{
		InstanceID: "analyze.__fanout__.g000.r000.i000" + itoa(index),
		Index:      index,
		Item:       item,
		ItemSHA256: ItemDigest(item),
	}
}

// itoa avoids pulling in strconv just for a single-digit test helper.
func itoa(i int) string {
	return string(rune('0' + i))
}

func TestFanOutManifestPath(t *testing.T) {
	if got := (Run{}).Step("analyze").FanOutManifest(0, 0); got != "" {
		t.Fatalf("empty runDir must yield empty path, got %q", got)
	}
	if got := RunAt("/run").Step("").FanOutManifest(0, 0); got != "" {
		t.Fatalf("empty familyID must yield empty path, got %q", got)
	}
	got := RunAt("/run").Step("analyze").FanOutManifest(1, 2)
	want := filepath.Join("/run", "steps", "analyze", "fanout", "generation-001-iteration-002.json")
	if got != want {
		t.Fatalf("FanOutManifestPath = %q, want %q", got, want)
	}
}

func TestWriteReadFanOutManifest_RoundTripAndOrder(t *testing.T) {
	runDir, err := Open(t.TempDir()).Create("run1")
	if err != nil {
		t.Fatal(err)
	}
	m := FanOutManifest{
		FamilyID:     "analyze",
		Generation:   0,
		Iteration:    0,
		SourceRef:    "@discover.targets",
		SourceDigest: "deadbeef",
		Items: []FanOutItem{
			mustItem(0, `{"name":"api"}`),
			mustItem(1, `{"name":"web"}`),
			mustItem(2, `{"name":"worker"}`),
		},
	}
	if err := runDir.Step(m.FamilyID).WriteFanOutManifest(m); err != nil {
		t.Fatalf("WriteFanOutManifest: %v", err)
	}

	got, err := runDir.Step("analyze").ReadFanOutManifest(0, 0)
	if err != nil {
		t.Fatalf("ReadFanOutManifest: %v", err)
	}
	if got.SchemaVersion != FanOutManifestVersion {
		t.Fatalf("SchemaVersion = %d, want %d", got.SchemaVersion, FanOutManifestVersion)
	}
	if got.SourceRef != m.SourceRef || got.SourceDigest != m.SourceDigest {
		t.Fatalf("got = %+v", got)
	}
	if len(got.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(got.Items))
	}
	// Order preservation: names must come back in exactly source order,
	// regardless of any encoding/decoding round trip.
	for i, want := range []string{"api", "web", "worker"} {
		var decoded struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(got.Items[i].Item, &decoded); err != nil {
			t.Fatalf("item %d: %v", i, err)
		}
		if decoded.Name != want {
			t.Fatalf("item %d name = %q, want %q", i, decoded.Name, want)
		}
		if got.Items[i].Index != i {
			t.Fatalf("item %d index = %d, want %d", i, got.Items[i].Index, i)
		}
	}
	if err := ValidateFanOutItems(got); err != nil {
		t.Fatalf("ValidateFanOutItems: %v", err)
	}
}

func TestWriteFanOutManifest_AtomicReplacement(t *testing.T) {
	runDir, err := Open(t.TempDir()).Create("run1")
	if err != nil {
		t.Fatal(err)
	}
	first := FanOutManifest{FamilyID: "analyze", Items: []FanOutItem{mustItem(0, `{"name":"a"}`)}}
	if err := runDir.Step(first.FamilyID).WriteFanOutManifest(first); err != nil {
		t.Fatal(err)
	}
	second := FanOutManifest{FamilyID: "analyze", Items: []FanOutItem{
		mustItem(0, `{"name":"a"}`),
		mustItem(1, `{"name":"b"}`),
	}}
	if err := runDir.Step(second.FamilyID).WriteFanOutManifest(second); err != nil {
		t.Fatal(err)
	}
	got, err := runDir.Step("analyze").ReadFanOutManifest(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items = %d, want 2 (second write must fully replace the first)", len(got.Items))
	}
	// No leftover temp file after a successful rename.
	path := runDir.Step("analyze").FanOutManifest(0, 0)
	assertOnlyEntries(t, filepath.Dir(path), filepath.Base(path))
}

func TestFanOutManifestDigest_ChangesWithContent(t *testing.T) {
	m := FanOutManifest{FamilyID: "analyze", Items: []FanOutItem{mustItem(0, `{"name":"a"}`)}}
	d1, err := FanOutManifestDigest(m)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := FanOutManifestDigest(m)
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Fatalf("digest is not deterministic: %q != %q", d1, d2)
	}
	m.Items = append(m.Items, mustItem(1, `{"name":"b"}`))
	d3, err := FanOutManifestDigest(m)
	if err != nil {
		t.Fatal(err)
	}
	if d3 == d1 {
		t.Fatal("digest did not change when manifest content changed")
	}
}

func TestValidateFanOutItems_DigestMismatch(t *testing.T) {
	m := FanOutManifest{
		FamilyID: "analyze",
		Items: []FanOutItem{
			{InstanceID: "i0", Index: 0, Item: json.RawMessage(`{"name":"a"}`), ItemSHA256: "not-the-real-digest"},
		},
	}
	if err := ValidateFanOutItems(m); err == nil {
		t.Fatal("expected digest mismatch error")
	}
}

func TestValidateFanOutItems_OutOfOrderIndex(t *testing.T) {
	m := FanOutManifest{
		FamilyID: "analyze",
		Items: []FanOutItem{
			mustItem(1, `{"name":"a"}`), // index 1 at position 0
		},
	}
	if err := ValidateFanOutItems(m); err == nil {
		t.Fatal("expected out-of-order index error")
	}
}

func TestReadFanOutManifest_CorruptAndVersionMismatch(t *testing.T) {
	runDir, err := Open(t.TempDir()).Create("run1")
	if err != nil {
		t.Fatal(err)
	}
	path := runDir.Step("analyze").FanOutManifest(0, 0)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("{not-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runDir.Step("analyze").ReadFanOutManifest(0, 0); err == nil {
		t.Fatal("expected error decoding corrupt manifest")
	}

	future := FanOutManifest{SchemaVersion: FanOutManifestVersion + 1, FamilyID: "analyze"}
	data, err := json.Marshal(future)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runDir.Step("analyze").ReadFanOutManifest(0, 0); err == nil {
		t.Fatal("expected error reading unsupported schema_version")
	}
}

func TestReadFanOutManifest_MissingFile(t *testing.T) {
	runDir, err := Open(t.TempDir()).Create("run1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runDir.Step("analyze").ReadFanOutManifest(0, 0); err == nil {
		t.Fatal("expected error reading a manifest that was never written")
	}
}

func TestFanOutManifest_PersistenceOff(t *testing.T) {
	if err := (Run{}).Step(FanOutManifest{FamilyID: "analyze"}.FamilyID).WriteFanOutManifest(FanOutManifest{FamilyID: "analyze"}); err != nil {
		t.Fatalf("persistence-off WriteFanOutManifest: %v", err)
	}
	got, err := (Run{}).Step("analyze").ReadFanOutManifest(0, 0)
	if err != nil {
		t.Fatalf("persistence-off ReadFanOutManifest: %v", err)
	}
	if got.FamilyID != "" {
		t.Fatalf("persistence-off read must yield a zero manifest, got %+v", got)
	}
	// No filesystem writes: confirm nothing was created under any plausible
	// relative path from the test's working directory.
	if _, err := os.Stat("steps"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("persistence-off write touched the filesystem: %v", err)
	}
}
