// Package debugsql validates SQL for the internal read-only debug CLI.
package debugsql

import (
	"fmt"
	"strings"
	"unicode"
)

// Validate permits one non-mutating PostgreSQL statement. It intentionally
// permits reading any application, information_schema, or pg_catalog table.
func Validate(query string) error {
	tokens, err := tokenize(query)
	if err != nil {
		return err
	}
	if len(tokens) == 0 {
		return fmt.Errorf("SQL query is empty")
	}

	statement := tokens[0]
	switch statement {
	case "SELECT", "WITH", "EXPLAIN":
	default:
		return fmt.Errorf("%s is not allowed; postgres-debug only runs read-only statements", statement)
	}

	for index, token := range tokens {
		switch token {
		case "CREATE", "COPY":
			return fmt.Errorf("%s is not allowed; postgres-debug must not change database state", token)
		case "ANALYZE":
			if statement == "EXPLAIN" && index == 1 {
				continue
			}
			return fmt.Errorf("%s is not allowed; postgres-debug must not change database state", token)
		case "ALTER", "BEGIN", "CALL", "CLUSTER", "COMMENT", "COMMIT", "DEALLOCATE", "DELETE", "DISCARD", "DO", "DROP", "EXECUTE", "GRANT", "INSERT", "LISTEN", "LOAD", "LOCK", "MOVE", "NOTIFY", "PREPARE", "REFRESH", "REASSIGN", "REINDEX", "RELEASE", "RESET", "REVOKE", "ROLLBACK", "SAVEPOINT", "SECURITY", "SET", "START", "TRANSACTION", "TRUNCATE", "UNLISTEN", "UPDATE", "VACUUM":
			return fmt.Errorf("%s is not allowed; postgres-debug must not change database state", token)
		case "INTO":
			return fmt.Errorf("INTO is not allowed; postgres-debug must not write query output")
		}
	}
	return nil
}

func tokenize(query string) ([]string, error) {
	var tokens []string
	for i := 0; i < len(query); {
		if unicode.IsSpace(rune(query[i])) {
			i++
			continue
		}
		if query[i] == ';' {
			if strings.TrimSpace(query[i+1:]) != "" {
				return nil, fmt.Errorf("multiple SQL statements are not allowed")
			}
			break
		}
		if query[i] == '#' || (query[i] == '-' && i+2 < len(query) && query[i+1] == '-' && unicode.IsSpace(rune(query[i+2]))) {
			for i < len(query) && query[i] != '\n' {
				i++
			}
			continue
		}
		if query[i] == '/' && i+1 < len(query) && query[i+1] == '*' {
			end := strings.Index(query[i+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("unterminated SQL comment")
			}
			i += end + 4
			continue
		}
		if query[i] == '\'' || query[i] == '"' || query[i] == '`' {
			i, _ = skipQuoted(query, i)
			if i < 0 {
				return nil, fmt.Errorf("unterminated quoted value")
			}
			continue
		}
		if isIdentifierChar(query[i]) {
			start := i
			for i < len(query) && isIdentifierChar(query[i]) {
				i++
			}
			tokens = append(tokens, strings.ToUpper(query[start:i]))
			continue
		}
		i++
	}
	return tokens, nil
}

func skipQuoted(query string, start int) (int, bool) {
	quote := query[start]
	for i := start + 1; i < len(query); i++ {
		if query[i] == '\\' && quote != '`' {
			i++
			continue
		}
		if query[i] == quote {
			if i+1 < len(query) && query[i+1] == quote {
				i++
				continue
			}
			return i + 1, true
		}
	}
	return -1, false
}

func isIdentifierChar(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
