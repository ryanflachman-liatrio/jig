package sentinel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	tmp, err := os.CreateTemp(filepath.Dir(path), ".security-monitor-state-*")
	if err != nil {
		return fmt.Errorf("create monitor state temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write monitor state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync monitor state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close monitor state: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace monitor state: %w", err)
	}
	return nil
}
