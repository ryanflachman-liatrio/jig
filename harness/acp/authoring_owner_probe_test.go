//go:build darwin || linux

package acp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A declared helper may detach from the adapter's process group without any
// OS sandbox preventing it. The fixture uses only synthetic temporary files.
func TestP3EscapedHelperProcess(t *testing.T) {
	if os.Getenv("JIG_P3_ESCAPED_HELPER") != "1" {
		return
	}
	if _, err := syscall.Setsid(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ready := os.NewFile(3, "p3-ready")
	control := os.NewFile(4, "p3-control")
	if ready == nil || control == nil {
		os.Exit(2)
	}
	fmt.Fprintf(ready, "%d\n", os.Getpid())
	scanner := bufio.NewScanner(control)
	for scanner.Scan() {
		if scanner.Text() == "exit" {
			os.Exit(0)
		}
		data, err := os.ReadFile(os.Getenv("JIG_P3_FROZEN_FILE"))
		if err != nil {
			fmt.Fprintln(ready, "READ_ERROR")
		} else {
			fmt.Fprintln(ready, string(data))
		}
	}
	os.Exit(0)
}

func TestP3ACPAdapterExitDoesNotConfirmHelperTermination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "captured-helper.txt")
	if err := os.WriteFile(source, []byte("ORIGINAL"), 0400); err != nil {
		t.Fatal(err)
	}
	readyR, readyW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	controlR, controlW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = readyR.Close(); _ = readyW.Close(); _ = controlR.Close(); _ = controlW.Close() })
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Background child inherits fd 3/4; stdout/stderr are regular files so a
	// detached child's inherited pipes cannot themselves keep Cmd.Wait blocked.
	cmd := exec.CommandContext(t.Context(), "sh", "-c", `"$JIG_P3_TEST_BINARY" -test.run='^TestP3EscapedHelperProcess$' & wait`)
	cmd.Env = append(os.Environ(), "JIG_P3_ESCAPED_HELPER=1", "JIG_P3_TEST_BINARY="+binary, "JIG_P3_FROZEN_FILE="+source)
	cmd.ExtraFiles = []*os.File{readyW, controlR}
	sink, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	cmd.Stdout = sink
	cmd.Stderr = sink
	configureProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	conn := &Conn{cmd: cmd}
	closed := make(chan error, 1)
	closeStarted, controllerExited := false, false
	startClose := func() {
		if !closeStarted {
			closeStarted = true
			go func() { closed <- conn.Close() }()
		}
	}
	t.Cleanup(func() {
		if !controllerExited {
			startClose()
			select {
			case <-closed:
			case <-time.After(5 * time.Second):
				t.Error("controller cleanup timed out")
			}
		}
	})
	_ = readyW.Close()
	_ = controlR.Close()
	lines := make(chan string, 4)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(readyR)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	receive := func() string {
		t.Helper()
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("helper pipe closed")
			}
			return line
		case <-time.After(5 * time.Second):
			t.Fatal("helper handshake timed out")
			return ""
		}
	}
	var pid int
	if _, err := fmt.Sscan(receive(), &pid); err != nil || pid <= 0 {
		t.Fatal("missing helper identity")
	}
	// Always stop the synthetic survivor, including failed assertion paths.
	helperExited := false
	t.Cleanup(func() {
		if !helperExited {
			_, _ = fmt.Fprintln(controlW, "exit")
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	startClose()
	var closeErr error
	select {
	case closeErr = <-closed:
		controllerExited = true
	case <-time.After(5 * time.Second):
		t.Fatal("adapter Close timed out")
	}
	if cmd.ProcessState == nil {
		t.Fatal("adapter termination unconfirmed")
	}
	if _, err := fmt.Fprintln(controlW, "read"); err != nil {
		t.Fatal(err)
	}
	got := receive()
	if got != "ORIGINAL" {
		t.Fatalf("survivor read=%q", got)
	}
	// This is a counterexample to using adapter Wait as helper termination
	// evidence, NOT a test asserting the complete P3 contract is implemented.
	evidence, _ := json.Marshal(map[string]any{
		"adapter_wait_completed":        true,
		"adapter_close_returned_error":  closeErr != nil,
		"detached_helper_can_read_root": true,
		"helper_output":                 got,
		"safe_root_reuse_authorized":    false,
	})
	t.Log(strings.TrimSpace(string(evidence)))
	if _, err := fmt.Fprintln(controlW, "exit"); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-lines:
		if ok {
			t.Fatal("unexpected helper output during exit")
		}
		helperExited = true
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not close readiness pipe")
	}
}
