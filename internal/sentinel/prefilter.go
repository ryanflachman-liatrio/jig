package sentinel

import (
	"encoding/json"
	"strings"

	"jig/internal/transcript"
)

// StuckLoopPrefilter returns true when the transcript window shows clear signs
// of a stuck loop without needing LLM confirmation:
//   - The same tool name + normalized input appears ≥ 3 times in the window, OR
//   - The consecutive IsError count across tool_result blocks is ≥ 3.
//
// The supervisor passes this signal to the stuck-loop monitor agent as context;
// the monitor emits flagged=false cheaply when the prefilter is false.
func StuckLoopPrefilter(entries []transcript.Entry) bool {
	if consecutiveErrors(entries) >= 3 {
		return true
	}
	return repeatedToolCall(entries) >= 3
}

// ExfilPrefilter returns true when the window contains a secret-shaped string
// in a tool_result block followed by a tool_use block for WebFetch or a
// curl/wget Bash command. This pattern (read secret → outbound call) is the
// defining signal for an exfiltration attempt.
func ExfilPrefilter(entries []transcript.Entry) bool {
	var sawSecret bool
	for _, e := range entries {
		for _, b := range e.Blocks {
			switch b.Type {
			case transcript.BlockToolResult:
				if containsSecret(b.Content) {
					sawSecret = true
				}
			case transcript.BlockToolUse:
				if !sawSecret {
					continue
				}
				if isOutboundCall(b.Name, b.Input) {
					return true
				}
			}
		}
	}
	return false
}

// consecutiveErrors counts the longest run of consecutive IsError=true
// tool_result blocks across all entries.
func consecutiveErrors(entries []transcript.Entry) int {
	var run, max int
	for _, e := range entries {
		for _, b := range e.Blocks {
			if b.Type != transcript.BlockToolResult {
				continue
			}
			if b.IsError {
				run++
				if run > max {
					max = run
				}
			} else {
				run = 0
			}
		}
	}
	return max
}

// repeatedToolCall counts the maximum occurrence of any single (toolName,
// normalizedInput) pair across all tool_use blocks in the window.
func repeatedToolCall(entries []transcript.Entry) int {
	counts := make(map[string]int)
	var max int
	for _, e := range entries {
		for _, b := range e.Blocks {
			if b.Type != transcript.BlockToolUse {
				continue
			}
			key := b.Name + "\x00" + normalizeInput(b.Input)
			counts[key]++
			if counts[key] > max {
				max = counts[key]
			}
		}
	}
	return max
}

// normalizeInput produces a canonical string from a tool_use input for
// stuck-loop comparison. It JSON-encodes the decoded map so key order is
// stable, falling back to the raw bytes on parse failure.
func normalizeInput(raw json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return string(raw)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return string(raw)
	}
	return string(b)
}

// containsSecret returns true when text matches any known secret pattern or
// contains a high-entropy token. Reuses the guard's detectors.
func containsSecret(text string) bool {
	for _, pat := range secretPatterns {
		if pat.re.MatchString(text) {
			return true
		}
	}
	for _, tok := range strings.FieldsFunc(text, isTokenSep) {
		if len(tok) >= minSecretLen && shannonEntropy(tok) >= entropyThreshold {
			return true
		}
	}
	return false
}

// isOutboundCall returns true when the tool is WebFetch or a Bash command
// containing curl or wget.
func isOutboundCall(toolName string, input json.RawMessage) bool {
	switch toolName {
	case "WebFetch":
		return true
	case "Bash":
		var m map[string]any
		if err := json.Unmarshal(input, &m); err != nil {
			return false
		}
		cmd, _ := m["command"].(string)
		return strings.Contains(cmd, "curl ") || strings.Contains(cmd, "wget ")
	}
	return false
}
