package engine

import (
	"reflect"
	"sort"
	"testing"
	"unsafe"

	"github.com/ryanflachman-liatrio/jig/internal/step"
)

// retainedOnReset lists the step-keyed scheduler maps that deliberately keep a
// reset step's entry. Every other step-keyed map must drop it, so stale
// execution state cannot leak into the re-run. A new map must be added here
// (with a reason) or cleared in handleReset.
//
// Entries marked SUSPECT record current behavior that looks like stale state
// surviving a reset; they are resolved when per-step state moves into one
// struct (docs/plans/engine-quality-refactor.md, 3.1), not here.
var retainedOnReset = map[string]string{
	"states":           "the step's State is reset in place, not removed",
	"stickyUserInputs": "once=true answers are retained across rewinds by design",
	"stepCancels":      "removed when the worker's stepDoneMsg is handled; reset requires no in-flight work",
	"executionViews":   "released when the read-only dispatch finishes; reset requires no in-flight work",

	"skippedByGuard":            "SUSPECT: a reset step stays transparent to depsReady until it re-runs",
	"skippedByOperator":         "SUSPECT: a reset step stays transparent to depsReady until it re-runs",
	"structured":                "SUSPECT: the cached decode of the pre-reset result outlives it",
	"retryNotBefore":            "SUSPECT: a pre-reset retry backoff still delays the re-run",
	"failedSessionResume":       "SUSPECT: a pre-reset dead session still forces CanResume=false",
	"sessionResumeInFlight":     "SUSPECT: pre-reset resume marker outlives the dispatch it described",
	"pendingQuestions":          "SUSPECT: questions from the pre-reset execution survive",
	"restoredQuestionResponses": "SUSPECT: answers for the pre-reset execution survive",
}

// notStepKeyed lists map[string]X scheduler fields whose keys are not step ids,
// so the reset contract does not apply to them.
var notStepKeyed = map[string]string{
	"classInFlight":     "keyed by resource class",
	"seenEscalations":   "keyed by security finding fingerprint",
	"pendingLoops":      "keyed by route goto target; drained when the loop fires",
	"fanOutChildren":    "keyed by child instance id; residue is dropped from Order on reset",
	"fanOutItemByChild": "keyed by child instance id; residue is dropped from Order on reset",
}

// TestHandleResetClearsEveryStepKeyedMap fills every map[string]X field on the
// scheduler for a reset target and an unrelated survivor, resets the target,
// and checks each map dropped the target (unless listed in retainedOnReset)
// and left the survivor alone.
func TestHandleResetClearsEveryStepKeyedMap(t *testing.T) {
	s := fixtureScheduler(t, `
[workflow]
name = "reset-state"
version = "1"
[[step]]
id = "target"
type = "command"
run = "true"
[[step]]
id = "survivor"
type = "command"
run = "true"
`)
	repo := t.TempDir()
	initRepo(t, repo)
	s.repoRoot, s.runWorktree = repo, repo
	head, err := currentHEAD(repo)
	if err != nil {
		t.Fatal(err)
	}
	s.runBaseSHA = head
	s.states["target"].Status = step.StatusSucceeded
	s.states["survivor"].Status = step.StatusSucceeded

	fields := stepKeyedMaps(s)
	for name := range retainedOnReset {
		if _, ok := fields[name]; !ok {
			t.Errorf("retainedOnReset names %q, which is not a map[string]X scheduler field", name)
		}
	}
	for name := range notStepKeyed {
		if _, ok := fields[name]; !ok {
			t.Errorf("notStepKeyed names %q, which is not a map[string]X scheduler field", name)
		}
	}
	for name, m := range fields {
		if name == "states" {
			continue
		}
		for _, id := range []string{"target", "survivor"} {
			m.SetMapIndex(reflect.ValueOf(id), nonNilZero(m.Type().Elem()))
		}
	}

	reply := make(chan resetReply, 1)
	s.handleReset(resetMsg{stepID: "target", reply: reply})
	if r := <-reply; r.err != nil {
		t.Fatalf("reset: %v", r.err)
	}

	var leaked, lostSurvivor []string
	for name, m := range fields {
		if _, skip := notStepKeyed[name]; skip {
			continue
		}
		_, retained := retainedOnReset[name]
		if has := m.MapIndex(reflect.ValueOf("target")).IsValid(); has && !retained {
			leaked = append(leaked, name)
		}
		if !m.MapIndex(reflect.ValueOf("survivor")).IsValid() {
			lostSurvivor = append(lostSurvivor, name)
		}
	}
	sort.Strings(leaked)
	sort.Strings(lostSurvivor)
	if len(leaked) > 0 {
		t.Errorf("reset kept the target's entry in %v; clear it in handleReset or list it in retainedOnReset", leaked)
	}
	if len(lostSurvivor) > 0 {
		t.Errorf("reset of target removed the survivor's entry from %v", lostSurvivor)
	}
}

// stepKeyedMaps returns every map[string]X field on s, made settable and
// non-nil. Unexported fields need unsafe to be writable through reflect.
func stepKeyedMaps(s *scheduler) map[string]reflect.Value {
	v := reflect.ValueOf(s).Elem()
	out := make(map[string]reflect.Value)
	for i := 0; i < v.NumField(); i++ {
		f := v.Type().Field(i)
		if f.Type.Kind() != reflect.Map || f.Type.Key().Kind() != reflect.String {
			continue
		}
		field := reflect.NewAt(f.Type, unsafe.Pointer(v.Field(i).UnsafeAddr())).Elem()
		if field.IsNil() {
			field.Set(reflect.MakeMap(f.Type))
		}
		out[f.Name] = field
	}
	return out
}

// nonNilZero returns a zero value of t, except pointers point at a zero
// value, so code that dereferences an entry does not panic.
func nonNilZero(t reflect.Type) reflect.Value {
	if t.Kind() == reflect.Pointer {
		return reflect.New(t.Elem())
	}
	return reflect.Zero(t)
}
