package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/ryanflachman-liatrio/jig/internal/config"
	"github.com/ryanflachman-liatrio/jig/internal/tui/shared"
)

type openerState struct {
	cancel          context.CancelFunc
	done            <-chan struct{}
	nextID, pending uint64
	launched        bool
	owner           shared.OpenLocationOwner
	notice          string
}

type openerPreparedMsg struct {
	id       uint64
	req      shared.OpenLocationRequest
	argv     []string
	terminal bool
	note     string
	err      error
}

func (m rootModel) openLocationCurrent(owner shared.OpenLocationOwner) bool {
	return m.active == screenMonitor && m.monitor.OpenLocationOwner() == owner
}

func (m rootModel) admitOpenLocation(req shared.OpenLocationRequest) (rootModel, tea.Cmd) {
	if !m.openLocationCurrent(req.Owner) {
		return m, nil
	}
	if m.opener.pending != 0 {
		m.opener.notice = "Open skipped: another editor launch is in progress"
		m.opener.owner = req.Owner
		return m, nil
	}
	m.opener.owner = req.Owner
	if req.Unavailable != "" {
		m.opener.notice = "Open unavailable: " + req.Unavailable
		return m, nil
	}
	m.opener.nextID++
	m.opener.pending = m.opener.nextID
	m.opener.launched = false
	id := m.opener.pending
	m.opener.notice = "Opening file" + shared.EllipsisGlyph
	// Resolve both layers once, before asynchronous work begins.
	cfg := m.tuiConfig.Opener
	cfg.Argv = slices.Clone(cfg.Argv)
	terminal := cfg.TerminalOrDefault()
	cfg.Terminal = &terminal
	editor := os.Getenv("EDITOR")
	return m, func() tea.Msg { return prepareOpenLocation(id, req, cfg, editor) }
}

func prepareOpenLocation(id uint64, req shared.OpenLocationRequest, cfg config.OpenerConfig, editor string) openerPreparedMsg {
	msg := openerPreparedMsg{id: id, req: req, terminal: cfg.TerminalOrDefault()}
	if err := validateOpenLocationPath(req.Target.Path); err != nil {
		msg.err = err
		return msg
	}
	if err := cfg.Validate(); err != nil {
		msg.err = errors.New("invalid opener configuration")
		return msg
	}
	if len(cfg.Argv) > 0 {
		replace := strings.NewReplacer("{path}", req.Target.Path, "{line}", strconv.Itoa(max(1, req.Target.Line)), "{column}", strconv.Itoa(max(1, req.Target.Column)))
		msg.argv = slices.Clone(cfg.Argv)
		for i := 1; i < len(msg.argv); i++ {
			msg.argv[i] = replace.Replace(msg.argv[i])
		}
	} else {
		msg.argv, msg.err = tokenizeEditor(editor)
		if msg.err != nil {
			return msg
		}
		msg.argv = append(msg.argv, req.Target.Path)
		if req.Target.Line > 0 || req.Target.Column > 0 {
			msg.note = "; opened without position navigation — configure tui.opener.argv with {line}/{column}"
		}
	}
	// Resolve executable off the update loop; never include exec's raw errors.
	executable, err := exec.LookPath(msg.argv[0])
	if err != nil {
		msg.err = errors.New("editor executable unavailable; check tui.opener.argv or EDITOR")
		return msg
	}
	msg.argv[0] = executable
	return msg
}

func validateOpenLocationPath(path string) error {
	if path == "" || !utf8.ValidString(path) || strings.ContainsFunc(path, unicode.IsControl) {
		return errors.New("invalid local file path")
	}
	if !filepath.IsAbs(path) {
		return errors.New("location is not an absolute local path; originating directory unavailable")
	}
	info, err := os.Stat(path)
	if err != nil {
		return errors.New("file unavailable; it may have moved or been deleted")
	}
	if !info.Mode().IsRegular() {
		return errors.New("target is not a regular file")
	}
	return nil
}

