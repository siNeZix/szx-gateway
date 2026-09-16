// postgres-debug is an internal, read-only production diagnostics tool.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"szx-gateway/internal/debugsql"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	query := flag.String("query", "", "Read-only PostgreSQL query to execute")
	format := flag.String("format", "table", "Output format: table or json")
	timeout := flag.Duration("timeout", 10*time.Second, "Maximum query duration")
	maxRows := flag.Int("max-rows", 200, "Maximum rows returned")
	envFile := flag.String("env-file", "", "Environment file to load before connecting (default: production.env, then .env)")
	flag.Parse()

	if *query == "" {
		log.Fatal("-query is required")
	}
	if *format != "table" && *format != "json" {
		log.Fatal("-format must be table or json")
	}
	if *timeout <= 0 {
		log.Fatal("-timeout must be greater than zero")
	}
	if *maxRows <= 0 {
		log.Fatal("-max-rows must be greater than zero")
	}
	if err := debugsql.Validate(*query); err != nil {
		log.Fatal(err)
	}

	if err := loadDBDSN(*envFile); err != nil {
		log.Fatal(err)
	}
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		log.Fatal("DB_DSN is required; pass -env-file <path> or provide production.env/.env")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatalf("open PostgreSQL connection: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		log.Fatalf("start read-only transaction: %v", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, *query)
	if err != nil {
		log.Fatalf("run query: %v", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		log.Fatalf("read columns: %v", err)
	}
	result, truncated, err := readRows(rows, columns, *maxRows)
	if err != nil {
		log.Fatalf("read rows: %v", err)
	}
	if *format == "json" {
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			log.Fatalf("encode JSON: %v", err)
		}
	} else {
		writeTable(columns, result)
	}
	if truncated {
		fmt.Fprintf(os.Stderr, "result truncated at %d rows; increase -max-rows if needed\n", *maxRows)
	}
}

func loadDBDSN(envFile string) error {
	if os.Getenv("DB_DSN") != "" {
		return nil
	}
	files := []string{envFile}
	if envFile == "" {
		files = []string{"production.env", ".env"}
	}
	for _, file := range files {
		if file == "" {
			continue
		}
		contents, err := os.ReadFile(filepath.Clean(file))
		if os.IsNotExist(err) && envFile == "" {
			continue
		}
		if err != nil {
			return fmt.Errorf("read environment file %q: %w", file, err)
		}
		for _, line := range strings.Split(string(contents), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok || strings.TrimSpace(key) != "DB_DSN" {
				continue
			}
			value = strings.Trim(strings.TrimSpace(value), `"'`)
			if value == "" {
				return fmt.Errorf("DB_DSN is empty in %q", file)
			}
			return os.Setenv("DB_DSN", value)
		}
		if envFile != "" {
			return fmt.Errorf("DB_DSN is missing in %q", file)
		}
	}
	return nil
}

func readRows(rows *sql.Rows, columns []string, maxRows int) ([]map[string]any, bool, error) {
	result := make([]map[string]any, 0)
	for rows.Next() {
		if len(result) == maxRows {
			return result, true, nil
		}
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			return nil, false, err
		}
		row := make(map[string]any, len(columns))
		for i, value := range values {
			if bytes, ok := value.([]byte); ok {
				row[columns[i]] = string(bytes)
			} else {
				row[columns[i]] = value
			}
		}
		result = append(result, row)
	}
	return result, false, rows.Err()
}

func writeTable(columns []string, rows []map[string]any) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(columns, "\t"))
	for _, row := range rows {
		values := make([]string, len(columns))
		for i, column := range columns {
			values[i] = fmt.Sprint(row[column])
		}
		fmt.Fprintln(w, strings.Join(values, "\t"))
	}
	w.Flush()
}
