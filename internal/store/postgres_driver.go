package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/stdlib"
)

func init() {
	sql.Register("postgres", postgresDriver{driver: stdlib.GetDefaultDriver()})
}

// postgresDriver keeps the store queries portable: PostgreSQL needs numbered
// placeholders and double-quoted identifiers while SQLite and MySQL accept ?.
type postgresDriver struct {
	driver driver.Driver
}

func (d postgresDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.driver.Open(name)
	if err != nil {
		return nil, err
	}
	return postgresConn{Conn: conn}, nil
}

type postgresConn struct {
	driver.Conn
}

func (c postgresConn) Prepare(query string) (driver.Stmt, error) {
	return c.Conn.Prepare(postgresQuery(query))
}

func (c postgresConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if conn, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return conn.PrepareContext(ctx, postgresQuery(query))
	}
	return c.Prepare(query)
}

func (c postgresConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if conn, ok := c.Conn.(driver.ConnBeginTx); ok {
		return conn.BeginTx(ctx, opts)
	}
	return c.Conn.Begin()
}

func (c postgresConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if conn, ok := c.Conn.(driver.ExecerContext); ok {
		return conn.ExecContext(ctx, postgresQuery(query), args)
	}
	return nil, driver.ErrSkip
}

func (c postgresConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if conn, ok := c.Conn.(driver.QueryerContext); ok {
		return conn.QueryContext(ctx, postgresQuery(query), args)
	}
	return nil, driver.ErrSkip
}

func (c postgresConn) CheckNamedValue(value *driver.NamedValue) error {
	if conn, ok := c.Conn.(driver.NamedValueChecker); ok {
		return conn.CheckNamedValue(value)
	}
	return nil
}

func postgresQuery(query string) string {
	query = strings.ReplaceAll(query, "`", `"`)
	var out strings.Builder
	out.Grow(len(query) + 8)
	placeholder := 0
	inQuote := false
	for _, r := range query {
		if r == '\'' {
			inQuote = !inQuote
		}
		if r == '?' && !inQuote {
			placeholder++
			out.WriteByte('$')
			out.WriteString(strconv.Itoa(placeholder))
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
