package ui

import (
	"encoding/base64"
	"errors"
	"fmt"
)

// ErrPayloadTooLarge is returned when clipboard text exceeds the bounded size ceiling.
var ErrPayloadTooLarge = errors.New("clipboard payload exceeds maximum size limit")

// defaultMaxCopyBytes is the default bound for clipboard payloads (64 KiB).
const defaultMaxCopyBytes = 64 * 1024

// encodeOSC52 formats text into an OSC 52 clipboard escape sequence using base64.
// It is a pure, side-effect-free function that never writes to the terminal directly.
// If text is empty, it returns ("", nil) without generating an escape sequence.
// If len(text) exceeds maxBytes (when maxBytes > 0), it returns ErrPayloadTooLarge.
func encodeOSC52(text string, maxBytes int) (string, error) {
	if text == "" {
		return "", nil
	}
	if maxBytes > 0 && len(text) > maxBytes {
		return "", fmt.Errorf("%w: %d > %d bytes", ErrPayloadTooLarge, len(text), maxBytes)
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(text))
	return fmt.Sprintf("\x1b]52;c;%s\x1b\\", b64), nil
}
