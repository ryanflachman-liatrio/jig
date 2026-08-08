package sentinel

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"unicode"
)

// secretPatterns are the known-secret regex patterns the guard scans for.
// Each entry carries the rule name (used in Redact and Finding.Monitor) and
// the compiled pattern.
var secretPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"aws-key", regexp.MustCompile(`AKIA[A-Z0-9]{16}`)},
	{"gcp-api-key", regexp.MustCompile(`AIza[0-9A-Za-z\-_]{35}`)},
	{"github-token", regexp.MustCompile(`gh[pors]_[A-Za-z0-9_]{36,}`)},
	{"pem-private-key", regexp.MustCompile(`-----BEGIN [\w ]* PRIVATE KEY-----`)},
}

// entropyThreshold is the Shannon bits-per-char threshold above which a
// non-whitespace token is treated as a likely secret.
const entropyThreshold = 4.5

// minSecretLen is the minimum token length required for high-entropy detection.
const minSecretLen = 16

// writeTools lists the tool names whose input payloads are scanned for secrets.
var writeTools = map[string]bool{
	"Write":     true,
	"Edit":      true,
	"Bash":      true,
	"MultiEdit": true,
}

// checkSecretInWrite blocks write-type tool calls that carry known-secret
// patterns or high-entropy tokens in their payload fields.
func checkSecretInWrite(toolName string, input map[string]any) Decision {
	if !writeTools[toolName] {
		return Decision{Allow: true}
	}
	payload := writePayload(toolName, input)
	for _, pat := range secretPatterns {
		if m := pat.re.FindString(payload); m != "" {
			return Decision{
				Allow:   false,
				Action:  ActionBlocked,
				Monitor: "secret-in-write",
				Reason:  "tool input contains " + pat.name + " pattern: " + Redact(pat.name, m),
			}
		}
	}
	// High-entropy token scan: split on whitespace and quotes, test each token.
	for _, tok := range strings.FieldsFunc(payload, isTokenSep) {
		if len(tok) >= minSecretLen && shannonEntropy(tok) >= entropyThreshold {
			return Decision{
				Allow:   false,
				Action:  ActionBlocked,
				Monitor: "secret-in-write",
				Reason:  "tool input contains high-entropy string (possible secret): " + Redact("high-entropy", tok),
			}
		}
	}
	return Decision{Allow: true}
}

func isTokenSep(r rune) bool {
	return unicode.IsSpace(r) || r == '"' || r == '\'' || r == '`'
}

