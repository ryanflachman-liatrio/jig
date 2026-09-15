package shared

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// TestSlice02RenderersUseCentralizedTheme protects the two repository rules
// that make slice 14's later theme/preset substitutions possible without
// scanning every call site:
//
//  1. New per-slot styles (Chat.Tool*) live in styles.go and per-state
//     palette hex values live in palette.go; the shared files introduced by
//     slice 02 may not add a package-level `lipgloss.NewStyle()` value or a
//     hardcoded hex color.
//  2. Status and tool glyphs go through `shared.Icon*` constants; the shared
//     files introduced by slice 02 may not embed bare glyph literals.
//
// The audit is scoped to the files this slice introduces (status_line.go and
// status_icon.go). Pre-existing files (panel.go, card.go, help.go) already
// have their own conventions and are not part of this slice's contract.
func TestSlice02RenderersUseCentralizedTheme(t *testing.T) {
	sliceFiles := []string{
		"status_line.go",
		"status_icon.go",
	}
	hexRe := regexp.MustCompile(`"#[0-9A-Fa-f]{3,8}"`)
	for _, name := range sliceFiles {
		path := filepath.Join(".", name)
		bytes, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read %s: %v", path, err)
		}
		src := string(bytes)
		if strings.Contains(src, "lipgloss.NewStyle(") {
			t.Errorf("%s contains a bare `lipgloss.NewStyle()` — new styles belong in styles.go", name)
		}
		if hexRe.MatchString(src) {
			t.Errorf("%s contains a hardcoded `#hex` color — palette tokens belong in palette.go", name)
		}
	}
}

// TestSlice02IconGlyphsAreCentralized checks that the new IconStatus* and
// IconTool* glyphs are defined in icons.go rather than embedded at call
// sites, so slice 14's preset table can substitute them in one place.
func TestSlice02IconGlyphsAreCentralized(t *testing.T) {
	glyphNames := []string{
		"IconStatusSuccess", "IconStatusError", "IconStatusRunning",
		"IconStatusPending", "IconStatusWarning",
		"IconToolRead", "IconToolEdit", "IconToolWrite", "IconToolSearch",
		"IconToolShell", "IconToolWeb", "IconToolAgent", "IconToolTodo",
		"IconToolAsk",
	}
	iconsBytes, err := os.ReadFile("icons.go")
	if err != nil {
		t.Fatalf("read icons.go: %v", err)
	}
	iconsSrc := string(iconsBytes)
	// Match `Name` followed by any run of spaces (const-block or var-block
	// alignment varies). Anchored to a line start via preceding whitespace.
	for _, name := range glyphNames {
		re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `\s+string`)
		if !re.MatchString(iconsSrc) {
			t.Errorf("shared/icons.go is missing declaration %q", name)
		}
	}
}

