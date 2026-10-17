package postgresql

import (
	"errors"
	"regexp"
	"strings"
)

// This admission check rejects known unsupported statements. It is deliberately
// not a complete PostgreSQL parser or a security sandbox for migration authors.
// Database privileges remain the boundary for the SQL they submit.
var unsupportedMigrationSQL = regexp.MustCompile(`(?i)(\b(?:CREATE|DROP) (?:UNIQUE )?INDEX CONCURRENTLY\b|\bREINDEX\b|\bVACUUM\b|\b(?:CREATE|DROP) (?:DATABASE|TABLESPACE|SUBSCRIPTION)\b|\b(?:CREATE|ALTER) (?:OR REPLACE )?(?:FUNCTION|PROCEDURE)\b|\bALTER SYSTEM\b|\bSET_CONFIG\b|(?:^|;)\s*(?:BEGIN|START TRANSACTION|COMMIT|END|ROLLBACK|ABORT|PREPARE TRANSACTION|SET|RESET|DISCARD|LOAD|DO|CALL)\b)`)

func ValidateMigrationSQL(sql []byte) error {
	tokens, err := migrationTokens(string(sql))
	if err != nil || unsupportedMigrationSQL.MatchString(tokens) {
		return errors.New("PostgreSQL migration contains unsupported nontransactional, transaction-control, session-control or routine SQL; use ordinary transactional SQL without per-script settings")
	}
	return nil
}

func migrationTokens(sql string) (string, error) {
	var out strings.Builder
	for i := 0; i < len(sql); {
		switch {
		case (sql[i] == 'u' || sql[i] == 'U') && strings.HasPrefix(sql[i+1:], `&"`):
			return "", errors.New("Unicode-escaped identifiers are outside the qualified migration policy")
		case strings.HasPrefix(sql[i:], "--"):
			out.WriteByte(' ')
			end := strings.IndexByte(sql[i:], '\n')
			if end < 0 {
				i = len(sql)
			} else {
				i += end + 1
			}
		case strings.HasPrefix(sql[i:], "/*"):
			out.WriteByte(' ')
			depth := 1
			i += 2
			for i < len(sql) && depth > 0 {
				if strings.HasPrefix(sql[i:], "/*") {
					depth++
					i += 2
				} else if strings.HasPrefix(sql[i:], "*/") {
					depth--
					i += 2
				} else {
					i++
				}
			}
			if depth != 0 {
				return "", errors.New("unterminated comment")
			}
		case sql[i] == '\'' || sql[i] == '"':
			quote := sql[i]
			start := i + 1
			escaped := quote == '\'' && i > 0 && (sql[i-1] == 'e' || sql[i-1] == 'E') && (i < 2 || !migrationWord(sql[i-2]))
			i++
			closed := false
			for i < len(sql) {
				if sql[i] == quote {
					i++
					if i < len(sql) && sql[i] == quote {
						i++
						continue
					}
					closed = true
					break
				}
				// Only E strings use backslash escapes under PostgreSQL's default
				// standard_conforming_strings policy. Identifier quotes never do.
				if escaped && sql[i] == '\\' && i+1 < len(sql) {
					i += 2
				} else {
					i++
				}
			}
			if !closed {
				return "", errors.New("unterminated quoted value")
			}
			if quote == '"' && strings.EqualFold(sql[start:i-1], "set_config") {
				out.WriteString(" set_config ")
			} else {
				out.WriteString(" value ")
			}
		default:
			c := sql[i]
			if c == ';' {
				out.WriteString(";")
			} else if migrationWord(c) {
				out.WriteByte(c)
			} else {
				out.WriteByte(' ')
			}
			i++
		}
		// Comments and whitespace must never splice keyword tokens together.
		if i < len(sql) && (sql[i] == '\n' || sql[i] == '\r' || sql[i] == '\t' || sql[i] == ' ') {
			out.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(out.String()), " "), nil
}

func migrationWord(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}
