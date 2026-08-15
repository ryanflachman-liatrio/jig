package helpchat

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	keybind "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	"charm.land/glamour/v2"
	claudecode "github.com/severity1/claude-agent-sdk-go"

	"jig/internal/engine"
	"jig/internal/tui/shared"
)

type helpFocus int

const (
	focusInput   helpFocus = iota
	focusHistory
)

type helpTurn struct {
	user      string
	assistant string
	isError   bool
}

// Model is the Bubble Tea component for the help agent chat modal.
// dispatchCh is a channel (reference type) so writes inside value-receiver
// methods persist across value copies — same pattern as monitor's render caches.
type Model struct {
	run    *engine.Run
	runDir string
	ctx    context.Context

	// SDK state — replaced on each queryCmd ConnectedMsg (one client per turn).
	client    claudecode.Client
	msgChan   <-chan claudecode.Message
	sessionID string

	// final-merge rendezvous: tool handlers write to gateReq, monitor reads it.
	gateReq chan<- struct{}
	gateAns <-chan bool

	// fire-and-forget: MCP tool handlers write here; waitForDispatchCmd drains.
	dispatchCh chan tea.Msg

	// conversation
	turns   []helpTurn
	pending string // streaming delta for the current assistant turn

	streaming bool
	connected bool

	// focus / layout
	focus  helpFocus
	vpW    int // last rendered viewport width (for rebuild detection)
	vpH    int // last rendered viewport height
	vp     viewport.Model
	ta     textarea.Model

	renderer *glamour.TermRenderer
	snap     engine.RunSnapshot
}

// New constructs a Model ready for Init() to fire connectCmd.
func New(run *engine.Run, runDir string, snap engine.RunSnapshot) Model {
	return Model{
		run:        run,
		runDir:     runDir,
		ctx:        context.Background(),
		dispatchCh: make(chan tea.Msg, 64),
		focus:      focusInput,
		snap:       snap,
		ta:         shared.NewInputTextarea("Ask about this run…", 80, 3),
	}
}

// NewUnavailable returns a Model pre-populated with a static "unavailable"
// turn for journal-replayed runs where no live engine handle exists.
func NewUnavailable() Model {
	m := Model{
		dispatchCh: make(chan tea.Msg, 1),
		focus:      focusInput,
		ta:         shared.NewInputTextarea("Ask about this run…", 80, 3),
	}
	m.turns = []helpTurn{{
		assistant: "Help agent unavailable for completed runs. The help agent requires a live run to read step state.",
		isError:   false,
	}}
	return m
}

// SetChannels wires the final-merge rendezvous channels from the monitor.
// Must be called before Init() so the MCP server has the channels.
func (m *Model) SetChannels(gateReq chan<- struct{}, gateAns <-chan bool) {
	m.gateReq = gateReq
	m.gateAns = gateAns
}

// SetContext sets the context for SDK calls (the monitor's run context).
func (m *Model) SetContext(ctx context.Context) {
	m.ctx = ctx
}

// dispatch implements DispatchFunc. Channel writes persist even through value
// copies because dispatchCh is a reference type.
func (m Model) dispatch(msg tea.Msg) {
	m.dispatchCh <- msg
}

// Init fires connectCmd to pre-flight the SDK connection. If run is nil (a
// journal-replayed run), it pre-populates an error turn and skips connecting.
func (m Model) Init() tea.Cmd {
	if m.run == nil {
		return nil // monitor adds the "unavailable" turn before calling Init
	}
	return connectCmd(m.ctx, m.run, m.runDir, m.snap, m.dispatch, m.gateReq, m.gateAns)
}

// SizeMsg carries the modal's outer dimensions so Update can (re-)initialise
// the viewport, textarea, and glamour renderer without View() writing state.
type SizeMsg struct{ W, H int }

