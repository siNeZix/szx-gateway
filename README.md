# SZX Gateway

OpenAI-compatible шлюз для ротации ключей OpenRouter и AIHubMix. По умолчанию использует SQLite; MySQL 8 с InnoDB доступен через конфигурацию. Ретраит запросы на других ключах, показывает админку и отдаёт только free-модели.

## Что умеет

- `/v1/chat/completions` и `/v1/models` как у OpenAI.
- Два провайдера одновременно: OpenRouter и AIHubMix.
- Ротация ключей с учётом статуса, дневного лимита и минутного rate limit.
- Retry/fallback при `429`, `401` и `5xx` до `-max-retries` раз.
- Алиасы моделей `top1`, `top2`, `top3` из Shir-Man ranking.
- Web UI с Basic Auth: ключи, bulk-операции, статистика, модели, прокси.
- SQLite без CGO (`modernc.org/sqlite`) или MySQL 8/InnoDB.

## Быстрый старт

```bash
docker compose up -d --build
```

По умолчанию compose публикует:

- `http://localhost:1005` — SZX Gateway для OpenRouter и Web UI.
- `http://localhost:1006` — SZX Gateway для AIHubMix и тот же Web UI.

Локально без Docker:

```bash
npm --prefix web ci
npm --prefix web run build
go run cmd/gateway/main.go
```

## Настройка

Через `.env`, env-переменные или флаги:

```env
GATEWAY_TOKEN=change-me
WEB_USERNAME=admin
WEB_PASSWORD=admin
DB_PATH=/data/gateway.db
LISTEN_ADDR=:8080
AIHUBMIX_LISTEN_ADDR=:8081
RANKING_REFRESH=1h
KEY_CHECK_TTL=1h
KEY_CHECK_INTERVAL=1m
```

### MySQL 8

SQLite остаётся default и не требует дополнительных переменных. Для MySQL нужен отдельный пользователь и пустая база:

```sql
CREATE DATABASE szx_gateway CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE USER 'szx_gateway'@'%' IDENTIFIED BY 'change-me';
GRANT ALL PRIVILEGES ON szx_gateway.* TO 'szx_gateway'@'%';
```

```env
DB_DRIVER=mysql
DB_DSN=szx_gateway:change-me@tcp(mysql-host:3306)/szx_gateway?parseTime=true&loc=UTC&charset=utf8mb4
DB_MAX_OPEN_CONNS=10
DB_MAX_IDLE_CONNS=5
```

Шлюз создаёт таблицы автоматически. Все таблицы используют `InnoDB`; MyISAM не поддерживается из-за транзакций и атомарных резервов дневных квот.

Для переноса существующей SQLite-базы остановите gateway, создайте пустую MySQL-базу и выполните:

```bash
make migrate-sqlite-to-mysql SQLITE_PATH=/data/gateway.db DB_DSN='szx_gateway:change-me@tcp(mysql-host:3306)/szx_gateway?parseTime=true&loc=UTC&charset=utf8mb4'
```

Команда переносит все данные, включая ключи и логи. Не запускайте её повторно в непустую MySQL-базу. У `DB_DSN` должны быть `parseTime=true`, `loc=UTC`, `charset=utf8mb4`.

Флаги:

```bash
go run cmd/gateway/main.go \
  -token change-me \
  -web-user admin \
  -web-pass admin \
  -listen :8080 \
  -aihubmix-listen :8081 \
  -db-path gateway.db \
  -ranking-refresh 1h \
  -key-ttl 1h \
  -key-check-rate 200 \
  -key-check-interval 1m \
  -key-check-concurrency 5 \
  -max-retries 5
```

## Подключение клиентов

OpenRouter:

- Base URL: `http://localhost:1005/v1`
- API Key: значение `GATEWAY_TOKEN`
- Model: `top1`, `top2`, `top3` или конкретная free-модель OpenRouter

AIHubMix:

- Base URL: `http://localhost:1006/v1`
- API Key: значение `GATEWAY_TOKEN`
- Model: конкретная free-модель AIHubMix

Пример:

```bash
curl http://localhost:1005/v1/chat/completions \
  -H "Authorization: Bearer change-me" \
  -H "Content-Type: application/json" \
  -d '{"model":"top1","messages":[{"role":"user","content":"ping"}]}'
```

## Web UI

Откройте `http://localhost:1005` или `http://localhost:1006`.

Логин/пароль по умолчанию: `admin` / `admin`.

Ключи добавляются в UI по провайдерам. API админки живёт под `/api/v2/*` и защищён тем же Basic Auth.

## Разработка

```bash
go test ./...
go fmt ./...
npm --prefix web run build
go build -o build/gateway.exe cmd/gateway/main.go
```

Go: `1.26.3`. Node: `24+`.
