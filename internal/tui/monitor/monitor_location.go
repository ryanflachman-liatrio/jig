package monitor

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/ryanflachman-liatrio/jig/internal/transcript"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

var nextLocationInstance atomic.Uint64

// OpenLocationOwner identifies a selection without retaining a mutable model.
// Group headers and first children have different kinds despite sharing anchors.
func (m Model) OpenLocationOwner() shared.OpenLocationOwner {
	owner := shared.OpenLocationOwner{Instance: m.locationInstance, RunID: m.RunID, StepID: m.chatStep}
	if item, ok := m.selectedTranscriptItem(); ok && m.selKind != "file" {
		owner.Generation, owner.Iteration, owner.Attempt = item.coord.generation, item.coord.iteration, item.coord.attempt
		owner.Seq, owner.Block = item.key.anchor.seq, item.key.anchor.block
		owner.ItemKind = int(item.kind)
	}
	return owner
}

type locationIdentity struct {
	path string
	// Unresolved relative strings are not identities across different exchanges.
	item  transcriptItemKey
	coord toolCorrelationKey
}

type locationCandidate struct {
	target      shared.OpenLocationTarget
	unavailable string
	identity    locationIdentity
	explicit    bool
}

// selectedLocationTargets never reads another page or resolves against current
// runtime state. A header visits its loaded members; a child is already narrowed
// by selectedTranscriptItem. Explicit metadata precedes weaker path fallbacks.
func (m Model) selectedLocationTargets() []locationCandidate {
	item, ok := m.selectedTranscriptItem()
	if !ok {
		return nil
	}
	return deduplicateLocations(m.itemLocationTargets(item))
}

func (m Model) itemLocationTargets(item transcriptItem) []locationCandidate {
	if item.kind == transcriptItemToolGroup {
		var out []locationCandidate
		for _, member := range item.groupMembers {
			out = append(out, m.itemLocationTargets(member)...)
		}
		return out
	}
	if item.kind != transcriptItemToolExchange && item.kind != transcriptItemToolResult {
		return nil
	}
	refs := itemMembers(item)
	var out []locationCandidate
	for strength := 0; strength < 3; strength++ {
		for _, ref := range refs {
			if ref.entryIdx < 0 || ref.entryIdx >= len(m.chatEntries) {
				continue
			}
			entry := m.chatEntries[ref.entryIdx]
			if ref.blockIdx < 0 || ref.blockIdx >= len(entry.Blocks) {
				continue
			}
			activity := entry.Blocks[ref.blockIdx].Activity()
			if activity == nil {
				continue
			}
			add := func(path string, line, column int, explicit bool) {
				candidate := resolveLocation(item, entry, m.locationOrigin(item, entry), shared.OpenLocationTarget{Path: path, Line: max(0, line), Column: max(0, column)})
				candidate.explicit = explicit
				out = append(out, candidate)
			}
			switch strength {
			case 0:
				for _, location := range activity.Locations {
					line, column := 0, 0
					if location.Line != nil {
						line = *location.Line
					}
					if location.Column != nil {
						column = *location.Column
					}
					add(location.Path, line, column, true)
				}
			case 1:
				for _, content := range activity.Content {
					if content.Diff != nil {
						add(content.Diff.Path, 0, 0, false)
					}
				}
			case 2:
				var keys []string
				switch canonicalActivityKind(activity) {
				case "read", "edit", "write":
					keys = []string{"file_path", "path"}
				case "notebookedit":
					keys = []string{"notebook_path", "target_notebook", "path"}
				default:
					continue
				}
				args := decodeToolArgs(activity.Input)
				for _, key := range keys {
					// stringArg is for display summaries and collapses whitespace. Paths
					// cross the launch boundary byte-for-byte, without that normalization.
					var path string
					if raw, ok := args[key]; ok && json.Unmarshal(raw, &path) == nil && path != "" {
						add(path, 0, 0, false)
					}
				}
			}
		}
	}
	return out
}

type executionOrigin struct{ dir, unavailable string }

