package chat

import (
	"context"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	claudecode "github.com/severity1/claude-agent-sdk-go"

	"jig/internal/tui/shared"
)

// focusTarget identifies which pane currently receives non-global key
// input: the chat input textarea, or the output viewport.
type focusTarget int

const (
	focusInput focusTarget = iota
	focusOutput
)

// chatModel is the streaming Claude chat client — the original spike that
// proved SDK streaming into Bubble Tea. It is no longer the app's entry
// screen (see rootModel), but is kept because the coming run monitor will
// reuse per-step streaming.
type chatModel struct {
	textarea textarea.Model
	spinner  spinner.Model
	viewport viewport.Model
	renderer *glamour.TermRenderer
	keys     chatKeys

	turns         []turn
	activeTurn    int
	streamingTurn int
	streaming     bool
	focus         focusTarget

	client  claudecode.Client
	msgChan <-chan claudecode.Message
	ctx     context.Context

	darkBackground bool

	connected bool
	fatal     bool
	fatalErr  error

	ready  bool
	width  int
	height int
}

// New returns the initial state of the streaming chat as a tea.Model.
// ctx governs the lifetime of the Claude connection and every SDK call made on
// it. darkBackground selects the glamour markdown style and must be detected by
// the caller before the terminal is put in raw mode (see cmd/jig/main.go).
func New(ctx context.Context, darkBackground bool) tea.Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = shared.Theme.Spinner

	// enter submits the prompt (handled in Update); width is deferred to the
	// first resize. withoutBorder: the Message panel owns the frame, so the
	// textarea must not draw its own box (avoids a double border).
	ta := shared.NewInputTextarea("Ask Claude... (enter to send, alt+enter for newline, ctrl+c to quit)", 0, 3, shared.WithoutBorder())

	return chatModel{
		textarea:       ta,
		spinner:        s,
		keys:           defaultChatKeys(),
		ctx:            ctx,
		darkBackground: darkBackground,
		focus:          focusInput,
	}
}
