// Package operatorcfg inspects operator-local Codex and Cursor permission
// config that auto-approves tool calls without a permission prompt, and so
// without jig's Tier-1 guard seeing them. ACP mode does not override these
// sources. The package is a leaf shared by the runner (checked before a guarded
// step opens its session) and doctor.
package operatorcfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"jig/internal/sentinel"
)

// Class says how much a finding widens auto-approval.
type Class string

const (
	// Blanket auto-approves every call. A guarded step fails closed on it.
	Blanket Class = "blanket"
	// Narrow auto-approves calls matching one entry. It is reported, and the
	// step still runs.
	Narrow Class = "narrow"
)

// Finding is one auto-approval source. Entry never holds the raw entry text:
// it is a short label (the entry's kind, with arguments elided) passed through
// sentinel.RedactText, so a credential inside an entry cannot leak into
// errors, findings or doctor output.
type Finding struct {
	Class Class
	File  string
	// Key names the setting, for example "approvalMode", "permissions.allow"
	// or "prefix_rule".
	Key string
	// Index is the entry's zero-based position in its list or file, or -1 for
	// a setting that is not a list entry.
	Index int
	Entry string
}

// Location renders the file, key and entry index.
func (f Finding) Location() string {
	if f.Index < 0 {
		return f.File + ": " + f.Key
	}
	return fmt.Sprintf("%s: %s entry %d", f.File, f.Key, f.Index)
}

// String renders the finding for an error, a finding detail, or doctor.
func (f Finding) String() string {
	return f.Location() + " (" + f.Entry + ")"
}

const (
	backendCodex  = "codex"
	backendCursor = "cursor"
)

// Inspect returns the auto-approval findings for backend, in a stable order.
// projectRoot and cwd bound the project-local files it reads; env supplies
// HOME and the backend's config-directory variables. A missing file is not a
// finding; an unreadable or malformed one is an error, so callers fail closed.
// Backends other than Codex and Cursor have nothing to inspect.
func Inspect(backend, projectRoot, cwd string, env func(string) string) ([]Finding, error) {
	var (
		findings []Finding
		err      error
	)
	switch backend {
	case backendCursor:
		findings, err = inspectCursor(projectRoot, cwd, env)
	case backendCodex:
		findings, err = inspectCodex(projectRoot, cwd, env)
	default:
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect %s operator config: %w", backend, err)
	}
	return findings, nil
}

// HasBlanket reports whether any finding is blanket auto-approval.
func HasBlanket(findings []Finding) bool {
	for _, f := range findings {
		if f.Class == Blanket {
			return true
		}
	}
	return false
}

func inspectCursor(projectRoot, cwd string, env func(string) string) ([]Finding, error) {
	dir := env("CURSOR_CONFIG_DIR")
	if dir == "" && env("XDG_CONFIG_HOME") != "" {
		dir = filepath.Join(env("XDG_CONFIG_HOME"), "cursor")
	}
	if dir == "" {
		home := env("HOME")
		if home == "" {
			return nil, errors.New("HOME is not set")
		}
		dir = filepath.Join(home, ".cursor")
	}

	var findings []Finding
	// permissions.json forces Cursor's unrestricted approval mode.
	permissions := filepath.Join(dir, "permissions.json")
	if _, err := os.Stat(permissions); err == nil {
		findings = append(findings, Finding{Class: Blanket, File: permissions, Key: "permissions.json", Index: -1, Entry: "file present"})
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	files := []string{filepath.Join(dir, "cli-config.json")}
	for _, d := range projectDirs(projectRoot, cwd) {
		files = append(files, filepath.Join(d, ".cursor", "cli.json"))
	}
	for _, file := range files {
		got, err := inspectCursorCLI(file)
		if err != nil {
			return nil, err
		}
		findings = append(findings, got...)
	}
	return findings, nil
}

func inspectCursorCLI(file string) ([]Finding, error) {
	data, ok, err := readOptional(file)
	if err != nil || !ok {
		return nil, err
	}
	var cfg struct {
		ApprovalMode string `json:"approvalMode"`
		Permissions  struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, malformed(file, "JSON", err)
	}
	var findings []Finding
	if cfg.ApprovalMode == "unrestricted" {
		findings = append(findings, Finding{Class: Blanket, File: file, Key: "approvalMode", Index: -1, Entry: `approvalMode = "unrestricted"`})
	}
	for i, entry := range cfg.Permissions.Allow {
		findings = append(findings, Finding{Class: Narrow, File: file, Key: "permissions.allow", Index: i, Entry: label(entry)})
	}
	return findings, nil
}

func inspectCodex(projectRoot, cwd string, env func(string) string) ([]Finding, error) {
	home := env("CODEX_HOME")
	if home == "" {
		userHome := env("HOME")
		if userHome == "" {
			return nil, errors.New("HOME is not set")
		}
		home = filepath.Join(userHome, ".codex")
	}

	var findings []Finding
	configs := []string{filepath.Join(home, "config.toml")}
	ruleDirs := []string{filepath.Join(home, "rules")}
	for _, d := range projectDirs(projectRoot, cwd) {
		configs = append(configs, filepath.Join(d, ".codex", "config.toml"))
		ruleDirs = append(ruleDirs, filepath.Join(d, ".codex", "rules"))
	}
	for _, file := range configs {
		got, err := inspectCodexConfig(file)
		if err != nil {
			return nil, err
		}
		findings = append(findings, got...)
	}
	if raw := env("CODEX_CONFIG"); raw != "" {
		var cfg map[string]any
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return nil, malformed("CODEX_CONFIG", "JSON", err)
		}
		if reviewer, ok := cfg["approvals_reviewer"]; ok && reviewer != "user" {
			findings = append(findings, reviewerFinding("CODEX_CONFIG"))
		}
	}
	for _, dir := range ruleDirs {
		got, err := inspectCodexRules(dir)
		if err != nil {
			return nil, err
		}
		findings = append(findings, got...)
	}
	return findings, nil
}

