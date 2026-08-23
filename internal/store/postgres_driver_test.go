package store

import "testing"

func TestPostgresQuery(t *testing.T) {
	query := postgresQuery("SELECT `rank` FROM models WHERE id = ? AND note = '?' AND type = ?")
	want := `SELECT "rank" FROM models WHERE id = $1 AND note = '?' AND type = $2`
	if query != want {
		t.Fatalf("postgres query = %q, want %q", query, want)
	}
}
