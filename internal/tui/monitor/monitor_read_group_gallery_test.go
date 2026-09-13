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

	reads := []struct {
		id, path, status string
		offset, limit    int
	}{
		{"a", "synthetic/config/alpha.go", "completed", 1, 20},
		{"b", "synthetic/config/alpha.go", "completed", 30, 10},
		{"c", "synthetic/service/beta.go", "failed", 0, 0},
		{"d", "synthetic/config/alpha.go", "completed", 50, 1},
		{"e", "synthetic/config/alpha.go", "completed", 70, 5},
	}
	var gallery strings.Builder
	fmt.Fprintln(&gallery, "# Tool-call grouping gallery — synthetic transcript")
	for _, width := range []int{32, 72} {
		m := newMonitorWithSteps(t)
		m.transcriptInnerW = width
		m.setChatPage(transcript.Page{Entries: readGroupFixture(reads...)})
		fmt.Fprintf(&gallery, "\n## collapsed · transcriptInnerW=%d\n", width)
		body := m.itemTranscriptBody()
		gallery.WriteString(body)
		for i, row := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
			fmt.Fprintf(&gallery, "# row[%d] visible-width=%d\n", i, lipgloss.Width(row))
		}
		m.chatItemExpand[m.chatItems[0].key] = true
		fmt.Fprintf(&gallery, "\n## expanded · transcriptInnerW=%d\n", width)
		gallery.WriteString(m.itemTranscriptBody())
	}

	txtPath := filepath.Join(dir, "25-task-02-read-group-gallery.txt")
	if err := os.WriteFile(txtPath, []byte(gallery.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	interactionPath := filepath.Join(dir, "25-task-03-read-group-interaction.txt")
	if err := os.WriteFile(interactionPath, []byte(gallery.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	htmlPath := filepath.Join(dir, "25-task-02-read-group-gallery.html")
	if err := os.WriteFile(htmlPath, []byte(terminalHTML(gallery.String())), 0o644); err != nil {
		t.Fatal(err)
	}

	chrome := readGroupGalleryChrome()
	if chrome == "" {
		return
	}
	pngPath := filepath.Join(dir, "25-task-02-read-group-gallery.png")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, chrome,
		"--headless=new", "--no-sandbox", "--disable-gpu", "--hide-scrollbars",
		"--force-device-scale-factor=2", "--window-size=1280,1000",
		"--screenshot="+pngPath, "file://"+htmlPath).CombinedOutput()
	if err != nil {
		t.Logf("chrome screenshot failed: %v\n%s", err, out)
	}
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
