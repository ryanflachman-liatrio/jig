package harness

import (
	"testing"
)

func TestCursorHarnessCapabilities(t *testing.T) {
	h := NewCursorHarness()
	if h.Name() != "cursor" {
		t.Fatalf("Name() = %q, want %q", h.Name(), "cursor")
	}
	caps := h.Capabilities()
	for _, c := range []Capability{CapPermissionCallback, CapStructuredOutput} {
		if !caps.Has(c) {
			t.Errorf("Capabilities() missing %v", c)
		}
	}
	for _, c := range []Capability{CapSessionResume, CapPartialStreaming, CapUserQuestion} {
		if caps.Has(c) {
			t.Errorf("Capabilities() advertises unimplemented capability %v", c)
		}
	}
}

func TestFor_CursorRoutes(t *testing.T) {
	h, err := For("cursor", "")
	if err != nil {
		t.Fatalf("For(cursor, ) error = %v", err)
	}
	if h.Name() != "cursor" {
		t.Errorf("For(cursor, ).Name() = %q, want %q", h.Name(), "cursor")
	}

	h, err = For("cursor", "acp")
	if err != nil {
		t.Fatalf("For(cursor, acp) error = %v", err)
	}
	if h.Name() != "cursor" {
		t.Errorf("For(cursor, acp).Name() = %q, want %q", h.Name(), "cursor")
	}
}
