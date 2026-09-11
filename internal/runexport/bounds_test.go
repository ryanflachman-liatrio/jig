package runexport

import (
	"bufio"
	"errors"
	"strings"
	"testing"
)

func TestBudgetSpendExceeded(t *testing.T) {
	b := newBudget(10)
	if err := b.spend(5); err != nil {
		t.Fatalf("spend(5) with budget 10 = %v, want nil", err)
	}
	if err := b.spend(6); !errors.Is(err, errBudgetExceeded) {
		t.Fatalf("spend(6) with 5 remaining = %v, want errBudgetExceeded", err)
	}
}

func TestBoundedReaderChargesBudget(t *testing.T) {
	b := newBudget(4)
	r := &boundedReader{r: strings.NewReader("hello world"), b: b}
	buf := make([]byte, 4)
	if _, err := r.Read(buf); err != nil {
		t.Fatalf("first read: %v", err)
	}
	if _, err := r.Read(buf); !errors.Is(err, errBudgetExceeded) {
		t.Fatalf("second read = %v, want errBudgetExceeded", err)
	}
}

func TestReadBoundedLineOversizedResyncsToNextLine(t *testing.T) {
	oversized := strings.Repeat("x", 50)
	data := oversized + "\nshort\n"
	br := bufio.NewReader(strings.NewReader(data))
	if _, err := readBoundedLine(br, 10); !errors.Is(err, errLineOversized) {
		t.Fatalf("first line = %v, want errLineOversized", err)
	}
	line, err := readBoundedLine(br, 10)
	if err != nil || string(line) != "short" {
		t.Fatalf("resynced line = %q, %v, want %q, nil", line, err, "short")
	}
}

func TestReadBoundedLineCleanEOF(t *testing.T) {
	br := bufio.NewReader(strings.NewReader("one\ntwo\n"))
	first, err := readBoundedLine(br, 100)
	if err != nil || string(first) != "one" {
		t.Fatalf("first = %q, %v", first, err)
	}
	second, err := readBoundedLine(br, 100)
	if err != nil || string(second) != "two" {
		t.Fatalf("second = %q, %v", second, err)
	}
	if _, err := readBoundedLine(br, 100); err == nil {
		t.Fatal("expected io.EOF at clean end of input")
	}
}
