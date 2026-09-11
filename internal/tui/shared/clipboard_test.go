package shared

import (
	"errors"
	"strings"
	"testing"
)

func TestClipboardEligibilityAndLimits(t *testing.T) {
	longRun := strings.Repeat("a", ClipboardMaxPayloadBytes)
	overRun := strings.Repeat("a", ClipboardMaxPayloadBytes+1)

	cases := []struct {
		name       string
		raw        string
		wantErr    error
		wantOutput string
	}{
		{name: "empty input", raw: "", wantErr: ErrClipboardEmpty},
		{name: "plain text", raw: "hello", wantOutput: "hello"},
		{name: "preserves tabs and CRLF", raw: "a\tb\r\nc\n", wantOutput: "a\tb\r\nc\n"},
		{name: "strips ansi color", raw: "\x1b[31mred\x1b[0m", wantOutput: "red"},
		{name: "strips OSC hyperlink", raw: "\x1b]8;;https://example.com\x07label\x1b]8;;\x07", wantOutput: "label"},
		{name: "strips bell", raw: "hi\x07there", wantOutput: "hithere"},
		{name: "rejects NUL", raw: "hello\x00world", wantErr: ErrClipboardBinary},
		{name: "rejects invalid utf8", raw: "hello\xff", wantErr: ErrClipboardInvalidText},
		{name: "sanitize-only becomes empty", raw: "\x1b[31m\x1b[0m\x07", wantErr: ErrClipboardEmpty},
		{name: "unicode preserved", raw: "café — 世界", wantOutput: "café — 世界"},
		{name: "exact limit", raw: longRun, wantOutput: longRun},
		{name: "oversized", raw: overRun, wantErr: ErrClipboardOversized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PrepareClipboardPayload(tc.raw)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("PrepareClipboardPayload err = %v, want %v", err, tc.wantErr)
				}
				if got != "" {
					t.Fatalf("PrepareClipboardPayload payload = %q, want empty on error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("PrepareClipboardPayload unexpected err = %v", err)
			}
			if got != tc.wantOutput {
				t.Fatalf("PrepareClipboardPayload = %q, want %q", got, tc.wantOutput)
			}
		})
	}
}

func TestFormatClipboardNotice(t *testing.T) {
	target := ClipboardTarget{Surface: ClipboardSurfaceRunID, Label: "20260101-000000-abc12345"}
	got := FormatClipboardNotice(target, 24, "")
	want := "Copy requested: run ID: 20260101-000000-abc12345 (24 bytes)"
	if got != want {
		t.Fatalf("FormatClipboardNotice = %q, want %q", got, want)
	}
	got = FormatClipboardNotice(target, 24, "3 records skipped")
	want = "Copy requested: run ID: 20260101-000000-abc12345 (24 bytes) · 3 records skipped"
	if got != want {
		t.Fatalf("FormatClipboardNotice with notes = %q, want %q", got, want)
	}
}

func TestFormatClipboardError(t *testing.T) {
	target := ClipboardTarget{Surface: ClipboardSurfaceMonitorFile, Label: "output.json"}
	got := FormatClipboardError(target, ErrClipboardOversized)
	want := "Copy skipped (output file: output.json): " + ErrClipboardOversized.Error()
	if got != want {
		t.Fatalf("FormatClipboardError = %q, want %q", got, want)
	}
}

func TestSanitizeClipboardTextTruncatedEscape(t *testing.T) {
	// A partially-written CSI at end-of-string is dropped without a panic.
	got, err := PrepareClipboardPayload("hi\x1b[31")
	if err != nil {
		t.Fatalf("PrepareClipboardPayload err = %v", err)
	}
	if got != "hi" {
		t.Fatalf("PrepareClipboardPayload = %q, want %q", got, "hi")
	}
}
