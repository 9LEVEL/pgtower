package db

import (
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
