package engine

// Runtime fan-out expansion and barrier settlement for [step.foreach] families
// (docs/plans/a8-dynamic-foreach-fan-out.md, Phase 2). A family is a declared
// static step in wf.Steps; its runtime children never join wf.Steps — they
// live only in the scheduler-owned registries below (fanOutFamilies,
// fanOutChildren, fanOutItemByChild), keyed by their stable instance id. This
// keeps the static DAG pure while letting every ordinary lifecycle path
// (dispatch, retry, recovery, worktree integration, snapshot) operate on a
// child exactly as it would on any statically declared step.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"jig/internal/datastore"
	"jig/internal/step"
	"jig/internal/workflow"
)

// fanOutFamily is the scheduler's runtime record of one foreach family's
// current expansion: its ordered children and the family-local concurrency
// cap layered on top of every other existing limit.
type fanOutFamily struct {
	FamilyID    string
	Generation  int
	Iteration   int
	MaxParallel int      // 0 = inherit only global/resource/read-mutate limits
	Order       []string // child instance ids, strict source order
	InFlight    int      // children of this family currently dispatched
	Settled     bool     // barrier already resolved; guards double-transition
}

// fanOutAggregate is the family's ordered aggregate output — see "Aggregate
// result contract" in the plan. Field order matches the documented JSON shape
// exactly (json.Marshal on a struct preserves declaration order).
type fanOutAggregate struct {
	Count        int                 `json:"count"`
	Succeeded    int                 `json:"succeeded"`
	Failed       int                 `json:"failed"`
	AllSucceeded bool                `json:"all_succeeded"`
	Results      []fanOutResultEntry `json:"results"`
}

type fanOutResultEntry struct {
	Index      int    `json:"index"`
	InstanceID string `json:"instance_id"`
	Item       any    `json:"item"`
	Status     string `json:"status"`
	Verdict    string `json:"verdict"`
	Output     any    `json:"output"`
	OutputPath string `json:"output_path"`
	Error      string `json:"error"`
}

// parseForEachRef splits a validated "@step.field" foreach items reference
// into its step id and field name. Validation (internal/workflow) already
// guarantees this shape — exactly one "@" prefix and exactly one field
// segment — so this never needs to report an error.
func parseForEachRef(items string) (stepID, field string) {
	ref := items
	if len(ref) > 0 && ref[0] == '@' {
		ref = ref[1:]
	}
	for i := 0; i < len(ref); i++ {
		if ref[i] == '.' {
			return ref[:i], ref[i+1:]
		}
	}
	return "", ""
}

// cloneForEachTemplate builds one runtime child from a foreach family's
// template: the declared step id becomes the family/template identity
// (FanOutItem.TemplateID), and the clone gets the deterministic instance id.
// ForEach is cleared so the child cannot recursively expand; When is cleared
// because the family's guard already ran once, before expansion. Every other
// field — deps, inputs, executor config, schema, validation, retry, security,
// isolation — is preserved by the shallow copy. Children are never mutated
// after creation, so sharing the template's slice/pointer fields is safe.
func cloneForEachTemplate(tmpl *workflow.Step, instanceID string) *workflow.Step {
	out := *tmpl
	out.ID = instanceID
	out.ForEach = nil
	out.When = ""
	return &out
}

