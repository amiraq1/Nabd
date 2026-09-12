package tools

import (
	"errors"
	"io"
	"os"
	"testing"
)

func TestLimitedReaderReturnsCanonicalEOFAtLimit(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "limited-reader")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := &limitedReader{f: f, left: 0}
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("error = %v, want io.EOF", err)
	}
}
