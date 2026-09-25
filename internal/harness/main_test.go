package harness_test

import (
	"fmt"
	"os"
	"testing"
)

// TestMain isolates the operator-local Codex and Cursor config that guarded
// steps inspect before opening a session, so fixture runs never read the
// developer's own ~/.cursor or ~/.codex.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "jig-operator-config-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, key := range []string{"CURSOR_CONFIG_DIR", "CODEX_HOME"} {
		if err := os.Setenv(key, dir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	_ = os.Unsetenv("CODEX_CONFIG")
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