func (m Model) locationOrigin(item transcriptItem, entry transcript.Entry) executionOrigin {
	origin := entry.ExecutionDir
	for _, ref := range itemMembers(item) {
		if ref.entryIdx < 0 || ref.entryIdx >= len(m.chatEntries) {
			continue
		}
		paired := m.chatEntries[ref.entryIdx]
		if paired.Generation != entry.Generation || paired.Iteration != entry.Iteration || paired.Attempt != entry.Attempt {
			continue
		}
		if paired.ExecutionDir != "" {
			if origin != "" && origin != paired.ExecutionDir {
				return executionOrigin{unavailable: "conflicting recorded execution directories"}
			}
			origin = paired.ExecutionDir
		}
	}
	if origin == "" {
		return executionOrigin{unavailable: "originating execution directory unavailable"}
	}
	if !filepath.IsAbs(origin) || !validLocationText(origin) {
		return executionOrigin{unavailable: "invalid recorded execution directory"}
	}
	return executionOrigin{dir: origin}
}

func validLocationText(path string) bool {
	return path != "" && utf8.ValidString(path) && !strings.ContainsFunc(path, unicode.IsControl)
}

func nonlocalLocation(path string) bool {
	if filepath.IsAbs(path) {
		return false
	}
	if strings.Contains(path, "://") {
		return true
	}
	// Colon-bearing local filenames remain literal, while recognized URI forms
	// without // are unavailable. No URI is ever fetched or converted to a path.
	lower := strings.ToLower(path)
	for _, prefix := range []string{"data:", "file:", "http:", "https:", "ssh:", "mailto:"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func resolveLocation(item transcriptItem, entry transcript.Entry, origin executionOrigin, target shared.OpenLocationTarget) locationCandidate {
	candidate := locationCandidate{target: target}
	switch {
	case !validLocationText(target.Path):
		candidate.unavailable = "invalid local file path"
	case nonlocalLocation(target.Path):
		candidate.unavailable = "nonlocal URI target"
	case !filepath.IsAbs(target.Path):
		if origin.unavailable != "" {
			candidate.unavailable = origin.unavailable
		} else {
			candidate.target.Path = filepath.Join(origin.dir, target.Path)
		}
	}
	candidate.identity.path = candidate.target.Path
	if !filepath.IsAbs(candidate.target.Path) {
		candidate.identity.item = item.key
		candidate.identity.coord = toolCorrelationKey{generation: entry.Generation, iteration: entry.Iteration, attempt: entry.Attempt, toolUseID: item.coord.toolUseID}
	}
	return candidate
}

func deduplicateLocations(candidates []locationCandidate) []locationCandidate {
	type position struct{ line, column int }
	type fileState struct{ positions map[position]int }
	files := make(map[locationIdentity]*fileState)
	out := make([]locationCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		pos := position{}
		if candidate.target.Line > 0 || candidate.target.Column > 0 {
			pos = position{max(1, candidate.target.Line), max(1, candidate.target.Column)}
		}
		state := files[candidate.identity]
		if state == nil {
			state = &fileState{positions: make(map[position]int)}
			files[candidate.identity] = state
		}
		if index, ok := state.positions[pos]; ok {
			if candidate.explicit && !out[index].explicit {
				out[index] = candidate
			}
			continue
		}
		if pos == (position{}) && len(state.positions) > 0 {
			continue
		}
		if index, ok := state.positions[position{}]; ok {
			// Replace weaker path-only metadata in its first display position.
			out[index] = candidate
			delete(state.positions, position{})
			state.positions[pos] = index
			continue
		}
		state.positions[pos] = len(out)
		out = append(out, candidate)
	}
	return out
}

func locationRequestCmd(owner shared.OpenLocationOwner, candidate locationCandidate) tea.Cmd {
	req := shared.OpenLocationRequest{Owner: owner, Target: candidate.target, Unavailable: candidate.unavailable}
	return func() tea.Msg { return req }
}

func (m Model) openLocation() (Model, tea.Cmd) {
	targets := m.selectedLocationTargets()
	switch len(targets) {
	case 0:
		return m, locationRequestCmd(m.OpenLocationOwner(), locationCandidate{unavailable: "no recorded file target on this selection"})
	case 1:
		return m, locationRequestCmd(m.OpenLocationOwner(), targets[0])
	default:
		return m.startLocationPicker(targets)
	}
}
