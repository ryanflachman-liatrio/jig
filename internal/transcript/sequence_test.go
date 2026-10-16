package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLatestSeqAndTailIgnoreIncompleteAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if seq, err := r.LatestSeq(); err != nil || seq != 0 {
		t.Fatalf("missing: %d, %v", seq, err)
	}
	// Even a syntactically valid record is not finalized until its newline.
	data := []byte("{\"seq\":1,\"role\":\"assistant\"}\n{\"seq\":2,\"role\":\"assistant\"}")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if seq, err := r.LatestSeq(); err != nil || seq != 1 {
		t.Fatalf("partial: %d, %v", seq, err)
	}
	page, err := r.TailPage(10)
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Seq != 1 {
		t.Fatalf("partial page: %+v, %v", page, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if seq, err := r.LatestSeq(); err != nil || seq != 2 {
		t.Fatalf("finalized: %d, %v", seq, err)
	}
	if err := os.WriteFile(path, append(data, []byte("\nnot-json\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if seq, err := r.LatestSeq(); err != nil || seq != 2 {
		t.Fatalf("damaged tail: %d, %v", seq, err)
	}
}

func TestLatestSeqReportsReadFailure(t *testing.T) {
	r, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.LatestSeq(); err == nil {
		t.Fatal("directory read silently succeeded")
	}
}
