package db

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// Cells are built from PostgreSQL's text output: one line, NULL as ∅, bytea
// abbreviated, and no control character left to reach the terminal.
func TestFormatValue(t *testing.T) {
	long := []byte(`\x000102030405060708090a0b0c0d0e0f10`) // 17 bytes
	cases := []struct {
		name string
		in   []byte
		oid  uint32
		want string
	}{
		{"NULL", nil, pgtype.TextOID, "∅"},
		{"empty string is not NULL", []byte{}, pgtype.TextOID, ""},
		{"line breaks and tabs", []byte("a\r\nb\nc\td\re"), pgtype.TextOID, "a b c d e"},
		{"escape sequence", []byte("x\x1b]0;hi\x07y"), pgtype.TextOID, "x\uFFFD]0;hi\uFFFDy"},
		{"short bytea in full", []byte(`\x000102`), pgtype.ByteaOID, `\x000102`},
		{"long bytea abbreviated", long, pgtype.ByteaOID, `\x000102030405060708090a0b0c0d0e0f…`},
		{"long text kept", long, pgtype.TextOID, string(long)},
	}
	for _, c := range cases {
		if got := formatValue(c.in, c.oid); got != c.want {
			t.Errorf("%s: formatValue(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// The full text (what y copies) keeps what the one-line cell drops; a large
// value that would be held twice is not kept.
func TestCellTextFull(t *testing.T) {
	big := []byte(`\x` + strings.Repeat("ab", maxCopyCell))
	cases := []struct {
		name       string
		in         []byte
		oid        uint32
		disp, full string
	}{
		{"NULL", nil, pgtype.TextOID, "∅", ""},
		{"line breaks kept", []byte("a\tb\nc"), pgtype.TextOID, "a b c", "a\tb\nc"},
		{"bytea in full", []byte(`\x000102030405060708090a0b0c0d0e0f10`), pgtype.ByteaOID,
			`\x000102030405060708090a0b0c0d0e0f…`, `\x000102030405060708090a0b0c0d0e0f10`},
		{"large bytea not kept", big, pgtype.ByteaOID, string(big[:byteaPreview]) + "…", ""},
		{"large plain text shared, so kept", []byte(strings.Repeat("x", maxCopyCell+1)), pgtype.TextOID,
			strings.Repeat("x", maxCopyCell+1), strings.Repeat("x", maxCopyCell+1)},
	}
	for _, c := range cases {
		disp, full := cellText(c.in, c.oid)
		if disp != c.disp || full != c.full {
			t.Errorf("%s: cellText = (%.40q, %.40q), want (%.40q, %.40q)", c.name, disp, full, c.disp, c.full)
		}
	}
}
