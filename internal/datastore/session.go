package datastore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// SessionInfo is the crash-durable mid-flight agent session identity written
// as soon as the runner observes a non-empty EventSessionID. It lives beside
// transcript.jsonl so result.json can stay terminal-only.
type SessionInfo struct {
	SessionID  string `json:"session_id"`
	Backend    string `json:"backend,omitempty"`
	Attempt    int    `json:"attempt"`
	Iteration  int    `json:"iteration"`
	Generation int    `json:"generation"`
	UpdatedAt  string `json:"updated_at"`
}

// Session returns session.json, or "" when the step is disabled.
func (s Step) Session() string { return s.path("session.json") }

// WriteSession atomically persists mid-flight session identity (see
// WriteFileAtomic). A disabled
// step is a first-class no-op (persistence off). Empty SessionID is rejected
// so a blank file can never advertise a resumable session.
func (s Step) WriteSession(info SessionInfo) error {
	if s.err != nil {
		return s.err
	}
	if s.dir == "" {
		return nil
	}
	if info.SessionID == "" {
		return fmt.Errorf("datastore: session_id is empty")
	}
	if err := s.Ensure(); err != nil {
		return err
	}
	if info.UpdatedAt == "" {
		info.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("datastore: encode session: %w", err)
	}
	return WriteFileAtomic(s.Session(), data, 0o600)
}

// ErrSessionCorrupt reports a session.json that exists but cannot be used:
// undecodable, or missing its session_id (WriteSession never writes one
// without). Callers treat it like an absent session — crash reopen degrades
// to a fresh retry — but can surface it, as jig doctor does.
var ErrSessionCorrupt = errors.New("datastore: session.json is corrupt")

// ReadSession loads session.json. A missing file yields a zero SessionInfo
// and a nil error; a corrupt one yields a zero SessionInfo and an error
// wrapping ErrSessionCorrupt. A disabled step is a no-op success.
func (s Step) ReadSession() (SessionInfo, error) {
	if s.err != nil {
		return SessionInfo{}, s.err
	}
	if s.dir == "" {
		return SessionInfo{}, nil
	}
	data, err := os.ReadFile(s.Session())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return SessionInfo{}, nil
		}
		return SessionInfo{}, fmt.Errorf("datastore: read session: %w", err)
	}
	var info SessionInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return SessionInfo{}, fmt.Errorf("%w: %w", ErrSessionCorrupt, err)
	}
	if info.SessionID == "" {
		return SessionInfo{}, fmt.Errorf("%w: session_id is empty", ErrSessionCorrupt)
	}
	return info, nil
}

// ClearSession removes session.json so a fresh attempt cannot resume a stale
// backend conversation. A disabled step and a missing file are no-ops.
func (s Step) ClearSession() error {
	if s.err != nil {
		return s.err
	}
	if s.dir == "" {
		return nil
	}
	if err := os.Remove(s.Session()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("datastore: clear session: %w", err)
	}
	return nil
}
