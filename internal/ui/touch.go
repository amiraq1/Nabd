package ui

import (
	"bytes"
	"io"
	"time"
	"unicode/utf8"

	"github.com/muesli/cancelreader"
	"golang.org/x/sys/unix"
)

const (
	// maxSGRBuffer bounds the maximum bytes buffered while examining an SGR sequence.
	// Standard SGR mouse reports are well under 32 bytes; 64 bytes safely accommodates
	// high-coordinate parameters without risking unbounded memory growth.
	maxSGRBuffer = 64

	// defaultEscTimeout is the maximum duration to wait for subsequent escape sequence
	// bytes on a file descriptor before treating a solitary ESC byte (0x1b) as a standalone
	// escape keypress. 25ms matches standard terminal escape resolution policies.
	defaultEscTimeout = 25 * time.Millisecond
)

// SGRNormalizer wraps an input stream, converting localized Arabic-Indic digits
// within recognized SGR mouse reports (ESC[<...[Mm]) to ASCII digits.
// All ordinary text, Arabic digits in messages, bracketed paste, and keyboard
// shortcuts are passed through byte-for-byte unchanged.
type SGRNormalizer struct {
	r          io.Reader
	pending    []byte
	out        []byte
	escTimeout time.Duration
}

// sgrFileNormalizer wraps SGRNormalizer and forwards file descriptor methods
// ONLY when the wrapped reader is an actual cancelreader.File (such as os.Stdin or a PTY).
// This preserves raw terminal mode detection (term.File) and cancellable epoll reading.
type sgrFileNormalizer struct {
	*SGRNormalizer
	file cancelreader.File
}

func (n *sgrFileNormalizer) Fd() uintptr {
	return n.file.Fd()
}

func (n *sgrFileNormalizer) Name() string {
	return n.file.Name()
}

func (n *sgrFileNormalizer) Close() error {
	return n.file.Close()
}

func (n *sgrFileNormalizer) Write(p []byte) (int, error) {
	return n.file.Write(p)
}

// NewSGRNormalizer creates a new SGRNormalizer wrapping the provided reader.
// If r is an OS file resource (e.g. os.Stdin or PTY), it preserves cancelreader.File
// and term.File interfaces for Bubble Tea raw mode and cancellation.
func NewSGRNormalizer(r io.Reader) io.Reader {
	base := &SGRNormalizer{
		r:          r,
		escTimeout: defaultEscTimeout,
	}
	if f, ok := r.(cancelreader.File); ok {
		return &sgrFileNormalizer{
			SGRNormalizer: base,
			file:          f,
		}
	}
	return base
}

func getFd(r io.Reader) (uintptr, bool) {
	if f, ok := r.(interface{ Fd() uintptr }); ok {
		return f.Fd(), true
	}
	return 0, false
}

// Read implements io.Reader. It processes input bytes, normalizes SGR mouse reports,
// and returns normalized bytes to the caller.
func (n *SGRNormalizer) Read(p []byte) (int, error) {
	for len(n.out) == 0 {
		// If an escape prefix is pending and the reader has an associated OS file descriptor,
		// poll with a small timeout to distinguish between a standalone ESC keypress and
		// an incoming multi-byte escape sequence without hanging indefinitely.
		if len(n.pending) > 0 {
			if fd, ok := getFd(n.r); ok {
				pollFds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
				timeoutMs := int(n.escTimeout.Milliseconds())
				if timeoutMs <= 0 {
					timeoutMs = 25
				}
				nReady, _ := unix.Poll(pollFds, timeoutMs)
				if nReady <= 0 {
					// Timeout or error: no subsequent bytes arrived for this prefix.
					// Flush pending bytes as raw literal input.
					n.out = append(n.out, n.pending...)
					n.pending = nil
					break
				}
			}
		}

		var buf [256]byte
		m, err := n.r.Read(buf[:])
		if m > 0 {
			n.feed(buf[:m])
		}
		if err != nil {
			// On EOF or error, flush any remaining pending bytes unmodified.
			if len(n.pending) > 0 {
				n.out = append(n.out, n.pending...)
				n.pending = nil
			}
			if len(n.out) > 0 {
				return n.drain(p), nil
			}
			return 0, err
		}
	}
	return n.drain(p), nil
}