// TestVocabularyLiteralsAreCentralized is slice 14's discipline check
// (FR-14.1): scans every non-test file under `internal/tui/**` outside
// the vocabulary sources (icons.go, symbols.go, spinner.go) and
// rejects any string literal that contains a non-ASCII rune present
// in the Unicode preset. A regression that reintroduces a bare `"○"`
// or `"╭"` fails here rather than surfacing as a broken ASCII flip.
//
// Only non-ASCII runes are enforced: many ASCII fallback values
// (`|`, `.`, `>`, `?`) legitimately appear as string literals for
// unrelated purposes, so scanning them would produce false positives.
//
// Three characters carry the vocabulary's semantic weight but also
// appear throughout prose as pure punctuation (middle-dot separator,
// em-dash separator, bullet). These are excluded from the forbidden
// set because migrating every text separator to a vocabulary key is
// outside slice 14's scope (the plan explicitly frames FR-14.5 as
// "every glyph in asciiSymbols" rather than "every rune in output"):
//
//   - U+00B7 (·) — used both as IconStateUnknown and as `" · "` meta
//     separator in every status line, breadcrumb, and hint.
//   - U+2014 (—) — used both as IconSkipped and as an em-dash
//     separator in help text.
//   - U+2022 (•) — used both as IconStatusSuccess and as a bullet in
//     the help panel's keybinding list.
//
// A follow-up slice can migrate those separators through a dedicated
// MetaSeparator vocabulary key and remove the exclusion below.
//
// Line comments are stripped before scanning so a comment can still
// name a glyph in prose; backtick raw strings and regular double-quoted
// literals are both scanned since either can carry a decoration.
func TestVocabularyLiteralsAreCentralized(t *testing.T) {
	ambiguousPunctuation := map[rune]bool{
		'·': true, // U+00B7 middle dot
		'—': true, // U+2014 em dash
		'•': true, // U+2022 bullet
	}

	forbidden := map[rune]string{}
	unicodeVal := reflect.ValueOf(unicodeSymbols)
	typ := unicodeVal.Type()
	for i := 0; i < unicodeVal.NumField(); i++ {
		if unicodeVal.Field(i).Kind() != reflect.String {
			continue
		}
		val := unicodeVal.Field(i).String()
		for _, r := range val {
			if r < 0x80 || ambiguousPunctuation[r] {
				continue
			}
			forbidden[r] = typ.Field(i).Name
		}
	}
	if len(forbidden) == 0 {
		t.Fatal("no non-ASCII runes in the Unicode preset; test would silently pass on every file")
	}

	// Files that own the vocabulary or intentionally register glyph
	// literals (spinner frame sets). No other exceptions are permitted;
	// a call site that needs a glyph adds a new vocabulary key rather
	// than an entry here.
	skipFiles := map[string]bool{
		"icons.go":   true,
		"symbols.go": true,
		"spinner.go": true,
	}

	// Walk `internal/tui/**` from the package directory. Skip _test.go
	// files: they assert on rendered output, so they may legitimately
	// carry glyph literals (\`strings.Contains(plain, \"╭\")\`).
	root := "../"
	var violations []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		if strings.HasSuffix(name, "_test.go") {
			return nil
		}
		if skipFiles[name] {
			return nil
		}
		bytes, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := stripLineComments(string(bytes))
		for _, lit := range extractStringLiterals(src) {
			for _, r := range lit.content {
				if key, ok := forbidden[r]; ok {
					rel, relErr := filepath.Rel(root, path)
					if relErr != nil {
						rel = path
					}
					violations = append(violations,
						rel+": "+lit.location+" contains "+string(r)+
							" — use shared."+key+" instead")
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/tui: %v", err)
	}
	if len(violations) > 0 {
		t.Errorf("bare vocabulary glyph literals found (%d):\n  %s", len(violations),
			strings.Join(violations, "\n  "))
	}
}

// literalMatch is one string literal extracted from a source file,
// with a human-readable location for the discipline test's error
// output. content is the unquoted body (no surrounding \`"\` or
// backticks).
type literalMatch struct {
	content  string
	location string
}

// extractStringLiterals returns every double-quoted or raw string
// literal in src. Comments must already be stripped by the caller
// (stripLineComments does that for line comments; block comments are
// unusual in this repo and are left in place — a decorative glyph in
// a block comment is not a rendered literal).
func extractStringLiterals(src string) []literalMatch {
	var out []literalMatch
	line := 1
	i := 0
	for i < len(src) {
		switch src[i] {
		case '\n':
			line++
			i++
		case '"':
			start := i + 1
			end := start
			for end < len(src) && src[end] != '"' && src[end] != '\n' {
				if src[end] == '\\' && end+1 < len(src) {
					end += 2
					continue
				}
				end++
			}
			if end <= len(src) && end > start {
				out = append(out, literalMatch{
					content:  src[start:end],
					location: "line " + itoa(line) + ": " + quote(src[start:end]),
				})
			}
			if end < len(src) && src[end] == '"' {
				i = end + 1
			} else {
				i = end
			}
		case '`':
			start := i + 1
			end := start
			startLine := line
			for end < len(src) && src[end] != '`' {
				if src[end] == '\n' {
					line++
				}
				end++
			}
			if end > start {
				out = append(out, literalMatch{
					content:  src[start:end],
					location: "line " + itoa(startLine) + ": `" + src[start:end] + "`",
				})
			}
			if end < len(src) {
				i = end + 1
			} else {
				i = end
			}
		default:
			i++
		}
	}
	return out
}

// stripLineComments removes // ...\n line comments from src but
// preserves string literals verbatim. The scanner is deliberately
// naive: it recognizes double-quoted strings, backtick raw strings,
// and line comments, and skips over everything else. Block comments
// (/* ... */) are left in place because the repository does not use
// them for glyph literals; a decorative glyph inside a block comment
// would be a false positive but is very rare and can be scrubbed
// manually if it ever arises.
func stripLineComments(src string) string {
	var b strings.Builder
	i := 0
	for i < len(src) {
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '/' {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		if src[i] == '"' {
			b.WriteByte(src[i])
			i++
			for i < len(src) && src[i] != '"' && src[i] != '\n' {
				if src[i] == '\\' && i+1 < len(src) {
					b.WriteByte(src[i])
					b.WriteByte(src[i+1])
					i += 2
					continue
				}
				b.WriteByte(src[i])
				i++
			}
			if i < len(src) {
				b.WriteByte(src[i])
				i++
			}
			continue
		}
		if src[i] == '`' {
			b.WriteByte(src[i])
			i++
			for i < len(src) && src[i] != '`' {
				b.WriteByte(src[i])
				i++
			}
			if i < len(src) {
				b.WriteByte(src[i])
				i++
			}
			continue
		}
		b.WriteByte(src[i])
		i++
	}
	return b.String()
}

// itoa is a tiny helper so extractStringLiterals doesn't take a
// runtime dependency on strconv (which would pollute the tiny grep
// scanner with a full package import). Handles positive ints only.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}

// quote formats s as a Go double-quoted string literal for the
// discipline test's error output. Only the runes strictly needed for
// legibility get escaped (\", \\, \n, \t); everything else including
// Unicode glyphs prints verbatim so the maintainer can read the
// offending literal directly.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
