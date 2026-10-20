package runexport

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/step"
)

func TestD05ContainerOwnershipNotExported(t *testing.T) {
	const private = "PRIVATE-CONTAINER-IDENTITY"
	event := engine.ContainerLifecycle{Ownership: step.ContainerPhase{RunID: private, StepID: private, Nonce: private, Name: private, DaemonID: private, EndpointID: private, ImageID: private, ContainerID: private, State: step.ContainerCreated}}
	record := projectEvent(decodedRecord{env: engine.Envelope{Seq: 1, Kind: "container_lifecycle"}, event: event}, nil, &timeAnchor{})
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), private) || record.Data != nil {
		t.Fatalf("ownership leaked in projection: %s", data)
	}
}
