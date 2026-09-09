package datastore

// Fan-out manifests are the durable, per-generation expansion record for a
// [step.foreach] family: exactly which items, in what order, produced from
// which producer field. They are the authoritative creation record runtime
// children are rebuilt from on resume — Resume trusts the digested manifest
// and never re-reads the producer (see
// docs/plans/a8-dynamic-foreach-fan-out.md, "Durability, replay, and crash
// reopen"). Path layout:
//
//	steps/<family-id>/fanout/generation-000-iteration-000.json
//
// Generation counts manual operator resets; iteration counts bounded route
// rewinds within a generation. Both are zero-padded to three digits so
// directory listings sort chronologically.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// FanOutManifestVersion is the only manifest schema version this package
// writes or accepts. A future incompatible shape must bump this and add
// explicit support rather than silently misreading an old manifest.
const FanOutManifestVersion = 1

// FanOutItem is one ordered instance in a family's expansion: its stable
// runtime id, its zero-based source position, its canonical item bytes, and
// that item's digest (kept alongside the value so a manifest can be
// integrity-checked without recomputing digests from scratch).
type FanOutItem struct {
	InstanceID string          `json:"instance_id"`
	Index      int             `json:"index"`
	Item       json.RawMessage `json:"item"`
	ItemSHA256 string          `json:"item_sha256"`
}

// FanOutManifest is the versioned, per-generation record of a foreach
// family's expansion.
type FanOutManifest struct {
	SchemaVersion int    `json:"schema_version"`
	FamilyID      string `json:"family_id"`
	Generation    int    `json:"generation"`
	Iteration     int    `json:"iteration"`
	// SourceRef is the exact "@step.field" the family's [step.foreach] items
	// resolved. SourceDigest is the SHA-256 of the producer's raw output field
	// at the moment of expansion, so a manifest can be told apart from a stale
	// one produced by a since-changed producer output.
	SourceRef    string       `json:"source_ref"`
	SourceDigest string       `json:"source_digest"`
	Items        []FanOutItem `json:"items"`
}

// FanOutManifestPath returns the path to one generation/iteration's manifest
// inside runDir. An empty runDir or familyID (persistence off / no family)
// yields "" so callers can no-op.
func FanOutManifestPath(runDir, familyID string, generation, iteration int) string {
	if runDir == "" || familyID == "" {
		return ""
	}
	name := fmt.Sprintf("generation-%03d-iteration-%03d.json", generation, iteration)
	return filepath.Join(runDir, "steps", familyID, "fanout", name)
}

// WriteFanOutManifest atomically persists a family's expansion: written to a
// temp file and renamed into place so a reader (or a crash) never observes a
// partial manifest. SchemaVersion defaults to FanOutManifestVersion when zero
// so callers can omit it. Persistence-off (runDir == "") is a first-class
// no-op, matching every other datastore writer.
func WriteFanOutManifest(runDir string, m FanOutManifest) error {
	path := FanOutManifestPath(runDir, m.FamilyID, m.Generation, m.Iteration)
	if path == "" {
		return nil
	}
	if m.SchemaVersion == 0 {
		m.SchemaVersion = FanOutManifestVersion
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("datastore: create fanout dir: %w", err)
	}
	// Plain (non-indented) Marshal: json.RawMessage items are embedded as-is
	// only when the encoder doesn't have to reflow them for indentation —
	// MarshalIndent rewrites each embedded item with added whitespace, which
	// would silently invalidate every stored ItemSHA256 on read-back.
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("datastore: encode fanout manifest: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("datastore: write fanout manifest temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("datastore: rename fanout manifest: %w", err)
	}
	return nil
}

// ReadFanOutManifest loads and decodes one generation/iteration's manifest.
// Persistence-off (runDir == "") yields a zero manifest and a nil error, the
// same "nothing to read" convention as ReadSession. A missing file, corrupt
// JSON, or an unsupported schema_version all return an error: unlike a
// session file, a fan-out manifest is the authoritative creation record for
// runtime children, so silently degrading to "absent" would let Resume
// recreate children from a fresh (and possibly different) producer read.
func ReadFanOutManifest(runDir, familyID string, generation, iteration int) (FanOutManifest, error) {
	path := FanOutManifestPath(runDir, familyID, generation, iteration)
	if path == "" {
		return FanOutManifest{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return FanOutManifest{}, fmt.Errorf("datastore: read fanout manifest: %w", err)
	}
	var m FanOutManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return FanOutManifest{}, fmt.Errorf("datastore: decode fanout manifest: %w", err)
	}
	if m.SchemaVersion != FanOutManifestVersion {
		return FanOutManifest{}, fmt.Errorf("datastore: fanout manifest %s generation %d iteration %d schema_version = %d, want %d",
			familyID, generation, iteration, m.SchemaVersion, FanOutManifestVersion)
	}
	return m, nil
}

// ItemDigest returns the SHA-256 hex digest of one item's canonical JSON
// bytes. Both the manifest writer and Resume use this so an item's identity
// is defined once.
func ItemDigest(item json.RawMessage) string {
	sum := sha256.Sum256(item)
	return hex.EncodeToString(sum[:])
}

// FanOutManifestDigest returns the SHA-256 hex digest of the manifest's
// canonical JSON encoding. This is the value journaled in the engine's
// FanOutExpanded event: Resume compares it against a freshly computed digest
// of the on-disk manifest before trusting it, so a manifest that was rewritten
// (or never durably renamed) after the event was journaled fails closed.
func FanOutManifestDigest(m FanOutManifest) (string, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("datastore: digest fanout manifest: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// ValidateFanOutItems recomputes each item's digest and confirms items are in
// strict source-index order. It guards against a manifest whose item bytes
// were altered (by hand, or by disk corruption) without updating the stored
// digest, and against an item list that was reordered or is missing an index.
func ValidateFanOutItems(m FanOutManifest) error {
	for i, it := range m.Items {
		if it.Index != i {
			return fmt.Errorf("datastore: fanout manifest %s generation %d iteration %d item %d has out-of-order index %d",
				m.FamilyID, m.Generation, m.Iteration, i, it.Index)
		}
		if want := ItemDigest(it.Item); it.ItemSHA256 != want {
			return fmt.Errorf("datastore: fanout manifest %s generation %d iteration %d item %d digest mismatch",
				m.FamilyID, m.Generation, m.Iteration, i)
		}
	}
	return nil
}
