package engine

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
)

// Envelope wraps a single Event for the JSONL journal. Each line in
// journal.jsonl is one marshaled Envelope.
//
// Example line:
//
//	{"seq":14,"ts":"2026-07-30T10:04:11Z","kind":"step_status","data":{...}}
//
// Seq is monotonically increasing within a run. Ts is UTC, second precision.
// Kind is the stable string name for the event type; Data is the event itself.
type Envelope struct {
	Seq  int             `json:"seq"`
	Ts   time.Time       `json:"ts"`
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// journalEvents registers every journaled event with its on-disk kind. It is
// the single source for both directions: eventKind (encode) and decoders
// (decode) are built from it, so they cannot disagree. Kind strings are a
// persisted format; journal_kinds_test.go pins them.
var journalEvents = []eventEntry{
	entry[RunStarted]("run_started"),
	entry[RunFinished](datastore.JournalKindRunFinished),
	entry[StepStatus]("step_status"),
	entry[StepOutput]("step_output"),
	entry[StepToolCall]("step_tool_call"),
	entry[StepMessage]("step_message"),
	entry[GateResult]("gate_result"),
	entry[RouteSelected]("route_selected"),
	entry[RouteCapExceeded]("route_cap_exceeded"),
	entry[StepsReset]("steps_reset"),
	entry[ReviewRequest]("review_request"),
	entry[ReviewSubmitted]("review_submitted"),
	entry[InputRequest]("input_request"),
	entry[RunError]("run_error"),
	entry[RecoveryRequest]("recovery_request"),
	entry[IntegrationConflictRequest]("integration_conflict_request"),
	entry[FinalMergeRequest]("final_merge_request"),
	entry[PromptRequest]("prompt_request"),
	entry[AgentQuestion]("agent_question"),
	entry[AgentQuestionResolved]("agent_question_resolved"),
	entry[SecurityFinding]("security_finding"),
	{kind: "fan_out_expanded", typ: reflect.TypeFor[FanOutExpanded](), decode: decodeFanOutExpanded},
}

type eventEntry struct {
	kind   string
	typ    reflect.Type
	decode func([]byte) (Event, error)
}

// entry registers T under kind with a plain JSON decoder. Decoding into the
// concrete type avoids the json.Unmarshal-into-interface pitfall.
func entry[T Event](kind string) eventEntry {
	return eventEntry{kind: kind, typ: reflect.TypeFor[T](), decode: func(b []byte) (Event, error) {
		var e T
		if err := json.Unmarshal(b, &e); err != nil {
			return nil, err
		}
		return e, nil
	}}
}

// decodeFanOutExpanded validates schema_version at decode time (unlike every
// other kind, which has no version field): it is the authoritative creation
// record for runtime children, so a journal from a newer, incompatible jig
// must fail closed on Resume rather than misread the instance list.
func decodeFanOutExpanded(b []byte) (Event, error) {
	var e FanOutExpanded
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, err
	}
	if e.SchemaVersion != FanOutExpandedVersion {
		return nil, fmt.Errorf("fan_out_expanded: unsupported schema_version %d (want %d)", e.SchemaVersion, FanOutExpandedVersion)
	}
	return e, nil
}

var (
	kindByType = make(map[reflect.Type]string, len(journalEvents))
	decoders   = make(map[string]func([]byte) (Event, error), len(journalEvents))
)

func init() {
	for _, ev := range journalEvents {
		if _, dup := decoders[ev.kind]; dup {
			panic("engine: duplicate journal kind " + ev.kind)
		}
		if _, dup := kindByType[ev.typ]; dup {
			panic("engine: event type registered twice: " + ev.typ.Name())
		}
		kindByType[ev.typ] = ev.kind
		decoders[ev.kind] = ev.decode
	}
}

// eventKind returns the stable journal kind string for an event, or
// "unknown" for a type missing from journalEvents.
func eventKind(e Event) string {
	if kind, ok := kindByType[reflect.TypeOf(e)]; ok {
		return kind
	}
	return "unknown"
}

// MarshalEnvelope encodes an Event into a JSONL-ready Envelope byte slice.
func MarshalEnvelope(seq int, e Event) ([]byte, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("marshal event data: %w", err)
	}
	env := Envelope{
		Seq:  seq,
		Ts:   time.Now().UTC().Truncate(time.Second),
		Kind: eventKind(e),
		Data: data,
	}
	return json.Marshal(env)
}

// UnmarshalEnvelope decodes one JSONL line into its Envelope and Event.
// An unknown Kind returns (env, nil, nil) — the caller (ReplayJournal) skips
// nil events rather than aborting, which preserves forward-compatibility: a
// journal written by a newer jig version degrades gracefully in an older one.
func UnmarshalEnvelope(line []byte) (Envelope, Event, error) {
	var env Envelope
	if err := json.Unmarshal(line, &env); err != nil {
		return env, nil, fmt.Errorf("unmarshal envelope: %w", err)
	}
	decode, ok := decoders[env.Kind]
	if !ok {
		return env, nil, nil // unknown kind: skip, not an error
	}
	e, err := decode(env.Data)
	if err != nil {
		return env, nil, fmt.Errorf("decode %q event: %w", env.Kind, err)
	}
	return env, e, nil
}
