package store

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"
)

// MigrateSQLiteToMySQL replaces all gateway data with a SQLite snapshot.
// Each table is copied in one transaction, so interrupted runs are safe to restart.
func MigrateSQLiteToMySQL(sqlitePath, mysqlDSN string, maxOpenConns, maxIdleConns int) error {
	source, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		return fmt.Errorf("open sqlite database: %w", err)
	}
	defer source.Close()

	destination, err := Open("mysql", "", mysqlDSN, maxOpenConns, maxIdleConns)
	if err != nil {
		return err
	}
	defer destination.Close()

	for _, table := range []string{
		"keys", "requests", "rate_limits_log", "models_cache", "free_models_cache",
		"aihubmix_free_models_cache", "google_free_models_cache", "model_usage", "proxies",
		"proxy_settings", "proxy_logs", "model_check_configs", "model_check_results",
	} {
		started := time.Now()
		copied, err := copyTable(source, destination.db, table)
		if err != nil {
			return err
		}
		log.Printf("%s: %d rows copied in %s", table, copied, time.Since(started).Round(time.Millisecond))
	}
	return nil
}

func copyTable(source, destination *sql.DB, table string) (int64, error) {
	quotedTable := "`" + strings.ReplaceAll(table, "`", "``") + "`"
	rows, err := source.Query("SELECT * FROM " + quotedTable)
	if err != nil {
		return 0, fmt.Errorf("read sqlite table %s: %w", table, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	if len(columns) == 0 {
		return 0, nil
	}
	quoted := make([]string, len(columns))
	placeholders := make([]string, len(columns))
	for i, column := range columns {
		quoted[i] = "`" + strings.ReplaceAll(column, "`", "``") + "`"
		placeholders[i] = "?"
	}
	query := "INSERT INTO " + quotedTable + " (" + strings.Join(quoted, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	tx, err := destination.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM " + quotedTable); err != nil {
		return 0, fmt.Errorf("clear MySQL table %s: %w", table, err)
	}
	statement, err := tx.Prepare(query)
	if err != nil {
		return 0, fmt.Errorf("prepare MySQL insert for %s: %w", table, err)
	}
	defer statement.Close()
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	var copied int64
	for rows.Next() {
		if err := rows.Scan(pointers...); err != nil {
			return 0, fmt.Errorf("read sqlite row from %s: %w", table, err)
		}
		if _, err := statement.Exec(values...); err != nil {
			return 0, fmt.Errorf("copy row into %s: %w", table, err)
		}
		copied++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit %s: %w", table, err)
	}
	return copied, nil
}
