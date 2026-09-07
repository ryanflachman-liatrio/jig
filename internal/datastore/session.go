package datastore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SessionInfo is the crash-durable mid-flight agent session identity written
// as soon as the runner observes a non-empty EventSessionID. It lives beside
// transcript.jsonl so result.json can stay terminal-only.
type SessionInfo struct {
	SessionID  string `json:"session_id"`
	Backend    string `json:"backend,omitempty"`
	Transport  string `json:"transport,omitempty"`
	Attempt    int    `json:"attempt"`
	Iteration  int    `json:"iteration"`
	Generation int    `json:"generation"`
	UpdatedAt  string `json:"updated_at"`
}

// SessionPath returns the path to session.json for a step inside runDir.
// An empty runDir (persistence off) yields "" so callers can no-op.
func SessionPath(runDir, stepID string) string {
	if runDir == "" || stepID == "" {
		return ""
	}
	return filepath.Join(runDir, "steps", stepID, "session.json")
}

// WriteSession atomically persists mid-flight session identity. Empty runDir
// is a first-class no-op (persistence off). Empty SessionID is rejected so a
// blank file can never advertise a resumable session.
func WriteSession(runDir, stepID string, info SessionInfo) error {
	path := SessionPath(runDir, stepID)
	if path == "" {
		return nil
	}
	if info.SessionID == "" {
		return fmt.Errorf("datastore: session_id is empty")
	}
	if _, err := StepDir(runDir, stepID); err != nil {
		return err
	}
	if info.UpdatedAt == "" {
		info.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("datastore: encode session: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("datastore: write session temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("datastore: rename session: %w", err)
	}
	return nil
}

// ReadSession loads session.json. Missing or corrupt files yield a zero
// SessionInfo and a nil error so crash reopen can degrade to fresh retry.
// Empty runDir is a no-op success.
func ReadSession(runDir, stepID string) (SessionInfo, error) {
	path := SessionPath(runDir, stepID)
	if path == "" {
		return SessionInfo{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return SessionInfo{}, nil
		}
		return SessionInfo{}, fmt.Errorf("datastore: read session: %w", err)
	}
	var info SessionInfo
	if err := json.Unmarshal(data, &info); err != nil || info.SessionID == "" {
		// Corrupt or empty: treat as absent so CanResume stays false.
		return SessionInfo{}, nil
	}
	return info, nil
}

// ClearSession removes session.json so a fresh attempt cannot resume a stale
// backend conversation. Persistence-off and missing files are no-ops.
func ClearSession(runDir, stepID string) error {
	path := SessionPath(runDir, stepID)
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("datastore: clear session: %w", err)
	}
	return nil
}
