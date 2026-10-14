package acp

import (
	"bufio"
	"os/exec"
	"testing"
)

// A killed adapter exit is ordinary cleanup when its managed group has ended.
// This uses real host processes but no backend login or protocol connection.
func TestManagedAdapterCloseConfirmsKilledGroup(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "sh", "-c", "sleep 60 & echo READY; wait")
	configureProcess(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = killProcess(cmd) })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "READY\n" {
		t.Fatal("adapter readiness", err)
	}
	conn := &Conn{cmd: cmd}
	if err := conn.Close(); err != nil {
		t.Fatalf("confirmed killed group treated as uncertain: %v", err)
	}
	if cmd.ProcessState == nil {
		t.Fatal("adapter not reaped")
	}
}
