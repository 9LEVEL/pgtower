package ui

import (
	"strings"
	"unicode/utf8"
)

// safeView neutralizes, in a frame about to reach the terminal, every control
// character the UI does not draw with itself. The UI only emits line breaks
// and SGR sequences (colors, bold …); anything else came from the data — a
// query in pg_stat_activity, an application_name, a table or column name, a
// server error message — and must not drive the terminal (move the cursor,
// set the window title or clipboard …). Each such character becomes U+FFFD;
// a tab or carriage return becomes a space.
func safeView(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\n':
			b.WriteByte(c)
			i++
		case c == '\x1b':
			if n := sgrLen(s[i:]); n > 0 {
				b.WriteString(s[i : i+n])
				i += n
				continue
			}
			b.WriteRune(utf8.RuneError)
			i++
		case c == '\t' || c == '\r':
			b.WriteByte(' ')
			i++
		case c < 0x20 || c == 0x7f:
			b.WriteRune(utf8.RuneError)
			i++
		case c < utf8.RuneSelf:
			b.WriteByte(c)
			i++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError || r <= 0x9f { // invalid byte or C1 control (e.g. 8-bit CSI)
				b.WriteRune(utf8.RuneError)
			} else {
				b.WriteString(s[i : i+size])
			}
			i += size
		}
	}
	return b.String()
}

// sgrLen is the length of the SGR sequence (ESC [ params m) s starts with,
// or 0 when s starts with any other escape sequence.
func sgrLen(s string) int {
	if len(s) < 3 || s[1] != '[' {
		return 0
	}
	for i := 2; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9', c == ';', c == ':':
		case c == 'm':
			return i + 1
		default:
			return 0
		}
	}
	return 0
}