// writePayload extracts the text content to scan from a write-type tool call.
func writePayload(toolName string, input map[string]any) string {
	var parts []string
	switch toolName {
	case "Write":
		if v, ok := input["content"].(string); ok {
			parts = append(parts, v)
		}
	case "Edit":
		for _, k := range []string{"old_string", "new_string"} {
			if v, ok := input[k].(string); ok {
				parts = append(parts, v)
			}
		}
	case "Bash":
		if v, ok := input["command"].(string); ok {
			parts = append(parts, v)
		}
	case "MultiEdit":
		// edits is []{"old_string": ..., "new_string": ...}
		if edits, ok := input["edits"].([]any); ok {
			for _, e := range edits {
				if m, ok := e.(map[string]any); ok {
					for _, k := range []string{"old_string", "new_string"} {
						if v, ok := m[k].(string); ok {
							parts = append(parts, v)
						}
					}
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

// shannonEntropy computes the Shannon entropy in bits per character of s.
func shannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	freq := make(map[rune]int)
	for _, r := range s {
		freq[r]++
	}
	n := float64(len([]rune(s)))
	var h float64
	for _, c := range freq {
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}

// deniedShellPatterns are Bash command patterns that indicate dangerous or
// irreversible operations. Matches trigger escalate (human review required).
var deniedShellPatterns = []*regexp.Regexp{
	// rm -rf of root or near-root paths
	regexp.MustCompile(`rm\s+(?:-[a-zA-Z]*r[a-zA-Z]*f|-[a-zA-Z]*f[a-zA-Z]*r)\s+/`),
	// chmod 777 (world-writable)
	regexp.MustCompile(`chmod\s+777`),
	// curl or wget piped directly into a shell interpreter
	regexp.MustCompile(`(?:curl|wget)\s[^|]+\|\s*(?:ba)?sh`),
}

// checkDeniedShell escalates Bash commands that match known dangerous patterns.
func checkDeniedShell(toolName string, input map[string]any) Decision {
	if toolName != "Bash" {
		return Decision{Allow: true}
	}
	cmd, _ := input["command"].(string)
	for _, re := range deniedShellPatterns {
		if re.MatchString(cmd) {
			return Decision{
				Allow:   false,
				Action:  ActionEscalated,
				Monitor: "denied-shell",
				Reason:  "Bash command matches denied shell pattern; escalated for human review",
			}
		}
	}
	return Decision{Allow: true}
}

// checkOutboundHost blocks WebFetch and Bash curl/wget calls to hosts not in
// the guard's allowlist. An empty allowlist makes this rule a no-op (allow
// all), which is the default when the workflow omits outbound_allowlist.
func (g *Guard) checkOutboundHost(toolName string, input map[string]any) Decision {
	if len(g.allowlist) == 0 {
		return Decision{Allow: true}
	}
	var hosts []string
	switch toolName {
	case "WebFetch":
		if u, ok := input["url"].(string); ok {
			if h := extractHost(u); h != "" {
				hosts = append(hosts, h)
			}
		}
	case "Bash":
		cmd, _ := input["command"].(string)
		hosts = extractCurlWgetHosts(cmd)
	}
	for _, h := range hosts {
		if !hostAllowed(h, g.allowlist) {
			return Decision{
				Allow:   false,
				Action:  ActionBlocked,
				Monitor: "non-allowlisted-host",
				Reason:  "outbound call to non-allowlisted host: " + h,
			}
		}
	}
	return Decision{Allow: true}
}

// extractHost returns the lowercase hostname from a URL, or "" on failure.
func extractHost(rawURL string) string {
	i := strings.Index(rawURL, "://")
	if i < 0 {
		return ""
	}
	rest := rawURL[i+3:]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j]
	}
	// strip port
	if k := strings.LastIndex(rest, ":"); k >= 0 && !strings.Contains(rest[:k], "[") {
		rest = rest[:k]
	}
	return strings.ToLower(rest)
}

// curlWgetHostRE matches curl/wget invocations and captures the hostname.
var curlWgetHostRE = regexp.MustCompile(`(?:curl|wget)\s+(?:-[^\s]+\s+)*https?://([^/\s'"]+)`)

// extractCurlWgetHosts returns the lowercase hostnames found in curl/wget
// invocations within cmd.
func extractCurlWgetHosts(cmd string) []string {
	var hosts []string
	for _, m := range curlWgetHostRE.FindAllStringSubmatch(cmd, -1) {
		if len(m) > 1 && m[1] != "" {
			h := strings.ToLower(m[1])
			// strip port
			if i := strings.LastIndex(h, ":"); i >= 0 {
				h = h[:i]
			}
			hosts = append(hosts, h)
		}
	}
	return hosts
}

// hostAllowed reports whether host matches any entry in allowlist.
// Entries may be exact hostnames ("api.example.com") or dot-prefixed for
// subdomain matching (".example.com" matches "foo.example.com").
func hostAllowed(host string, allowlist []string) bool {
	for _, entry := range allowlist {
		if strings.HasPrefix(entry, ".") {
			if strings.HasSuffix(host, entry) || host == entry[1:] {
				return true
			}
		} else if host == entry {
			return true
		}
	}
	return false
}

// RedactJSON scans the raw JSON input for a tool call and replaces any
// detected secrets (known patterns and high-entropy tokens) with their
// Redact() preview. Returns the original slice when no secrets are found, so
// callers can use a simple bytes.Equal guard to skip no-op writes.
// This is used by the transcript-redaction filter before appending to
// transcript.jsonl (Task 3.5).
func RedactJSON(toolName string, raw []byte) []byte {
	if len(raw) == 0 || !writeTools[toolName] {
		return raw
	}
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return raw
	}
	payload := writePayload(toolName, input)
	result := string(raw)

	for _, pat := range secretPatterns {
		for _, m := range pat.re.FindAllString(payload, -1) {
			result = strings.ReplaceAll(result, m, Redact(pat.name, m))
		}
	}
	for _, tok := range strings.FieldsFunc(payload, isTokenSep) {
		if len(tok) >= minSecretLen && shannonEntropy(tok) >= entropyThreshold {
			result = strings.ReplaceAll(result, tok, Redact("high-entropy", tok))
		}
	}
	return []byte(result)
}
