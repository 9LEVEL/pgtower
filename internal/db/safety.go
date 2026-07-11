package db

import (
	"regexp"
	"strings"
)

// Danger classifica o risco de um statement SQL para o query runner.
type Danger int

const (
	// Safe: somente leitura (SELECT/WITH...SELECT/EXPLAIN/SHOW/TABLE/VALUES).
	Safe Danger = iota
	// Write: modifica dados ou schema (INSERT/UPDATE/DELETE/DDL). Confirmação.
	Write
	// Critical: potencial perda de dados em massa (DROP DATABASE/TABLE,
	// TRUNCATE, DELETE/UPDATE sem WHERE). Confirmação enfática.
	Critical
)

func (d Danger) String() string {
	switch d {
	case Write:
		return "WRITE"
	case Critical:
		return "CRITICAL"
	default:
		return "SAFE"
	}
}

var (
	reComment    = regexp.MustCompile(`(?s)/\*.*?\*/`)
	reLineComose = regexp.MustCompile(`(?m)--.*$`)
	reWS         = regexp.MustCompile(`\s+`)

	reReadOnly = regexp.MustCompile(`^(select|with|explain|show|table|values|fetch)\b`)
	reCritical = regexp.MustCompile(`^(drop\s+database|drop\s+table|drop\s+schema|truncate|drop\s+role|drop\s+user|drop\s+tablespace)\b`)
	reWrite    = regexp.MustCompile(`^(insert|update|delete|alter|create|drop|grant|revoke|comment|reindex|vacuum|analyze|cluster|refresh|call|do|copy|set|reset|begin|commit|rollback|savepoint|lock|merge|import)\b`)
	reHasWhere = regexp.MustCompile(`\bwhere\b`)
)

// Classify determina o nível de risco de um statement. Faz uma análise
// léxica simples (não é um parser SQL) — na dúvida, escala o risco.
func Classify(sql string) Danger {
	norm := normalize(sql)
	if norm == "" {
		return Safe
	}

	switch {
	case reCritical.MatchString(norm):
		return Critical
	case strings.HasPrefix(norm, "delete") && !reHasWhere.MatchString(norm):
		return Critical
	case strings.HasPrefix(norm, "update") && !reHasWhere.MatchString(norm):
		return Critical
	case reReadOnly.MatchString(norm):
		// Um WITH pode conter DML (writable CTE). Se aparecer, escala.
		if strings.HasPrefix(norm, "with") && containsDML(norm) {
			return Write
		}
		return Safe
	case reWrite.MatchString(norm):
		return Write
	default:
		// Desconhecido: trata como escrita por segurança.
		return Write
	}
}

func normalize(sql string) string {
	s := reComment.ReplaceAllString(sql, " ")
	s = reLineComose.ReplaceAllString(s, " ")
	s = reWS.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "(")
	return strings.ToLower(strings.TrimSpace(s))
}

func containsDML(norm string) bool {
	for _, kw := range []string{" insert ", " update ", " delete ", " merge "} {
		if strings.Contains(norm, kw) {
			return true
		}
	}
	return false
}
