package acp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const maxCursorFrame = 10 << 20

// cursorWireReader adapts Cursor's bare extension method names to the ACP
// SDK's underscore-prefixed extension namespace. It operates a frame at a
// time, leaving JSON-RPC IDs and payloads untouched.
type cursorWireReader struct {
	r *bufio.Reader
	b []byte
}

func newCursorWireReader(r io.Reader) io.Reader { return &cursorWireReader{r: bufio.NewReader(r)} }

func (r *cursorWireReader) Read(p []byte) (int, error) {
	for len(r.b) == 0 {
		line, err := r.readFrame()
		if len(line) > 0 {
			out, rewriteErr := rewriteCursorFrame(line)
			if rewriteErr != nil {
				return 0, rewriteErr
			}
			r.b = out
		}
		if err != nil {
			if len(r.b) != 0 {
				break
			}
			return 0, err
		}
	}
	n := copy(p, r.b)
	r.b = r.b[n:]
	return n, nil
}

func (r *cursorWireReader) readFrame() ([]byte, error) {
	line := make([]byte, 0, 4096)
	for {
		b, err := r.r.ReadByte()
		if err != nil {
			if len(line) != 0 && err == io.EOF {
				return line, err
			}
			return line, err
		}
		if len(line) == maxCursorFrame {
			return nil, fmt.Errorf("cursor ACP frame exceeds %d bytes", maxCursorFrame)
		}
		line = append(line, b)
		if b == '\n' {
			return line, nil
		}
	}
}

func rewriteCursorFrame(line []byte) ([]byte, error) {
	body := bytes.TrimSpace(line)
	if len(body) == 0 {
		return line, nil
	}
	var msg struct {
		Method string           `json:"method"`
		ID     *json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, fmt.Errorf("cursor ACP malformed frame: %w", err)
	}
	if msg.ID == nil || (msg.Method != "cursor/ask_question" && msg.Method != "cursor/create_plan") {
		return line, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("cursor ACP malformed frame: %w", err)
	}
	raw["method"], _ = json.Marshal("_" + msg.Method)
	out, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
