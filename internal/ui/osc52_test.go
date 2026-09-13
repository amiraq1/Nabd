package ui

import (
	"errors"
	"strings"
	"testing"
)

func TestOSC52EncodingIsDeterministic(t *testing.T) {
	input := "Hello, World! مرحبا بالعالم 12345"
	want, err := encodeOSC52(input, 1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i := 0; i < 50; i++ {
		got, err := encodeOSC52(input, 1024)
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
		if got != want {
			t.Fatalf("iteration %d: got %q, want %q", i, got, want)
		}
	}
}

func TestOSC52RejectsOversizedPayload(t *testing.T) {
	input := strings.Repeat("A", 100)
	maxBytes := 50
	_, err := encodeOSC52(input, maxBytes)
	if err == nil {
		t.Fatal("expected error for oversized payload, got nil")
	}
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
	}
}

func TestOSC52ContainsNoRawControlCharacters(t *testing.T) {
	// Raw text with control characters, tabs, newlines, NUL bytes, ANSI escapes
	raw := "line1\n\x00\x1b[31mred\x07\x08\tline2"
	encoded, err := encodeOSC52(raw, 1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	const prefix = "\x1b]52;c;"
	const suffix = "\x1b\\"
	if !strings.HasPrefix(encoded, prefix) {
		t.Fatalf("missing OSC 52 prefix: %q", encoded)
	}
	if !strings.HasSuffix(encoded, suffix) {
		t.Fatalf("missing OSC 52 suffix: %q", encoded)
	}

	// The base64 body itself must contain exclusively standard ASCII base64 characters
	body := encoded[len(prefix) : len(encoded)-len(suffix)]
	for i, b := range []byte(body) {
		if b < 0x20 || b >= 0x7f {
			t.Fatalf("byte %d (%#x) in payload is a control or non-ASCII character", i, b)
		}
	}
}

func TestOSC52EmptyInputDoesNothing(t *testing.T) {
	got, err := encodeOSC52("", 1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}
