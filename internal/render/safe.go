package render

import (
	"io"
	"unicode/utf8"
)

// SafeWriter keeps text from the server from driving the terminal: it
// replaces C0 and C1 control characters, DEL, bidi controls and invalid UTF-8 with U+FFFD
// and drops carriage returns, so escape sequences (CSI, OSC and the like)
// cannot clear the screen, set the window title or overwrite earlier lines.
// Newlines and tabs pass through. A UTF-8 sequence split between two Write
// calls is held back until it is complete; Flush writes out what is left.
type SafeWriter struct {
	w       io.Writer
	enabled func() bool
	pending []byte
}

// NewSafeWriter wraps w. enabled is consulted on every Write; nil means
// always on, and while it returns false bytes pass through unchanged.
func NewSafeWriter(w io.Writer, enabled func() bool) *SafeWriter {
	return &SafeWriter{w: w, enabled: enabled}
}

func (s *SafeWriter) Write(p []byte) (int, error) {
	if s.enabled != nil && !s.enabled() {
		if err := s.Flush(); err != nil {
			return 0, err
		}
		return s.w.Write(p)
	}
	data := p
	if len(s.pending) > 0 {
		data = append(s.pending, p...)
		s.pending = nil
	}
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size <= 1 {
			if !utf8.FullRune(data[i:]) {
				s.pending = append([]byte(nil), data[i:]...)
				break
			}
			out = utf8.AppendRune(out, utf8.RuneError)
			i++
			continue
		}
		switch {
		case r == '\n' || r == '\t':
			out = append(out, byte(r))
		case r == '\r':
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || isBidi(r):
			out = utf8.AppendRune(out, utf8.RuneError)
		default:
			out = append(out, data[i:i+size]...)
		}
		i += size
	}
	if _, err := s.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

// isBidi reports direction-control characters, which can make a URL or a
// name display differently from its bytes. ZWJ, ZWNJ and variation
// selectors are not among them: emoji sequences need those.
func isBidi(r rune) bool {
	return r == 0x061c || r == 0x200e || r == 0x200f ||
		(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

// Flush writes a held-back incomplete UTF-8 sequence as U+FFFD.
func (s *SafeWriter) Flush() error {
	if len(s.pending) == 0 {
		return nil
	}
	s.pending = nil
	_, err := s.w.Write([]byte(string(utf8.RuneError)))
	return err
}
