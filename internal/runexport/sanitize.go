package runexport

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"jig/internal/sentinel"
)

// redactedMarker is the fixed replacement token for a detected secret or a
// prior sentinel redaction marker. It never retains any part of the matched
// value (spec FR-09: "including the retained suffix of existing sentinel
// redaction markers").
func redactedMarker(category string) string { return "[REDACTED:" + category + "]" }

// priorMarkerRE matches the existing live-monitor preview marker shape
// (sentinel.Redact: "[category:…suffix]") so export sanitization removes any
// four-character suffix that already reached a transcript before this
// stricter export boundary existed.
var priorMarkerRE = regexp.MustCompile(`\[[a-z0-9-]+:…[^\]]{0,64}\]`)

// ansiCSI matches ANSI CSI escape sequences (e.g. cursor movement, color).
var ansiCSI = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")

// idReplacement is one deterministic original-value-to-alias substitution.
// boundary selects which adjacency rule protects against rewriting a common
// substring of a longer token or path.
type idReplacement struct {
	original    string
	replacement string
	boundary    string // "token" | "path"
}

// exportSanitizer is the single deterministic full-replacement pass every
// retained free-text value goes through before it reaches an exported record
// (spec FR-09). It is never the partial four-character sentinel.Redact
// preview used by the live monitor.
type exportSanitizer struct {
	replacements []idReplacement
	counters     *Counters
}

func newExportSanitizer(replacements []idReplacement, counters *Counters) *exportSanitizer {
	// Longer originals first, so a short id that happens to be a substring of
	// a longer one never shadows the longer, more specific replacement.
	sorted := append([]idReplacement(nil), replacements...)
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i].original) > len(sorted[j].original) })
	return &exportSanitizer{replacements: sorted, counters: counters}
}

// Sanitize is the complete deterministic replacement described by spec
// FR-09/FR-10: control stripping, prior-marker removal, secret detection,
// identifier/path replacement, UTF-8 normalization, then truncation last so a
// credential is never cut into an unrecognized-looking prefix.
func (sn *exportSanitizer) Sanitize(s string) string {
	if s == "" {
		return s
	}
	s = stripControls(s)
	s = sn.redactPriorMarkers(s)
	s = sn.redactSecrets(s)
	for _, r := range sn.replacements {
		var count int
		if r.boundary == "path" {
			s, count = replaceWithBoundary(s, r.original, r.replacement, pathWordChar)
		} else {
			s, count = replaceWithBoundary(s, r.original, r.replacement, tokenWordChar)
		}
		if count > 0 {
			sn.counters.Replacements[r.boundary] += count
		}
	}
	s = strings.ToValidUTF8(s, "�")
	truncated, cut := truncateUTF8(s, maxRetainedText)
	if cut {
		sn.counters.Truncations++
	}
	return truncated
}

func (sn *exportSanitizer) redactPriorMarkers(s string) string {
	matches := priorMarkerRE.FindAllString(s, -1)
	if len(matches) == 0 {
		return s
	}
	sn.counters.Replacements["prior_marker"] += len(matches)
	return priorMarkerRE.ReplaceAllString(s, "[REDACTED]")
}

func (sn *exportSanitizer) redactSecrets(s string) string {
	matches := sentinel.DetectSecrets(s)
	if len(matches) == 0 {
		return s
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Start < matches[j].Start })
	kept := matches[:0:0]
	lastEnd := -1
	for _, m := range matches {
		if m.Start < lastEnd {
			continue
		}
		kept = append(kept, m)
		lastEnd = m.End
	}
	for i := len(kept) - 1; i >= 0; i-- {
		m := kept[i]
		s = s[:m.Start] + redactedMarker(m.Category) + s[m.End:]
		sn.counters.Replacements[m.Category]++
	}
	return s
}

func stripControls(s string) string {
	s = ansiCSI.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\t' {
			b.WriteRune(r)
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func tokenWordChar(c byte) bool {
	return isAlnum(c) || c == '_' || c == '-'
}

func pathWordChar(c byte) bool {
	return isAlnum(c) || c == '_' || c == '.'
}

func isAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// replaceWithBoundary replaces every occurrence of old in s with new, except
// where an adjacent byte satisfies isWordChar — which would mean old is only
// part of a longer token/path segment rather than a whole match. It returns
// the replaced count so callers can maintain fixed-category counters.
func replaceWithBoundary(s, old, new string, isWordChar func(byte) bool) (string, int) {
	if old == "" {
		return s, 0
	}
	var b strings.Builder
	count := 0
	for {
		idx := strings.Index(s, old)
		if idx < 0 {
			b.WriteString(s)
			break
		}
		leftOK := idx == 0 || !isWordChar(s[idx-1])
		rightPos := idx + len(old)
		rightOK := rightPos >= len(s) || !isWordChar(s[rightPos])
		b.WriteString(s[:idx])
		if leftOK && rightOK {
			b.WriteString(new)
			count++
		} else {
			b.WriteString(old)
		}
		s = s[rightPos:]
	}
	return b.String(), count
}

// truncateUTF8 cuts s to at most max bytes on a valid rune boundary and
// appends a fixed marker, reporting whether truncation occurred.
func truncateUTF8(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…[truncated]", true
}
