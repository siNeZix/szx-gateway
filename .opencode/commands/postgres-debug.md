---
description: Диагностирует production PostgreSQL через внутренний read-only CLI. Аргумент: SQL или описание проблемы.
agent: build
---

Используй `cmd/postgres-debug` для диагностики PostgreSQL-проблемы: $ARGUMENTS

В production используется PostgreSQL. CLI сам загружает `DB_DSN` из `production.env`, затем `.env`; для другого файла используй `-env-file path/to/file`. Сначала выясни схему через `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema()` или запрос к `information_schema.columns`, если она не известна. Затем выполни минимальные read-only запросы через `go run ./cmd/postgres-debug -query "..."`. Не запускай прямой `psql` и не выполняй изменяющий SQL. Результаты могут содержать секреты: не показывай raw keys, DSN, proxy логины и пароли в ответе.
