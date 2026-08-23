package main

import (
	"flag"
	"log"

	"szx-gateway/internal/store"
)

func main() {
	sqlitePath := flag.String("sqlite", "gateway.db", "Path to source SQLite database")
	postgresDSN := flag.String("postgres", "", "Target PostgreSQL DSN")
	maxOpenConns := flag.Int("max-open-conns", 10, "Maximum PostgreSQL open connections")
	maxIdleConns := flag.Int("max-idle-conns", 5, "Maximum PostgreSQL idle connections")
	flag.Parse()
	if *postgresDSN == "" {
		log.Fatal("-postgres is required")
	}
	if err := store.MigrateSQLiteToPostgres(*sqlitePath, *postgresDSN, *maxOpenConns, *maxIdleConns); err != nil {
		log.Fatal(err)
	}
}