// Update processes messages for the help chat model.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case SizeMsg:
		innerW := msg.W - 4
		innerH := msg.H - 4
		histH := innerH * 80 / 100
		if histH < 1 {
			histH = 1
		}
		inputH := innerH - histH
		if inputH < 2 {
			inputH = 2
		}
		if m.vpW != innerW || m.vpH != histH {
			m.vp = viewport.New(viewport.WithWidth(innerW), viewport.WithHeight(histH))
			old := m.ta.Value()
			m.ta = shared.NewInputTextarea("Ask about this run…", innerW, inputH)
			if old != "" {
				m.ta.SetValue(old)
			}
			m.vpW = innerW
			m.vpH = histH
			m.buildRenderer(innerW)
			m.updateViewport()
		}
		return m, nil


	case ConnectedMsg:
		// Disconnect the old client on subsequent-turn reconnects.
		if m.client != nil && msg.client != m.client {
			_ = m.client.Disconnect()
		}
		if msg.client != nil {
			m.client = msg.client
			m.msgChan = msg.msgChan
		}
		m.connected = true
		// queryCmd returns ConnectedMsg after calling Query, so streaming is active.
		if m.msgChan != nil {
			cmds = append(cmds, waitForMessageCmd(m.msgChan))
		}
		if m.dispatchCh != nil {
			cmds = append(cmds, waitForDispatchCmd(m.dispatchCh))
		}

	case ConnectErrMsg:
		m.turns = append(m.turns, helpTurn{
			isError:   true,
			assistant: fmt.Sprintf("Connection error: %v", msg.err),
		})
		m.updateViewport()

	case DeltaMsg:
		m.pending += string(msg)
		m.updateViewport()
		if m.msgChan != nil {
			cmds = append(cmds, waitForMessageCmd(m.msgChan))
		}

	case TurnCompleteMsg:
		if msg.sessionID != "" {
			m.sessionID = msg.sessionID
		}
		if len(m.turns) > 0 {
			last := &m.turns[len(m.turns)-1]
			if last.assistant == "" {
				last.assistant = m.pending
			}
		}
		m.pending = ""
		m.streaming = false
		m.updateViewport()

	case TurnErrorMsg:
		m.turns = append(m.turns, helpTurn{
			isError:   true,
			assistant: fmt.Sprintf("Error: %v", msg.err),
		})
		m.pending = ""
		m.streaming = false
		m.updateViewport()

	case DispatchedMsg:
		inner := msg.Inner
		cmds = append(cmds, func() tea.Msg { return inner })
		if m.dispatchCh != nil {
			cmds = append(cmds, waitForDispatchCmd(m.dispatchCh))
		}

	case FinalMergeGateMsg:
		// Bubble up to monitor — the monitor re-arms waitForGateReqCmd.
		cmds = append(cmds, func() tea.Msg { return FinalMergeGateMsg{} })

	case tea.KeyPressMsg:
		switch {
		case keybind.Matches(msg, keybind.NewBinding(keybind.WithKeys("tab"))):
			if m.focus == focusInput {
				m.focus = focusHistory
				m.ta.Blur()
			} else {
				m.focus = focusInput
				m.ta.Focus()
			}

		case keybind.Matches(msg, keybind.NewBinding(keybind.WithKeys("enter"))):
			if m.focus == focusInput && !m.streaming && m.ta.Value() != "" {
				userMsg := m.ta.Value()
				m.ta.Reset()
				m.turns = append(m.turns, helpTurn{user: userMsg})
				m.streaming = true
				m.updateViewport()
				sysPrompt := BuildSystemPrompt(m.snap.Workflow, m.snap)
				cmds = append(cmds, queryCmd(
					m.ctx, m.run, m.runDir, m.dispatch, m.gateReq, m.gateAns,
					m.sessionID, sysPrompt, userMsg,
				))
			}

		default:
			if m.focus == focusInput {
				var taCmd tea.Cmd
				m.ta, taCmd = m.ta.Update(msg)
				cmds = append(cmds, taCmd)
			} else {
				var vpCmd tea.Cmd
				m.vp, vpCmd = m.vp.Update(msg)
				cmds = append(cmds, vpCmd)
			}
		}

	default:
		if m.focus == focusInput {
			var taCmd tea.Cmd
			m.ta, taCmd = m.ta.Update(msg)
			cmds = append(cmds, taCmd)
		}
	}

	return m, tea.Batch(cmds...)
}

// View renders the help chat modal content.
// Sizing is handled entirely in Update via SizeMsg — View is a pure render.
func (m Model) View(width, height int, _ bool) string {
	if m.vpW == 0 {
		return ""
	}

	histStr := m.vp.View()
	taStr := m.ta.View()
	hint := shared.Theme.Chat.Hint.Render(`ctrl+\ or esc · close  ·  tab · switch focus`)
	inner := strings.Join([]string{histStr, taStr, hint}, "\n")

	return shared.Theme.Help.Box.
		Width(width - 2).
		Render("Help Agent\n\n" + inner)
}

// CapturesText returns true when the textarea has focus, telling the monitor
// to route all text key presses to this model.
func (m Model) CapturesText() bool {
	return m.focus == focusInput
}

// DispatchCh exposes the dispatch channel for the monitor to arm
// waitForDispatchCmd after initial connection.
func (m Model) DispatchCh() <-chan tea.Msg { return m.dispatchCh }

// ── internal helpers ──────────────────────────────────────────────────────────

func (m *Model) buildRenderer(wordWrap int) {
	if wordWrap < 20 {
		wordWrap = 20
	}
	m.renderer, _ = glamour.NewTermRenderer(
		glamour.WithStyles(shared.Theme.Markdown),
		glamour.WithWordWrap(wordWrap),
	)
}

func (m *Model) updateViewport() {
	if m.vpW == 0 {
		return
	}
	var b strings.Builder
	for _, t := range m.turns {
		if t.user != "" {
			b.WriteString(shared.Theme.UserPrompt.Render("You: "))
			b.WriteString(t.user)
			b.WriteString("\n\n")
		}
		if t.isError {
			b.WriteString(shared.Theme.Error.Render(t.assistant))
			b.WriteString("\n\n")
		} else if t.assistant != "" {
			b.WriteString(m.renderMD(t.assistant))
			b.WriteString("\n")
		}
	}
	if m.pending != "" {
		b.WriteString(m.renderMD(m.pending))
	}
	m.vp.SetContent(b.String())
	m.vp.GotoBottom()
}

func (m Model) renderMD(s string) string {
	if m.renderer != nil {
		if rendered, err := m.renderer.Render(s); err == nil {
			return rendered
		}
	}
	return s
}