func (n *SGRNormalizer) drain(p []byte) int {
	copied := copy(p, n.out)
	n.out = n.out[copied:]
	return copied
}

func (n *SGRNormalizer) feed(data []byte) {
	for i := 0; i < len(data); i++ {
		b := data[i]

		if len(n.pending) == 0 {
			if b == 0x1b { // ESC
				n.pending = append(n.pending, b)
			} else {
				n.out = append(n.out, b)
			}
			continue
		}

		n.pending = append(n.pending, b)

		// Check prefix progress.
		switch len(n.pending) {
		case 2:
			if n.pending[1] != '[' {
				// Not CSI: flush and reset
				n.out = append(n.out, n.pending...)
				n.pending = nil
			}
			continue
		case 3:
			if n.pending[2] != '<' {
				// Not SGR mouse sequence: flush and reset
				n.out = append(n.out, n.pending...)
				n.pending = nil
			}
			continue
		}

		// Sequence completed.
		if b == 'M' || b == 'm' {
			normalized, ok := normalizeSGR(n.pending)
			if ok {
				n.out = append(n.out, normalized...)
			} else {
				n.out = append(n.out, n.pending...)
			}
			n.pending = nil
			continue
		}

		// Enforce bounded buffer length.
		if len(n.pending) > maxSGRBuffer {
			n.out = append(n.out, n.pending...)
			n.pending = nil
			continue
		}

		// Valid characters within SGR parameters:
		// Semicolon
		if b == ';' {
			continue
		}
		// ASCII digits '0'-'9'
		if b >= '0' && b <= '9' {
			continue
		}
		// First byte of 2-byte UTF-8 Arabic-Indic digit (0xd9)
		if b == 0xd9 {
			continue
		}
		// Second byte of 2-byte UTF-8 Arabic-Indic digit (0xa0-0xa9)
		if len(n.pending) >= 2 && n.pending[len(n.pending)-2] == 0xd9 {
			if b >= 0xa0 && b <= 0xa9 {
				continue
			}
		}

		// Invalid character inside SGR parameter: flush as raw bytes.
		n.out = append(n.out, n.pending...)
		n.pending = nil
	}
}

// normalizeSGR validates an SGR mouse report (ESC[<Cb;Cx;Cy[Mm]) and converts
// any Arabic-Indic digits within its numeric parameters to ASCII digits.
func normalizeSGR(seq []byte) ([]byte, bool) {
	if len(seq) < 4 || seq[0] != 0x1b || seq[1] != '[' || seq[2] != '<' {
		return nil, false
	}
	term := seq[len(seq)-1]
	if term != 'M' && term != 'm' {
		return nil, false
	}

	body := seq[3 : len(seq)-1]
	var parts [][]byte
	start := 0
	for i := 0; i < len(body); i++ {
		if body[i] == ';' {
			parts = append(parts, body[start:i])
			start = i + 1
		}
	}
	parts = append(parts, body[start:])
	if len(parts) != 3 {
		return nil, false
	}

	var res bytes.Buffer
	res.WriteString("\x1b[<")

	for partIdx, part := range parts {
		if len(part) == 0 {
			return nil, false
		}
		i := 0
		for i < len(part) {
			r, sz := utf8.DecodeRune(part[i:])
			if r >= '0' && r <= '9' {
				res.WriteRune(r)
			} else if r >= 0x0660 && r <= 0x0669 { // Arabic-Indic digits ٠-٩
				res.WriteByte(byte('0' + (r - 0x0660)))
			} else {
				return nil, false
			}
			i += sz
		}
		if partIdx < 2 {
			res.WriteByte(';')
		}
	}

	res.WriteByte(term)
	return res.Bytes(), true
}
