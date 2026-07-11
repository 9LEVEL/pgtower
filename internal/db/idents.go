package db

import "strings"

// QuoteIdent quota um identificador SQL (nome de tabela, coluna, role, db).
func QuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// QuoteQualified quota schema.tabela.
func QuoteQualified(schema, name string) string {
	return QuoteIdent(schema) + "." + QuoteIdent(name)
}

// QuoteLiteral quota uma string literal SQL (dobra aspas simples).
func QuoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
