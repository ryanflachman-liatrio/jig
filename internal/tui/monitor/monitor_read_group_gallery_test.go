package monitor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"jig/internal/transcript"
)

func TestReadGroupGallery(t *testing.T) {
	dir := os.Getenv("JIG_UI_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set JIG_UI_SNAPSHOT_DIR to capture the read-group gallery")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	type fixtureRead = struct {
		id, path, status string
		offset, limit    int
	}
	failedReads := []fixtureRead{
		{"a", "synthetic/config/alpha.go", "completed", 1, 20},
		{"b", "synthetic/config/alpha.go", "completed", 30, 10},
		{"c", "synthetic/service/beta.go", "failed", 0, 0},
		{"d", "synthetic/config/alpha.go", "completed", 50, 1},
		{"e", "synthetic/config/alpha.go", "completed", 70, 5},
	}
	var gallery strings.Builder
	fmt.Fprintln(&gallery, "# Tool-call grouping gallery — synthetic transcript")
	writeScene := func(name string, width int, reads []fixtureRead, states []toolDisplayState, expanded bool) {
		m := newMonitorWithSteps(t)
		m.transcriptInnerW = width
		m.setChatPage(transcript.Page{Entries: readGroupFixture(reads...)})
		if len(states) > 0 {
			for i := range m.chatItems[0].groupMembers {
				m.chatItems[0].groupMembers[i].displayState = states[i]
			}
			m.chatItems[0].displayState = aggregateReadGroupState(m.chatItems[0].groupMembers)
		}
		m.chatItemExpand[m.chatItems[0].key] = expanded
		fmt.Fprintf(&gallery, "\n## %s · transcriptInnerW=%d\n", name, width)
		body := m.itemTranscriptBody()
		gallery.WriteString(body)
		for i, row := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
			fmt.Fprintf(&gallery, "# row[%d] visible-width=%d\n", i, lipgloss.Width(row))
		}
	}
	stateReads := []fixtureRead{
		{"state-a", "synthetic/state/one.go", "completed", 0, 0},
		{"state-b", "synthetic/state/two.go", "completed", 0, 0},
	}
	writeScene("all-success collapsed", 72, stateReads, []toolDisplayState{toolDisplaySuccess, toolDisplaySuccess}, false)
	writeScene("running collapsed", 72, stateReads, []toolDisplayState{toolDisplaySuccess, toolDisplayRunning}, false)
	writeScene("unknown collapsed", 72, stateReads, []toolDisplayState{toolDisplaySuccess, toolDisplayUnknownUse}, false)
	writeScene("failed repeated-target collapsed narrow", 32, failedReads, nil, false)
	writeScene("failed repeated-target expanded wide", 72, failedReads, nil, true)

	txtPath := filepath.Join(dir, "25-task-02-read-group-gallery.txt")
	if err := os.WriteFile(txtPath, []byte(gallery.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	var interaction strings.Builder
	fmt.Fprintln(&interaction, "# Read-group keyboard interaction — synthetic transcript")
	m := interactionMonitor(t)
	m.transcriptInnerW = 72
	m.setChatPage(readGroupInteractionPage())
	writeInteraction := func(label string) {
		selected := m.chatVisibleItems[m.chatItemCursor]
		fmt.Fprintf(&interaction, "\n## %s\n\ncursor=%d kind=%d visible-items=%d local-expanded=%v expand-all=%v\n\n```text\n%s```\n",
			label, m.chatItemCursor, selected.kind, len(m.chatVisibleItems), m.chatItemExpand[selected.key], m.chatItemExpandAll, proofPlainText(m.chatBody()))
	}
	writeInteraction("initial — item before group selected")
	m, _ = m.Update(key("n"))
	writeInteraction("after n — collapsed group selected")
	m, _ = m.Update(key("enter"))
	writeInteraction("after enter — group expanded")
	m, _ = m.Update(key("enter"))
	writeInteraction("after enter — group collapsed")
	m, _ = m.Update(key("o"))
	writeInteraction("after o — global expansion enabled")
	m, _ = m.Update(key("o"))
	writeInteraction("after o — global expansion disabled")
	m, _ = m.Update(key("n"))
	writeInteraction("after n — item after group selected")
	m, _ = m.Update(key("N"))
	writeInteraction("after N — group selected again")
	m, _ = m.Update(key("N"))
	writeInteraction("after N — item before group selected")
	interactionPath := filepath.Join(dir, "25-task-03-read-group-interaction.txt")
	if err := os.WriteFile(interactionPath, []byte(interaction.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	htmlPath := filepath.Join(dir, "25-task-02-read-group-gallery.html")
	if err := os.WriteFile(htmlPath, []byte(terminalHTML(gallery.String())), 0o644); err != nil {
		t.Fatal(err)
	}

	chrome := readGroupGalleryChrome()
	if chrome == "" {
		t.Fatal("JIG_UI_SNAPSHOT_DIR requires Chrome to generate the read-group PNG proof")
	}
	pngPath := filepath.Join(dir, "25-task-02-read-group-gallery.png")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, chrome,
		"--headless=new", "--no-sandbox", "--disable-gpu", "--hide-scrollbars",
		"--force-device-scale-factor=2", "--window-size=1280,2400",
		"--screenshot="+pngPath, "file://"+htmlPath).CombinedOutput()
	if err != nil {
		t.Fatalf("chrome screenshot failed: %v\n%s", err, out)
	}
}

func proofPlainText(value string) string {
	lines := strings.Split(ansi.Strip(value), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.Join(lines, "\n")
}

func readGroupGalleryChrome() string {
	if chrome, err := exec.LookPath("google-chrome"); err == nil {
		return chrome
	}
	const macChrome = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	if _, err := os.Stat(macChrome); err == nil {
		return macChrome
	}
	return ""
}