// resolveForEachItems resolves a family's [step.foreach] items reference
// against its producer's already-succeeded structured output, decoding the
// list as []json.RawMessage and canonicalizing each element (unmarshal +
// remarshal, which produces compact, key-sorted bytes) so identity, digests,
// and rendered agent/command values are stable regardless of the producer's
// original formatting. Returns the canonical items plus the raw (pre-item-
// canonicalization) field bytes, used only for the manifest's SourceDigest.
func (s *scheduler) resolveForEachItems(fe *workflow.ForEach) (items []json.RawMessage, rawField json.RawMessage, err error) {
	stepID, field := parseForEachRef(fe.Items)
	depState := s.states[stepID]
	if depState == nil || depState.Result == nil || len(depState.Result.Structured) == 0 {
		return nil, nil, fmt.Errorf("foreach items %q: producer %q has no structured output", fe.Items, stepID)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(depState.Result.Structured, &top); err != nil {
		return nil, nil, fmt.Errorf("foreach items %q: decode producer output: %w", fe.Items, err)
	}
	raw, ok := top[field]
	if !ok {
		return nil, nil, fmt.Errorf("foreach items %q: field %q missing from producer output", fe.Items, field)
	}
	var rawItems []json.RawMessage
	if err := json.Unmarshal(raw, &rawItems); err != nil {
		return nil, nil, fmt.Errorf("foreach items %q: field %q is not a JSON array: %w", fe.Items, field, err)
	}
	canon := make([]json.RawMessage, len(rawItems))
	for i, it := range rawItems {
		var v any
		if err := json.Unmarshal(it, &v); err != nil {
			return nil, nil, fmt.Errorf("foreach items %q: item %d: %w", fe.Items, i, err)
		}
		b, err := json.Marshal(v)
		if err != nil {
			return nil, nil, fmt.Errorf("foreach items %q: item %d: canonicalize: %w", fe.Items, i, err)
		}
		canon[i] = json.RawMessage(b)
	}
	return canon, raw, nil
}

// dispatchForEach is the ForEach family's dispatch strategy (see dispatch()):
// instead of running an agent/command worker for the family step itself, it
// resolves and bounds the producer's list, durably records the expansion, and
// registers runtime children for the ordinary scheduler loop to dispatch.
// The family consumes zero inFlight capacity — it is a pure barrier.
func (s *scheduler) dispatchForEach(_ context.Context, st *workflow.Step) {
	familyID := st.ID
	state := s.states[familyID]
	from := state.Status

	items, rawField, err := s.resolveForEachItems(st.ForEach)
	if err != nil {
		s.failForEach(familyID, st, err)
		return
	}
	if len(items) > st.ForEach.MaxItems {
		s.failForEach(familyID, st, fmt.Errorf("foreach %q: producer returned %d items, exceeds max_items %d — no children were created", familyID, len(items), st.ForEach.MaxItems))
		return
	}

	generation, iteration := state.Generation, state.Iteration
	manifestItems := make([]datastore.FanOutItem, len(items))
	instanceIDs := make([]string, len(items))
	for i, it := range items {
		instanceID := fmt.Sprintf("%s%sg%03d.r%03d.i%04d", familyID, workflow.ForEachIDMarker, generation, iteration, i)
		instanceIDs[i] = instanceID
		manifestItems[i] = datastore.FanOutItem{
			InstanceID: instanceID,
			Index:      i,
			Item:       it,
			ItemSHA256: datastore.ItemDigest(it),
		}
	}

	m := datastore.FanOutManifest{
		SchemaVersion: datastore.FanOutManifestVersion,
		FamilyID:      familyID,
		Generation:    generation,
		Iteration:     iteration,
		SourceRef:     st.ForEach.Items,
		SourceDigest:  datastore.ItemDigest(rawField),
		Items:         manifestItems,
	}
	if err := datastore.WriteFanOutManifest(s.runDir, m); err != nil {
		s.failForEach(familyID, st, fmt.Errorf("write fanout manifest: %w", err))
		return
	}
	digest, err := datastore.FanOutManifestDigest(m)
	if err != nil {
		s.failForEach(familyID, st, fmt.Errorf("digest fanout manifest: %w", err))
		return
	}
	descriptors := make([]FanOutInstanceDescriptor, len(manifestItems))
	for i, it := range manifestItems {
		descriptors[i] = FanOutInstanceDescriptor{InstanceID: it.InstanceID, Index: it.Index, ItemSHA256: it.ItemSHA256}
	}
	if err := s.emit(FanOutExpanded{
		SchemaVersion:  FanOutExpandedVersion,
		RunID:          s.runID,
		FamilyID:       familyID,
		Generation:     generation,
		Iteration:      iteration,
		ManifestDigest: digest,
		Instances:      descriptors,
	}); err != nil {
		return // fatalJournalErr already set by emit/failJournal; run halts.
	}

	fam := &fanOutFamily{
		FamilyID:    familyID,
		Generation:  generation,
		Iteration:   iteration,
		MaxParallel: st.ForEach.MaxParallel,
	}
	for i, instanceID := range instanceIDs {
		child := cloneForEachTemplate(st, instanceID)
		s.states[instanceID] = &step.State{
			ID:          instanceID,
			Status:      step.StatusPending,
			ParentID:    familyID,
			FanOutIndex: i,
			FanOutTotal: len(items),
		}
		s.fanOutChildren[instanceID] = child
		s.fanOutItemByChild[instanceID] = FanOutItem{
			TemplateID: familyID,
			InstanceID: instanceID,
			Index:      i,
			Total:      len(items),
			As:         st.ForEach.As,
			Item:       items[i],
			Digest:     manifestItems[i].ItemSHA256,
		}
		fam.Order = append(fam.Order, instanceID)
	}
	s.fanOutFamilies[familyID] = fam
	s.registerFanOutFamilyOrder(familyID)

	if !s.transition(familyID, from, step.StatusRunning) {
		return
	}
	// Zero items (or every child already somehow terminal, which cannot
	// happen on first expansion but is harmless to check) settles immediately
	// — an empty fan-out is a successful, empty barrier.
	s.trySettleFamily(familyID)
}

// registerFanOutFamilyOrder appends familyID to fanOutFamilyOrder exactly
// once. A family can expand more than once in a run's lifetime — a bounded
// route rewind or an operator reset invalidates the current expansion and
// dispatchForEach runs again with a new generation/iteration (see "Reset,
// routes, worktrees, and budgets") — and RunSnapshot folds children in by
// walking fanOutFamilyOrder once per entry, so a duplicate entry here would
// double-count that family's (freshly overwritten, single) child set.
func (s *scheduler) registerFanOutFamilyOrder(familyID string) {
	for _, id := range s.fanOutFamilyOrder {
		if id == familyID {
			return
		}
	}
	s.fanOutFamilyOrder = append(s.fanOutFamilyOrder, familyID)
}

// failForEach records an expansion failure and routes it through the family
// template's ordinary on_failure policy — retry, continue, or the human
// recovery gate — exactly like any other step-level failure. No child is ever
// created on this path (fail closed before dispatch, per the plan's bounds
// contract).
func (s *scheduler) failForEach(familyID string, st *workflow.Step, err error) {
	s.states[familyID].Result = &step.Result{Status: step.StatusFailed, Err: err.Error()}
	s.applyFailurePolicy(familyID, st)
}

// nextReadyChild scans one family's children in strict source order and
// returns the first one eligible to dispatch, applying the same
// budget/backoff/resource checks as an ordinary step plus the family-local
// max_parallel cap. Returning (nil, false) means "nothing ready in this
// family right now" — callers continue scanning the rest of the static graph,
// they do not stop dispatching altogether.
func (s *scheduler) nextReadyChild(familyID string) (*workflow.Step, bool) {
	fam := s.fanOutFamilies[familyID]
	if fam == nil {
		return nil, false
	}
	for _, childID := range fam.Order {
		state := s.states[childID]
		if state == nil || state.Status != step.StatusPending {
			continue
		}
		if blocked, reason := s.budgetExhausted(); blocked {
			state.Result = &step.Result{Status: step.StatusFailed, Err: reason}
			s.transition(childID, step.StatusPending, step.StatusFailed)
			continue
		}
		if until := s.retryNotBefore[childID]; !until.IsZero() && time.Now().Before(until) {
			continue
		}
		if fam.MaxParallel > 0 && fam.InFlight >= fam.MaxParallel {
			return nil, false
		}
		child := s.fanOutChildren[childID]
		if child == nil {
			continue
		}
		if !s.resourceAvailable(child) {
			continue
		}
		return child, true
	}
	return nil, false
}

// trySettleFamily checks whether every child of familyID has reached a
// terminal status (succeeded/failed/skipped — never running, retrying, or
// parked on a human gate) and, if so, builds and records the ordered
// aggregate and transitions the family from running to succeeded exactly
// once. This is the single barrier-settlement choke point: it is invoked
// from transitionRecovery after every child transition that could complete
// the family (see transitionRecovery in engine.go), so every lifecycle path
// — normal success, on_failure="continue", operator recovery-skip, a
// resumed/recovered child, validation, and integration completion — settles
// the family without any of those call sites needing to know about fan-out.
func (s *scheduler) trySettleFamily(familyID string) {
	fam := s.fanOutFamilies[familyID]
	if fam == nil || fam.Settled {
		return
	}
	famState := s.states[familyID]
	if famState == nil || famState.Status != step.StatusRunning {
		return
	}

	results := make([]fanOutResultEntry, len(fam.Order))
	succeeded, failed := 0, 0
	for i, childID := range fam.Order {
		cst := s.states[childID]
		if cst == nil {
			return
		}
		switch cst.Status {
		case step.StatusSucceeded, step.StatusFailed, step.StatusSkipped:
		default:
			return // at least one child is still in flight or parked — wait.
		}

		entry := fanOutResultEntry{Index: i, InstanceID: childID}
		if cst.Result != nil {
			entry.Verdict = cst.Result.Verdict
			entry.OutputPath = cst.Result.OutputPath
			entry.Error = cst.Result.Err
			if len(cst.Result.Structured) > 0 {
				var out any
				_ = json.Unmarshal(cst.Result.Structured, &out)
				entry.Output = out
			}
		}
		switch cst.Status {
		case step.StatusSucceeded:
			entry.Status = "succeeded"
			succeeded++
		case step.StatusFailed:
			entry.Status = "failed"
			failed++
		case step.StatusSkipped:
			entry.Status = "skipped"
		}
		if item, ok := s.fanOutItemByChild[childID]; ok {
			var iv any
			_ = json.Unmarshal(item.Item, &iv)
			entry.Item = iv
		}
		results[i] = entry
	}

	fam.Settled = true
	aggregate := fanOutAggregate{
		Count:        len(results),
		Succeeded:    succeeded,
		Failed:       failed,
		AllSucceeded: failed == 0,
		Results:      results,
	}
	data, err := json.Marshal(aggregate)
	if err != nil {
		famState.Result = &step.Result{Status: step.StatusFailed, Err: fmt.Sprintf("foreach %q: encode aggregate: %v", familyID, err)}
		s.applyFailurePolicy(familyID, s.stepByID(familyID))
		return
	}

	outputPath := ""
	if s.runDir != "" {
		if dir, err := datastore.StepDir(s.runDir, familyID); err == nil {
			outputPath = filepath.Join(dir, "output.json")
			if werr := os.WriteFile(outputPath, data, 0o644); werr != nil {
				famState.Result = &step.Result{Status: step.StatusFailed, Err: fmt.Sprintf("foreach %q: write aggregate: %v", familyID, werr)}
				s.applyFailurePolicy(familyID, s.stepByID(familyID))
				return
			}
		}
	}

	famState.Result = &step.Result{Status: step.StatusSucceeded, Structured: data, OutputPath: outputPath}
	s.transition(familyID, step.StatusRunning, step.StatusSucceeded)
}

// trySettleFamilyIfChild is the hook transitionRecovery calls after every
// terminal transition: a no-op for every ordinary step (ParentID == "") and
// for the family's own terminal transition (settling itself would be
// meaningless — trySettleFamily already guards on Status == Running).
func (s *scheduler) trySettleFamilyIfChild(stepID string) {
	state := s.states[stepID]
	if state == nil || state.ParentID == "" {
		return
	}
	s.trySettleFamily(state.ParentID)
}
