package engine

import (
	"encoding/json"
	"fmt"
	"time"
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

// eventKind returns the stable journal kind string for an event.
func eventKind(e Event) string {
	switch e.(type) {
	case RunStarted:
		return "run_started"
	case RunFinished:
		return "run_finished"
	case StepStatus:
		return "step_status"
	case StepOutput:
		return "step_output"
	case StepToolCall:
		return "step_tool_call"
	case StepMessage:
		return "step_message"
	case GateResult:
		return "gate_result"
	case LoopFired:
		return "loop_fired"
	case ReviewRequest:
		return "review_request"
	case RunError:
		return "run_error"
	case RecoveryRequest:
		return "recovery_request"
	case IntegrationConflictRequest:
		return "integration_conflict_request"
	default:
		return "unknown"
	}
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

// decoders maps kind strings to functions that decode the event's Data field.
// Using concrete-typed decoders avoids the json.Unmarshal-into-interface pitfall.
var decoders = map[string]func([]byte) (Event, error){
	"run_started": func(b []byte) (Event, error) {
		var e RunStarted
		return e, json.Unmarshal(b, &e)
	},
	"run_finished": func(b []byte) (Event, error) {
		var e RunFinished
		return e, json.Unmarshal(b, &e)
	},
	"step_status": func(b []byte) (Event, error) {
		var e StepStatus
		return e, json.Unmarshal(b, &e)
	},
	"step_output": func(b []byte) (Event, error) {
		var e StepOutput
		return e, json.Unmarshal(b, &e)
	},
	"step_tool_call": func(b []byte) (Event, error) {
		var e StepToolCall
		return e, json.Unmarshal(b, &e)
	},
	"step_message": func(b []byte) (Event, error) {
		var e StepMessage
		return e, json.Unmarshal(b, &e)
	},
	"gate_result": func(b []byte) (Event, error) {
		var e GateResult
		return e, json.Unmarshal(b, &e)
	},
	"loop_fired": func(b []byte) (Event, error) {
		var e LoopFired
		return e, json.Unmarshal(b, &e)
	},
	"review_request": func(b []byte) (Event, error) {
		var e ReviewRequest
		return e, json.Unmarshal(b, &e)
	},
	"run_error": func(b []byte) (Event, error) {
		var e RunError
		return e, json.Unmarshal(b, &e)
	},
	"recovery_request": func(b []byte) (Event, error) {
		var e RecoveryRequest
		return e, json.Unmarshal(b, &e)
	},
	"integration_conflict_request": func(b []byte) (Event, error) {
		var e IntegrationConflictRequest
		return e, json.Unmarshal(b, &e)
	},
}

// UnmarshalEnvelope decodes one JSONL line into its Envelope and Event.
func UnmarshalEnvelope(line []byte) (Envelope, Event, error) {
	var env Envelope
	if err := json.Unmarshal(line, &env); err != nil {
		return env, nil, fmt.Errorf("unmarshal envelope: %w", err)
	}
	decode, ok := decoders[env.Kind]
	if !ok {
		return env, nil, fmt.Errorf("unknown event kind %q", env.Kind)
	}
	e, err := decode(env.Data)
	if err != nil {
		return env, nil, fmt.Errorf("decode %q event: %w", env.Kind, err)
	}
	return env, e, nil
}
