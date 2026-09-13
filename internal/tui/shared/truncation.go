package shared

import (
	"strconv"
	"strings"
)

// Truncation vocabulary for the Monitor's Transcript panel (epic
// omp-transcript-parity, slice 06). The helpers below format the count
// phrase, the expand hint, and their combination as plain ASCII text;
// callers are responsible for wrapping the returned string in exactly one
// style pass (typically Theme.Chat.Hint.Render). Keeping styling at the
// caller preserves the "styles live in styles.go" contract and lets the
// same helper serve panels that reach the operator through a different
// hint style token.
//
// The helpers never lie: ExpandHint returns "" when there is nothing to
// reveal, HintLine never emits a leading/trailing space or a double
// space, and MoreItems/EarlierItems pluralize on n == 1 vs everything
// else so a rendered notice always agrees with the count it reports.

// MoreItems renders "… <n> more <word>" using singular when n == 1 and
// plural otherwise. Intended for head-anchored windows where the notice
// is appended below the last visible row.
func MoreItems(n int, singular, plural string) string {
	return "… " + strconv.Itoa(n) + " more " + pluralWord(n, singular, plural)
}

// EarlierItems renders "… <n> earlier <word>" for tail-anchored windows
// where the notice is prepended above the newest visible row.
func EarlierItems(n int, singular, plural string) string {
	return "… " + strconv.Itoa(n) + " earlier " + pluralWord(n, singular, plural)
}

// ExpandHint renders the bracketed expand-key hint "[<key>: Expand]" or
// returns "" when there is nothing to reveal (already expanded, no more
// hidden content) or when the caller supplied no key text. Callers thread
// the live keybinding's Help().Key so a rebind flows through without
// touching the renderer.
func ExpandHint(expanded, hasMore bool, keyHelp string) string {
	if expanded || !hasMore {
		return ""
	}
	key := strings.TrimSpace(keyHelp)
	if key == "" {
		return ""
	}
	return "[" + key + ": Expand]"
}

// HintLine joins a count phrase and an expand hint with a single ASCII
// space. Either side may be empty; the combinator never emits a leading
// or trailing space or a double space so the resulting line is safe to
// pass straight through Theme.Chat.Hint.Render.
func HintLine(more, hint string) string {
	switch {
	case more == "" && hint == "":
		return ""
	case more == "":
		return hint
	case hint == "":
		return more
	default:
		return more + " " + hint
	}
}

// CaptureTruncatedHint returns the fixed wording used when a transcript
// block was truncated at write time. Keeping the literal in one place
// lets the retirement grep-lock and every consumer share one source of
// truth even though there is no quantitative field to format.
func CaptureTruncatedHint() string {
	return "… capture truncated at write"
}

// DiffClampedHint returns the fixed wording the Monitor emits above a
// computed diff when the enclosing transcript block was truncated at
// write time. Slice 07 pins the phrasing here so the diff section, the
// existing capture-truncated hint, and any future consumer share one
// canonical source of truth.
func DiffClampedHint() string {
	return "… content clamped at write; diff may be incomplete"
}

// DiffUnavailableHint returns the fixed wording used when diff
// computation was skipped or failed and the Monitor falls back to the
// resulting-source card. Kept alongside CaptureTruncatedHint and
// DiffClampedHint so the whole diff-fallback vocabulary lives in one
// place (slice 07 FR-07.15).
func DiffUnavailableHint() string {
	return "… diff unavailable; showing resulting source"
}

func pluralWord(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