// tokenizeEditor handles quoting and escapes only. Shell operators, variables,
// command substitutions, and glob characters remain literal argument bytes.
func tokenizeEditor(s string) ([]string, error) {
	var args []string
	var word strings.Builder
	var quote rune
	escaped, started := false, false
	bad := errors.New("invalid EDITOR quoting or executable; configure tui.opener.argv")
	if strings.ContainsRune(s, 0) || !utf8.ValidString(s) {
		return nil, bad
	}
	for _, r := range s {
		switch {
		case escaped:
			word.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped, started = true, true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, started = r, true
		case unicode.IsSpace(r):
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
		default:
			word.WriteRune(r)
			started = true
		}
	}
	if escaped || quote != 0 {
		return nil, bad
	}
	if started {
		args = append(args, word.String())
	}
	if len(args) == 0 {
		return nil, errors.New("configure tui.opener.argv or EDITOR to open files")
	}
	if strings.TrimSpace(args[0]) == "" {
		return nil, bad
	}
	return args, nil
}

func (m rootModel) preparedOpenLocation(msg openerPreparedMsg) (rootModel, tea.Cmd) {
	if msg.id == 0 || msg.id != m.opener.pending || m.opener.launched {
		return m, nil
	}
	if !m.openLocationCurrent(msg.req.Owner) {
		m.opener.pending = 0
		m.opener.notice = ""
		return m, nil
	}
	if msg.err != nil {
		return m.completeOpenLocation(shared.OpenLocationResult{ID: msg.id, Owner: msg.req.Owner, Notice: "Open failed: " + msg.err.Error()})
	}
	m.opener.launched = true
	ctx, cancel := context.WithCancel(m.ctx)
	done := make(chan struct{})
	m.opener.cancel, m.opener.done = cancel, done
	return m, launchOpenLocation(ctx, msg, func() { cancel(); close(done) })
}

func launchOpenLocation(ctx context.Context, msg openerPreparedMsg, finished func()) tea.Cmd {
	cmd := exec.CommandContext(ctx, msg.argv[0], msg.argv[1:]...)
	complete := func(err error) tea.Msg {
		if finished != nil {
			defer finished()
		}
		notice := "Opened current local file" + msg.note
		if err != nil {
			notice = "Open failed: editor could not start or finish"
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				notice = fmt.Sprintf("Open failed: editor exited unsuccessfully (%d)", exit.ExitCode())
			}
			if ctx.Err() != nil {
				notice = "Open canceled"
			}
		}
		return shared.OpenLocationResult{ID: msg.id, Owner: msg.req.Owner, Notice: notice}
	}
	if msg.terminal {
		return tea.ExecProcess(cmd, complete)
	}
	// Nil descriptors connect to the null device: GUI launchers never compete
	// with Bubble Tea's terminal. Run waits and reaps even unsuccessful exits.
	return func() tea.Msg { return complete(cmd.Run()) }
}

func (m rootModel) completeOpenLocation(msg shared.OpenLocationResult) (rootModel, tea.Cmd) {
	if msg.ID == 0 || msg.ID != m.opener.pending {
		return m, nil
	}
	m.opener.pending = 0
	m.opener.launched = false
	m.opener.cancel, m.opener.done = nil, nil
	m.opener.notice = ""
	if m.openLocationCurrent(msg.Owner) {
		m.opener.owner = msg.Owner
		m.opener.notice = msg.Notice
	}
	return m, nil
}

// Quit cancels only the launcher owned by this TUI, then waits for Run to reap
// it before Bubble Tea exits. Navigation never takes this shutdown path.
func (m rootModel) quitAfterOpener() tea.Cmd {
	if m.opener.cancel == nil {
		return tea.Quit
	}
	cancel, done := m.opener.cancel, m.opener.done
	return func() tea.Msg {
		cancel()
		<-done
		return tea.Quit()
	}
}
