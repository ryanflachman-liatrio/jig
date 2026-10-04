package monitor

import (
	"crypto/sha256"
	"encoding/json"

	"github.com/ryanflachman-liatrio/jig/internal/toolcall"
)

// Presentation state belongs to a loaded exchange, not its path or screen row.
// Run/step and execution coordinates prevent reuse across replacement pages.
type sourceOwner struct {
	run, step string
	item      transcriptItemKey
	coord     toolCorrelationKey
}
type sourcePresentation struct {
	source  bool
	section int
	offset  int
	version [sha256.Size]byte
}

func (m Model) sourceOwner(item transcriptItem) sourceOwner {
	return sourceOwner{run: m.RunID, step: m.chatStep, item: item.key, coord: item.coord}
}

// The finalized result owns edit content when supplied. A result with no
// structured edits does not hide the recorded request's structured content.
func (m Model) sourceActivity(item transcriptItem) *toolcall.Activity {
	if item.kind != transcriptItemToolExchange && item.kind != transcriptItemToolResult {
		return nil
	}
	refs := itemMembers(item)
	for i := len(refs) - 1; i >= 0; i-- {
		ref := refs[i]
		a := m.chatEntries[ref.entryIdx].Blocks[ref.blockIdx].Activity()
		if a == nil {
			continue
		}
		for _, c := range a.Content {
			if c.Diff != nil {
				return a
			}
		}
	}
	return nil
}

func (m Model) sourceSections(item transcriptItem) []*toolcall.Diff {
	var out []*toolcall.Diff
	if a := m.sourceActivity(item); a != nil {
		for _, c := range a.Content {
			if c.Diff != nil {
				out = append(out, c.Diff)
			}
		}
	}
	return out
}

func (m Model) sourceVersion(item transcriptItem) [sha256.Size]byte {
	data, _ := json.Marshal(m.sourceSections(item))
	return sha256.Sum256(data)
}

func (m Model) itemEvidenceTruncated(item transcriptItem) bool {
	for _, ref := range itemMembers(item) {
		if m.chatEntries[ref.entryIdx].Blocks[ref.blockIdx].Truncated {
			return true
		}
	}
	return false
}

// Reconcile only against the complete loaded page, before view filters. A
// payload change discards its selection; hidden loaded group children survive.
func (m *Model) pruneSourceState() {
	m.pruneSourceDiffs()
	loaded := make(map[sourceOwner][sha256.Size]byte)
	tracked := make(map[sourceOwner]bool)
	for owner := range m.sourceStates {
		tracked[owner] = true
	}
	for key := range m.sourceWindows {
		tracked[key.owner] = true
	}
	var visit func(transcriptItem)
	visit = func(item transcriptItem) {
		if len(item.groupMembers) > 0 {
			for _, member := range item.groupMembers {
				visit(member)
			}
			return
		}
		owner := m.sourceOwner(item)
		if tracked[owner] && len(m.sourceSections(item)) > 0 {
			loaded[owner] = m.sourceVersion(item)
		}
	}
	for _, item := range m.chatItems {
		visit(item)
	}
	for owner, state := range m.sourceStates {
		if version, ok := loaded[owner]; !ok || version != state.version {
			delete(m.sourceStates, owner)
		}
	}
	for key, cache := range m.sourceWindows {
		if version, ok := loaded[key.owner]; !ok || version != cache.version {
			delete(m.sourceWindows, key)
		}
	}
	// A picker contains references into this page; never carry them across a
	// replacement, even if a superficially identical header remains selected.
	m.sourcePicker = nil
}
