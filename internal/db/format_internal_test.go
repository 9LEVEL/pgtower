package db

import "testing"

// Grid cells are one line and abbreviated; the raw text (what gets copied) is
// not.
func TestFormatVsRawValue(t *testing.T) {
	long := []byte("0123456789abcdefXYZ") // 19 bytes: longer than the preview
	cases := []struct {
		in        any
		disp, raw string
	}{
		{nil, "∅", ""},
		{"a\tb\nc", "a b c", "a\tb\nc"},
		{long, "\\x" + hexPreview(long), "\\x3031323334353637383961626364656658595a"},
		{42, "42", "42"},
	}
	for _, c := range cases {
		if got := formatValue(c.in); got != c.disp {
			t.Errorf("formatValue(%#v) = %q, want %q", c.in, got, c.disp)
		}
		if got := rawValue(c.in); got != c.raw {
			t.Errorf("rawValue(%#v) = %q, want %q", c.in, got, c.raw)
		}
	}
}
