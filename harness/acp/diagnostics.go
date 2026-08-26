package acp

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const diagnosticTailBytes = 8 << 10

type lifecycleEvent struct {
	Timestamp time.Time        `json:"timestamp"`
	Event     string           `json:"event"`
	Resource  ResourceSnapshot `json:"resource"`
	Fields    map[string]any   `json:"fields,omitempty"`
}

// diagnosticLog keeps machine-readable lifecycle data separate from arbitrary
// adapter stderr, which can contain content the adapter chose to print.
type diagnosticLog struct {
	mu      sync.Mutex
	events  *os.File
	stderr  *os.File
	tail    bytes.Buffer
	sampler ResourceSampler
	rootPID int
}

func newDiagnosticLog(dir string, sampler ResourceSampler) (*diagnosticLog, error) {
	if sampler == nil {
		sampler = sampleResources
	}
	d := &diagnosticLog{sampler: sampler}
	if dir == "" {
		return d, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	events, err := os.OpenFile(filepath.Join(dir, "acp-diagnostics.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	stderr, err := os.OpenFile(filepath.Join(dir, "acp-adapter.stderr.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		_ = events.Close()
		return nil, err
	}
	d.events, d.stderr = events, stderr
	return d, nil
}

func (d *diagnosticLog) setRootPID(pid int) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.rootPID = pid
	d.mu.Unlock()
}

func (d *diagnosticLog) Event(event string, fields map[string]any) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.events == nil {
		return
	}
	snapshot := d.sampler(d.rootPID)
	line, err := json.Marshal(lifecycleEvent{Timestamp: time.Now().UTC(), Event: event, Resource: snapshot, Fields: fields})
	if err == nil {
		_, _ = d.events.Write(append(line, '\n'))
	}
}

func (d *diagnosticLog) StderrWriter() io.Writer { return diagnosticStderrWriter{d: d} }

func (d *diagnosticLog) StderrTail() string {
	if d == nil {
		return ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.TrimSpace(d.tail.String())
}

func (d *diagnosticLog) Close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var err error
	if d.events != nil {
		err = d.events.Close()
		d.events = nil
	}
	if d.stderr != nil {
		if closeErr := d.stderr.Close(); err == nil {
			err = closeErr
		}
		d.stderr = nil
	}
	return err
}

type diagnosticStderrWriter struct{ d *diagnosticLog }

func (w diagnosticStderrWriter) Write(p []byte) (int, error) {
	if w.d == nil {
		return len(p), nil
	}
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	if w.d.stderr != nil {
		_, _ = w.d.stderr.Write(p)
	}
	if len(p) >= diagnosticTailBytes {
		w.d.tail.Reset()
		_, _ = w.d.tail.Write(p[len(p)-diagnosticTailBytes:])
		return len(p), nil
	}
	overflow := w.d.tail.Len() + len(p) - diagnosticTailBytes
	if overflow > 0 {
		remaining := append([]byte(nil), w.d.tail.Bytes()[overflow:]...)
		w.d.tail.Reset()
		_, _ = w.d.tail.Write(remaining)
	}
	_, _ = w.d.tail.Write(p)
	return len(p), nil
}
