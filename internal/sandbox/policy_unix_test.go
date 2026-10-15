//go:build darwin || linux

package sandbox

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestD04PolicyNonregularAndOwnership(t *testing.T) {
	in, _ := policyFixture(t)
	if err := unix.Mkfifo(in.ExplicitPath, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(in)
	if err == nil {
		t.Fatal("FIFO accepted")
	}
	if !trustedOwner(0) || !trustedOwner(uint32(os.Geteuid())) || trustedOwner(uint32(os.Geteuid()+10000)) {
		t.Fatal("ownership rule")
	}
	t.Log("FIFO rejected without blocking; ownership restricted to root/invoking uid")
}
