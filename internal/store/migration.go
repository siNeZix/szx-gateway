package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// MigrateSQLiteToMySQL copies all gateway tables into an empty MySQL database.
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
		if err := copyTable(source, destination.db, table); err != nil {
			return err
		}
	}
	return nil
}

func copyTable(source, destination *sql.DB, table string) error {
	rows, err := source.Query("SELECT * FROM `" + table + "`")
	if err != nil {
		return fmt.Errorf("read sqlite table %s: %w", table, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	if len(columns) == 0 {
		return nil
	}
	quoted := make([]string, len(columns))
	placeholders := make([]string, len(columns))
	for i, column := range columns {
		quoted[i] = "`" + strings.ReplaceAll(column, "`", "``") + "`"
		placeholders[i] = "?"
	}
	query := "INSERT INTO `" + table + "` (" + strings.Join(quoted, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	tx, err := destination.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statement, err := tx.Prepare(query)
	if err != nil {
		return fmt.Errorf("prepare MySQL insert for %s: %w", table, err)
	}
	defer statement.Close()
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	for rows.Next() {
		if err := rows.Scan(pointers...); err != nil {
			return fmt.Errorf("read sqlite row from %s: %w", table, err)
		}
		if _, err := statement.Exec(values...); err != nil {
			return fmt.Errorf("copy row into %s: %w", table, err)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s: %w", table, err)
	}
	return nil
}
