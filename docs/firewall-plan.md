# Firewall: план интеграции Cerberus

Источник: [Asati-git/ai-agent-firewall](https://github.com/Asati-git/ai-agent-firewall) (Cerberus)

## Контекст

Cerberus — локальный фаервол для AI coding agents на tool-boundary (Bash/Read/Write).
szx-gateway — API-шлюз на API-boundary (chat completions → OpenRouter/AIHubMix).

Цепочка трафика:
```
Opencode (агент) → szx-gateway → OpenRouter/AIHubMix
                        ↑
                   фаервол здесь
                        ↓
             ответ API проходит обратно через szx-gateway
                        ↓
             агент получает ответ и может исполнить tool call
```

szx-gateway видит ответ API **до** агента — может перехватить вирусную команду.

## Что берём из Cerberus

| Файл Cerberus | Что | Чем полезно |
|---|---|---|
| `signals/injection.ts` | 6 regex на injection-фразы | Ловит "ignore previous instructions", "you are now DAN" в ответе API → блокирует до агента |
| `signals/secrets.ts` | 7 regex на секреты + `redactSecrets()` | Если API вернёт чужой AWS/GitHub/OpenAI ключ — редачит на `[REDACTED:type]` |
| `rules/default_policy.yaml` | regex опасных команд | Ловит саму команду: `rm -rf`, `curl|sh`, `pip install malware` в тексте ответа → блокирует до агента |

## Что НЕ берём

| Файл Cerberus | Почему |
|---|---|
| `behavioral.ts` | Rate/repetition tool calls — нет применимо к API traffic |
| `content.ts` | Session contamination state across tool calls — нет сессий |
| `risk_weights.yaml` | Weighted scoring — избыточно, бинарный verdict достаточен |
| ONNX DeBERTa model | Тяжёлая зависимость, эвристики достаточно для V1 |
| `proxy/`, `mcp/`, `deps/` | Tool-boundary фичи, не применимы к API-шлюзу |

## Где перехватываем

| Фаза | Что сканируем | Где в коде | Действие |
|---|---|---|---|
| **Request** (агент → API) | `messages[].content` — секреты | `handler.go:204`, `aihubmix.go:168` | BLOCK |
| **Response non-stream** (API → агент) | `choices[].message.content` — injection + команды + секреты | `handler.go:346` (`handleNormalResponse`), `aihubmix.go:375` (`relayResponse`) | BLOCK или REDACT |
| **Response stream** (API → агент) | SSE deltas `content` — то же, инлайн | `handler.go:400` (`handleStreamResponse`), `aihubmix.go:375` | REDACT инлайн, injection/команды — log + алерт |

### Streaming-подход
- Секреты — редачить инлайн по SSE deltas (sliding window ~256 байт overlap)
- Injection/команды — log-only с алертом в `firewall_events` (блокировка стрима = зависший клиент, UX хуже чем пропустить + алерт)
- `ponytail:` upgrade path — буферизация с блокировкой если понадобится

## Структура

```
internal/firewall/
├── secrets.go       — 7 паттернов + DetectSecrets + RedactSecrets (порт secrets.ts)
├── injection.go      — 6 regex паттернов + Classify (порт injection.ts)
├── commands.go       — regex опасных команд из default_policy.yaml
├── firewall.go       — Inspect (request), InspectResponse (non-stream), StreamScanner (stream)
└── firewall_test.go  — self-check
```

## Config

| Флаг | Env | Default | Эффект |
|---|---|---|---|
| `-firewall` | `FIREWALL_ENABLED` | `false` | Включить фаервол |
| `-firewall-block` | `FIREWALL_BLOCK` | `true` | Блокировать (`false` = только логировать) |
| `-firewall-redact` | `FIREWALL_REDACT` | `true` | Редачить секреты в ответе вместо блокировки |

## Логирование

Новая таблица `firewall_events` (не трогает существующую схему):
- `id`, `timestamp`, `action` (BLOCK/REDACT/LOG), `phase` (request/response), `provider`, `reason`, `secret_types`

## Покрытие тестами

- `TestRequest_BlocksSecretInPrompt` — секрет в messages → BLOCK
- `TestResponse_BlocksInjection` — "ignore previous instructions" в ответе → BLOCK
- `TestResponse_BlocksDangerousCommand` — "rm -rf /" в ответе → BLOCK
- `TestResponse_RedactsAWSSecret` — `AKIA...` в content → REDACT
- `TestStreamScanner_RedactsInline` — sliding window по SSE deltas
- `TestStreamScanner_LogsInjection` — injection в стриме → log event, поток продолжается

## Цена

False positives — агент не получит легитимный ответ где модель обсуждает `rm -rf` (документация, статья). Решается `--firewall-block=false` (только логирование).

## Объём

~250 строк Go + ~80 строк тестов. 1 файл `internal/firewall/` + врезка в `handler.go`/`aihubmix.go` + 1 миграция БД.