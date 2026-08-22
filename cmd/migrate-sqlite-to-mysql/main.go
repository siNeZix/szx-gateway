package main

import (
	"flag"
	"log"

	"szx-gateway/internal/store"
)

func main() {
	sqlitePath := flag.String("sqlite", "gateway.db", "Path to source SQLite database")
	mysqlDSN := flag.String("mysql", "", "Target MySQL DSN")
	maxOpenConns := flag.Int("db-max-open-conns", 10, "Maximum open MySQL connections")
	maxIdleConns := flag.Int("db-max-idle-conns", 5, "Maximum idle MySQL connections")
	flag.Parse()
	if *mysqlDSN == "" {
		log.Fatal("-mysql is required")
	}
	if err := store.MigrateSQLiteToMySQL(*sqlitePath, *mysqlDSN, *maxOpenConns, *maxIdleConns); err != nil {
		log.Fatalf("SQLite to MySQL migration failed: %v", err)
	}
	log.Println("SQLite data migrated to MySQL")
}
