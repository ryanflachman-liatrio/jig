package harness

import "testing"

func TestCodexHarnessCapabilities(t *testing.T) {
	h := NewCodexHarness()
	if h.Name() != "codex" {
		t.Fatalf("Name() = %q, want codex", h.Name())
	}
	caps := h.Capabilities()
	for _, c := range []Capability{CapPermissionCallback, CapSessionResume, CapStructuredOutput, CapPartialStreaming} {
		if !caps.Has(c) {
			t.Errorf("Capabilities() missing %v", c)
		}
	}
	for _, c := range []Capability{CapUserQuestion} {
		if caps.Has(c) {
			t.Errorf("Capabilities() advertises unsupported capability %v", c)
		}
	}
}
