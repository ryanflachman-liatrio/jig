//go:build darwin || linux

package workflow

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Separate process mutation, released only after the parent's pinned handle or
// final open. Environment paths refer exclusively to synthetic temp fixtures.
func TestP3CaptureMutationProcess(t *testing.T) {
	mode := os.Getenv("JIG_P3_CAPTURE_MUTATION")
	if mode == "" {
		return
	}
	root := os.Getenv("JIG_P3_CAPTURE_ROOT")
	outside := os.Getenv("JIG_P3_CAPTURE_OUTSIDE")
	fmt.Println("ready")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() || scanner.Text() != "change" {
		os.Exit(2)
	}
	var err error
	switch mode {
	case "anchor":
		err = os.Rename(root, root+"-pinned")
		if err == nil {
			err = os.Symlink(outside, root)
		}
	case "file":
		err = os.Rename(filepath.Join(root, "source"), filepath.Join(root, "original"))
		if err == nil {
			err = os.Symlink(filepath.Join(outside, "source"), filepath.Join(root, "source"))
		}
	case "link":
		err = os.Remove(filepath.Join(root, "link"))
		if err == nil {
			err = os.Symlink(filepath.Join(outside, "source"), filepath.Join(root, "link"))
		}
	default:
		os.Exit(3)
	}
	if err != nil {
		os.Exit(4)
	}
	fmt.Println("changed")
	os.Exit(0)
}

func TestAuthoringAssetPolicyProcessRetarget(t *testing.T) {
	for _, mode := range []string{"anchor", "file", "link"} {
		t.Run(mode, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "selected")
			outside := filepath.Join(parent, ".aws")
			p3Write(t, dir, "source", []byte("ORIGINAL"))
			p3Write(t, outside, "source", []byte("EXCLUDED"))
			if err := os.Symlink("source", filepath.Join(dir, "link")); err != nil {
				t.Fatal(err)
			}
			root, err := p3Pin(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "-test.run=^TestP3CaptureMutationProcess$")
			cmd.Env = append(os.Environ(), "JIG_P3_CAPTURE_MUTATION="+mode, "JIG_P3_CAPTURE_ROOT="+dir, "JIG_P3_CAPTURE_OUTSIDE="+outside)
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			t.Cleanup(func() {
				input.Close()
				if !waited {
					cancel()
					_ = cmd.Wait()
				}
			})
			scan := bufio.NewScanner(output)
			if !scan.Scan() || scan.Text() != "ready" {
				t.Fatal("mutation process did not become ready")
			}
			change := func() {
				if _, err := fmt.Fprintln(input, "change"); err != nil {
					t.Fatal(err)
				}
				if !scan.Scan() || scan.Text() != "changed" {
					t.Fatal("mutation process did not finish")
				}
			}
			afterOpen := change
			path := "source"
			if mode == "anchor" {
				change()
				afterOpen = nil
			}
			if mode == "link" {
				path = "link"
			}
			ledger := new(p3Ledger)
			captured, err := ledger.capture(root, path, afterOpen)
			if mode == "file" {
				if err != p3Integrity || len(captured.bytes) != 0 {
					t.Fatalf("replacement accepted: %q %v", captured.bytes, err)
				}
			} else if err != nil || string(captured.bytes) != "ORIGINAL" {
				t.Fatalf("pinned original=%q err=%v", captured.bytes, err)
			}
			if ledger.read > len("ORIGINAL") {
				t.Fatalf("unexpected read count %d", ledger.read)
			}
			for _, b := range ledger.blobs {
				if string(b) != "ORIGINAL" {
					t.Fatal("excluded replacement captured")
				}
			}
			input.Close()
			if err := cmd.Wait(); err != nil {
				t.Fatal(err)
			}
			waited = true
			t.Logf("barrier=%s read_bytes=%d blobs=%d; excluded bytes never captured", mode, ledger.read, len(ledger.blobs))
		})
	}
}

func TestAuthoringAssetPolicyExactAbsoluteFile(t *testing.T) {
	dir := t.TempDir()
	p3Write(t, dir, "selected", []byte("ORIGINAL"))
	p3Write(t, dir, ".env", []byte("EXCLUDED"))
	selected := filepath.Join(dir, "selected")
	root, err := p3Pin(filepath.Dir(selected))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ledger := new(p3Ledger)
	got, err := ledger.capture(root, filepath.Base(selected), nil)
	if err != nil || string(got.bytes) != "ORIGINAL" || ledger.read != 8 || ledger.bindings != 1 || len(ledger.blobs) != 1 {
		t.Fatalf("exact-file capture: %v ledger=%+v", err, ledger)
	}
	if _, err := ledger.capture(root, ".env", nil); err != p3Forbidden || ledger.read != 8 {
		t.Fatal("neighbor exclusion failed")
	}
}
