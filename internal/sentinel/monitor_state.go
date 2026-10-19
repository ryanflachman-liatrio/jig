package sentinel

import (
	"encoding/json"
	"fmt"
	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"os"
)

const monitorStateVersion = 1

type monitorState struct {
	Version  int     `json:"version"`
	SpentUSD float64 `json:"spent_usd"`
	Degraded bool    `json:"degraded"`
	InFlight bool    `json:"in_flight"`
}

func newMonitorState() monitorState { return monitorState{Version: monitorStateVersion} }

func readMonitorState(path string) (monitorState, error) {
	if path == "" {
		return newMonitorState(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return monitorState{}, err
	}
	var state monitorState
	if err := json.Unmarshal(data, &state); err != nil {
		return monitorState{}, fmt.Errorf("decode monitor state: %w", err)
	}
	if state.Version != monitorStateVersion || state.SpentUSD < 0 {
		return monitorState{}, fmt.Errorf("unsupported or invalid monitor state")
	}
	return state, nil
}

func writeMonitorState(path string, state monitorState) error {
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode monitor state: %w", err)
	}
	return datastore.WriteFileAtomic(path, data, 0o600)
}
