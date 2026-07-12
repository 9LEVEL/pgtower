package db

import "strings"

// QuoteIdent quotes a SQL identifier (table, column, role, db name).
func QuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// QuoteQualified quotes schema.table.
func QuoteQualified(schema, name string) string {
	return QuoteIdent(schema) + "." + QuoteIdent(name)
}

// QuoteLiteral quotes a SQL string literal (doubles single quotes).
func QuoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
