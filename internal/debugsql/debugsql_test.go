package debugsql

import "testing"

func TestValidateAllowsReadOnlyQueries(t *testing.T) {
	queries := []string{
		`SELECT * FROM "keys" WHERE key_hash = 'example'`,
		"WITH recent AS (SELECT * FROM requests) SELECT * FROM recent",
		"EXPLAIN ANALYZE SELECT * FROM requests WHERE timestamp > NOW() - INTERVAL '1 day'",
		"SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema()",
		"SELECT relname FROM pg_catalog.pg_class WHERE relkind = 'r'",
		"/* investigate incident */ SELECT 1 -- safe comment\n",
	}
	for _, query := range queries {
		if err := Validate(query); err != nil {
			t.Errorf("Validate(%q) returned %v", query, err)
		}
	}
}

func TestValidateRejectsStateChanges(t *testing.T) {
	queries := []string{
		"DELETE FROM requests",
		"SELECT * FROM requests; DROP TABLE requests",
		"SELECT * INTO temporary_table FROM requests",
		"SELECT * FROM requests FOR UPDATE",
		"WITH deleted AS (DELETE FROM requests RETURNING *) SELECT * FROM deleted",
		"SET default_transaction_read_only = off",
		"COPY requests TO '/tmp/requests.csv'",
	}
	for _, query := range queries {
		if err := Validate(query); err == nil {
			t.Errorf("Validate(%q) succeeded", query)
		}
	}
}
