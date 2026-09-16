# SZX Gateway (agents.md)

This file contains instructions, conventions, and operational patterns for any AI agents or developers working on the `szx-gateway` project.

## 🛠️ Stack & Architecture Overview
- **Language:** Go (1.26.3)
- **Database:** PostgreSQL 16 is production backend via `github.com/jackc/pgx/v5`; SQLite via pure Go driver (`modernc.org/sqlite`) is available for local development. MySQL remains an optional legacy backend. Do not use CGO SQLite drivers.
- **Routing:** Standard library `net/http` (no complex router/frameworks).
- **Core Components:**
  - `cmd/gateway/main.go`: Entry point, lifecycle management, HTTP server orchestration, and graceful shutdown.
  - `internal/config`: Configurations via flags and environment variables.
  - `internal/store`: Database layer for API keys, usage statistics, logs, ranking and SQLite migrations.
  - `cmd/postgres-debug`: Internal read-only PostgreSQL diagnostics CLI for agents and developers.
  - `internal/keys`: 
    - `KeyPool`: Thread-safe pool managing key allocation, status rotation (`Active`, `Cooldown`, `Exhausted`, `Invalid`, `Disabled`), and minute/daily quotas.
    - `KeyChecker`: Background ticker worker that validates key active statuses/limits with OpenRouter `/api/v1/key` endpoint. Ignores `disabled` keys.
  - `internal/models`: Periodically fetches the model rank top from Shir-Man API, resolving aliases like `top1`/`top2`/`top3`.
  - `internal/proxy`: Thread-safe reverse proxy (`/v1/chat/completions`) implementing auto-retry on fallback keys (up to 5 times) and proxying requested payloads.
  - `internal/web`: JSON API (`/api/v2/*`) and embedded SPA static serving via `go:embed dist/*`, protected via Basic Auth.
  - `web`: React + TypeScript + Vite SPA. Vite build writes to `internal/web/dist`.

## 🚀 Deployment Strategy (Zero-CI/CD)
Проект деплоится максимально просто и эффективно без тяжелого CI/CD:
- **Метод:** Прямой пуш исходников на прод `git push prod main`.
- **Механизм:** На сервере настроен bare-репозиторий и хук `post-receive`, который чекаутит ветку `main` в рабочую папку и запускает локальную пересборку Docker-контейнера через `docker compose up -d --build`.
- **Сборка:** Осуществляется на стороне сервера в легковесном Docker-контейнере. Зависимости кэшируются, мелкие правки деплоятся за считанные секунды.
  - **Хранение БД:** В production данные хранятся в PostgreSQL; SQLite-файл для локальной разработки хранится в `./data/`.

## 📜 Development Guidelines & Rules (Ponytail-Friendly)
1. **Zero Over-Engineering:** Keep standard library solutions first. Do not add routing, ORM, or state-management packages. Use raw SQL/prepared statements inside `store.go`.
2. **Concurrency Safety:** `KeyPool` and checkers operate concurrently. Always guard map/slice/counter reads and writes with read-write mutexes (`sync.RWMutex`).
3. **Robust Retries & Error States:** In `internal/proxy`, errors like `429` (too many requests), `401` (unauthorized), and `5xx` must not immediately propagate to the client. Mark the offending key, activate cooldown/invalid status, and retry immediately using another healthy key.
4. **No SQLite CGO:** Keep SQLite purely serverless and C-dependency free.
5. **Bulk & Key Management:** Ключи могут переводиться в статус `disabled`. В этом статусе они полностью исключаются из ротации и проверок чекером. Групповые (bulk) операции выполняются через эндпоинт `/api/v2/keys/bulk` и обрабатываются транзакционно на уровне БД.
6. **Add High-Value Tests only:** Code changes that modify the rotation logic or check criteria should be covered in `sqlite_test.go`, `state_test.go`, or `server_test.go`.
7. **Language Constraint:** **ALWAYS respond in Russian.** All communication with the user must be in Russian only. Code comments can remain in English/Russian matching existing files, but explanations, summaries, and agent output must be Russian.

## PostgreSQL Debug Policy

- В production используется PostgreSQL. Для диагностики используй только `go run ./cmd/postgres-debug`.
- Сначала выясни схему через `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema()` или `SELECT column_name, data_type FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = '<table>'`, затем делай минимальный запрос, проверяющий гипотезу.
- CLI берёт полный `DB_DSN`; он намеренно может читать все таблицы, в том числе `keys.raw_key` и proxy credentials. Значения считать секретами: не копировать в ответ, документацию, коммиты, логи, issues и внешние tool calls.
- Разрешены `SELECT`, CTE с финальным `SELECT` и `EXPLAIN`. CLI блокирует изменяющие запросы и запускает запрос внутри read-only транзакции. Не обходить проверку прямым `psql` без явной команды пользователя.
- Не выполнять миграцию SQLite-to-PostgreSQL для диагностики: она замещает данные целевой БД.
- CLI сам ищет `production.env`, затем `.env`, если `DB_DSN` не задан в процессе. Для другого файла передай `-env-file <path>`; не передавай DSN в аргументах командной строки.

## ⚙️ Key Commands
- **Run local app:** `go run cmd/gateway/main.go`
- **Run all unit tests:** `go test ./...`
- **Lint/Format:** `go fmt ./...`
- **Build SPA:** `npm --prefix web ci; npm --prefix web run build`
- **Build binary:** `go build -o build/gateway.exe cmd/gateway/main.go`
- **PostgreSQL debug:** `go run ./cmd/postgres-debug -query "SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema()"`
- **Deploy to prod:** `git push prod main`
