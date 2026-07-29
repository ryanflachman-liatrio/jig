package tui

// turn is one question/answer pair, rendered into its own pane. answer
// accumulates in place while the response streams; rendered is a
// glamour-rendered cache populated lazily once the turn is no longer
// streaming, and is invalidated (reset to "") whenever the glamour
// renderer is rebuilt, since glamour bakes its word-wrap width in at
// construction time. scrollOffset remembers the viewport's YOffset for
// this turn so navigating away and back restores the reader's position.
type turn struct {
	question     string
	answer       string
	rendered     string
	isError      bool
	scrollOffset int
}
