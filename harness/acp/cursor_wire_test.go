package acp

import (
	"io"
	"strings"
	"testing"
)

func TestCursorWireReaderRewritesOnlyKnownRequests(t *testing.T) {
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":9007199254740993,"method":"cursor/ask_question","params":{"text":"cursor/ask_question"}}`,
		`{"jsonrpc":"2.0","id":"id","method":"cursor/create_plan","params":{}}`,
		`{"jsonrpc":"2.0","method":"cursor/ask_question","params":{}}`,
		`{"jsonrpc":"2.0","id":9007199254740993,"result":{}}`,
	}, "\n") + "\n"
	out, err := io.ReadAll(newCursorWireReader(strings.NewReader(in)))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `"method":"_cursor/ask_question"`) || !strings.Contains(got, `"method":"_cursor/create_plan"`) {
		t.Fatalf("rewritten frames = %s", got)
	}
	if !strings.Contains(got, `"id":9007199254740993`) || !strings.Contains(got, `"text":"cursor/ask_question"`) {
		t.Fatalf("ID or params changed: %s", got)
	}
	if !strings.Contains(got, `"method":"cursor/ask_question","params":{}`) {
		t.Fatalf("notification changed: %s", got)
	}
}

func TestCursorWireReaderRejectsMalformedAndOversizedFrames(t *testing.T) {
	if _, err := io.ReadAll(newCursorWireReader(strings.NewReader("not-json\n"))); err == nil {
		t.Fatal("malformed frame did not fail")
	}
	big := strings.Repeat("x", maxCursorFrame+1) + "\n"
	if _, err := io.ReadAll(newCursorWireReader(strings.NewReader(big))); err == nil {
		t.Fatal("oversized frame did not fail")
	}
}
