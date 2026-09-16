---
name: postgres-debug
description: Use when diagnosing PostgreSQL database data, request logs, key state, quota, proxy, model, or production gateway incidents. Uses cmd/postgres-debug safely with DB_DSN.
---

# PostgreSQL Debug

Use `go run ./cmd/postgres-debug -query "..."` for PostgreSQL production incident diagnosis. It automatically loads `DB_DSN` from `production.env`, then `.env`, when it is absent from the process. For another file, pass `-env-file path/to/file`.

1. Confirm schema first when needed: query `information_schema.tables` or `information_schema.columns` scoped to `current_schema()`.
2. Use `SELECT`, CTE ending in `SELECT`, or `EXPLAIN`. CLI rejects mutating SQL and executes the query in a read-only transaction.
3. Bound scans with a time condition, selective columns, `ORDER BY`, and `LIMIT`. Default result cap is 200; use `-max-rows` only when required.
4. Use `-format json` only when structured processing is useful. Keep `-timeout` low unless a query plan proves a longer query is necessary.
5. Treat output as confidential. Never reveal `DB_DSN`, `keys.raw_key`, `proxies.raw`, proxy `username`/`password`, or other credentials in messages, commits, docs, logs, or external calls. Summarize sensitive evidence instead.

Do not bypass the CLI with direct `psql` unless user explicitly requests a state-changing database operation.
