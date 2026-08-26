package monitor

import (
	"strings"
	"testing"

	"jig/internal/transcript"
)

func TestUserGuidanceIsDistinctFromRoleUserToolResult(t *testing.T) {
	m := New("run")
	var bodyBuilder strings.Builder
	m.writeBlock(&bodyBuilder, blockKey{seq: 1, block: 0}, transcript.Block{Type: transcript.BlockText, Text: "Please inspect the config."}, transcript.RoleUser)
	body := stripANSI(bodyBuilder.String())
	if !strings.Contains(body, "User") || !strings.Contains(body, "Please inspect the config.") {
		t.Fatalf("user guidance missing from %q", body)
	}
}