func inspectCodexConfig(file string) ([]Finding, error) {
	data, ok, err := readOptional(file)
	if err != nil || !ok {
		return nil, err
	}
	var cfg map[string]any
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, malformed(file, "TOML", err)
	}
	if reviewer, ok := cfg["approvals_reviewer"]; ok && reviewer != "user" {
		return []Finding{reviewerFinding(file)}, nil
	}
	return nil, nil
}

// malformed reports a config that does not parse by its position only. The
// decoders quote the offending text, and operator config (for example an MCP
// server's env table) can hold secrets, so the decoder error is never wrapped.
func malformed(where, format string, err error) error {
	var (
		tomlErr   toml.ParseError
		syntaxErr *json.SyntaxError
		typeErr   *json.UnmarshalTypeError
	)
	switch {
	case errors.As(err, &tomlErr):
		return fmt.Errorf("%s: malformed %s at line %d", where, format, tomlErr.Position.Line)
	case errors.As(err, &syntaxErr):
		return fmt.Errorf("%s: malformed %s at byte %d", where, format, syntaxErr.Offset)
	case errors.As(err, &typeErr):
		return fmt.Errorf("%s: malformed %s at byte %d", where, format, typeErr.Offset)
	}
	return fmt.Errorf("%s: malformed %s", where, format)
}

func reviewerFinding(file string) Finding {
	return Finding{Class: Blanket, File: file, Key: "approvals_reviewer", Index: -1, Entry: `approvals_reviewer is not "user"`}
}

var ruleDecision = regexp.MustCompile(`decision\s*=\s*["']([A-Za-z_-]+)["']`)

// inspectCodexRules reports each exec-policy prefix_rule whose decision
// auto-approves: an explicit "allow", or no decision (Codex's default).
func inspectCodexRules(dir string) ([]Finding, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.rules"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	var findings []Finding
	for _, file := range matches {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		for i, call := range prefixRuleCalls(string(data)) {
			decision := "allow"
			if m := ruleDecision.FindStringSubmatch(call); m != nil {
				decision = m[1]
			}
			if decision == "allow" {
				findings = append(findings, Finding{Class: Narrow, File: file, Key: "prefix_rule", Index: i, Entry: "prefix_rule(…)"})
			}
		}
	}
	return findings, nil
}

// prefixRuleCalls returns the argument text of each prefix_rule(...) call in
// a Starlark rules file, skipping comments and honoring quoted strings.
func prefixRuleCalls(src string) []string {
	var calls []string
	const head = "prefix_rule("
	for i := 0; i < len(src); i++ {
		switch {
		case src[i] == '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case strings.HasPrefix(src[i:], head) && (i == 0 || !isIdent(src[i-1])):
			start := i + len(head)
			end := matchParen(src, start)
			calls = append(calls, src[start:end])
			i = end
		}
	}
	return calls
}

func matchParen(src string, i int) int {
	depth := 1
	var quote byte
	for ; i < len(src); i++ {
		c := src[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(src)
}

func isIdent(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// label keeps only an allow entry's kind, such as "Shell(…)", so arguments
// that may carry credentials never leave this package.
func label(entry string) string {
	kind, _, found := strings.Cut(entry, "(")
	kind = strings.TrimSpace(kind)
	if kind == "" || !found {
		return sentinel.RedactText("entry")
	}
	return sentinel.RedactText(kind + "(…)")
}

// projectDirs lists projectRoot and every directory below it down to cwd.
// When cwd is empty or outside projectRoot only projectRoot is listed.
func projectDirs(projectRoot, cwd string) []string {
	if projectRoot == "" {
		projectRoot = cwd
	}
	if projectRoot == "" {
		return nil
	}
	root := filepath.Clean(projectRoot)
	dirs := []string{root}
	if cwd == "" {
		return dirs
	}
	rel, err := filepath.Rel(root, filepath.Clean(cwd))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return dirs
	}
	dir := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		dir = filepath.Join(dir, part)
		dirs = append(dirs, dir)
	}
	return dirs
}

func readOptional(file string) ([]byte, bool, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}
