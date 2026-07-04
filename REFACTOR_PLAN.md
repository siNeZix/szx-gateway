# Рабочая тетрадь: перепис Web UI → SPA

> Worktree: `.worktrees/spa-web` (ветка `spa-web`).
> Цель: переписать `html/template` + ~1000 строк vanilla JS в `internal/web/server.go`
> на полноценный SPA (React + TS + Vite), подготовив архитектуру к росту.
> Принцип: **backend сначала**, не ломать старый UI пока новый не покроет функционал,
> один self-contained Go-бинарник через `go:embed`.

---

## ✅ Прогресс

| # | Задача | Статус | Заметки |
|---|---|---|---|
| 0.1 | Зафиксировать текущие API-эндпоинты старого UI | ✅ | См. «Инвентаризация» ниже |
| 0.2 | Создать `.worktrees/spa-web`, ветку, правила worktree | ✅ | Глобальный skill `worktree` |
| 1.1 | JSON-хелперы в `internal/web` (`writeJSON`, `writeAPIError`, `providerFromRequest`) | ✅ | `internal/web/api.go` |
| 1.2 | JSON-теги в store-структурах (`GeneralStats`, `ModelStats`, `KeyUsageStats`, `DBModel`, `DBKey.RawKey` → `json:"-"`) | ✅ | `internal/store/sqlite.go` |
| 1.3 | `GET /api/v2/providers` | ✅ | Реестр захардкожен, YAGNI до 3-го провайдера |
| 1.4 | `GET /api/v2/keys?provider=&status=` | ✅ | С фильтром по статусу |
| 1.5 | `POST /api/v2/keys` (добавить пачкой, JSON+form) | ✅ | Обратная совместимость с form-encoded |
| 1.6 | `POST /api/v2/keys/bulk` (enable/disable/delete) | ✅ | JSON+form |
| 1.7 | `GET /api/v2/stats?provider=` (general + models + keys + top + trend) | ✅ | Обёрнуто в `{data:...}` |
| 1.8 | `GET /api/v2/stats/models?provider=` | ✅ | |
| 1.9 | `GET /api/v2/stats/usage?provider=&range=N` (тренд) | ✅ | `range` в днях, дефолт 14 |
| 1.10 | `GET /api/v2/models?provider=` (top + free + aihubmix) | ✅ | |
| 1.11 | Тесты JSON API (contract + lifecycle + error cases) | ✅ | 3 теста, все зелёные |
| 1.12 | `go:embed internal/web/dist` (пустой, заглушка) | ⏳ | Перенесено на срез 2 — нужен `dist/` от Vite |
| 2.1 | Vite + React + TS каркас в `web/` | ✅ | Tailwind v4 (CSS-first), Vite 8 |
| 2.2 | Tailwind + базовый layout (sidebar/header/provider switcher) | ✅ | Тёмная тема slate-900 как в старом UI |
| 2.3 | TanStack Query + React Router | ✅ | refetchInterval 5s на keys/stats |
| 2.4 | `src/types/index.ts` (контракты API) | ✅ | Ручная синк с Go-структурами |
| 2.5 | `src/api/client.ts` (fetch + разворот конверта `{data}`) | ✅ | |
| 2.6 | Provider-контекст (`useProvider`) с синком в URL | ✅ | `?provider=aihubmix` переживает reload |
| 2.7 | `Dashboard.tsx` (карточки + топ моделей) | ✅ | 7 StatCard + таблица |
| 2.8 | `Keys.tsx` (TanStack Table: sort/filter/search/pagination/bulk/add) | ✅ | Самая функциональная страница |
| 2.9 | `Stats.tsx` (Recharts: requests/tokens/latency/errors, range) | ✅ | 7/14/30 дней; aihubmix-only тренд |
| 2.10 | `Models.tsx` (top + free, поиск, copy-markdown) | ✅ | |
| 2.11 | `npm run build` green, dist → `internal/web/dist/` | ✅ | JS 685кб (gzip 204кб), chunk-size warn не блокирующий |
| 2.12 | `.gitignore`: dist игнорируется, `.gitkeep` коммитится | ✅ | |
| 3.1 | `go:embed internal/web/dist` + static handler + SPA fallback | ✅ | SPA пока под `/spa/`, старый `/` не трогаем |
| 4.1 | Docker multi-stage (Node build → Go build → alpine) | ✅ | Dockerfile обновлён; docker build не проверен — daemon off |
| 5.1 | Удалить старый `dashboardTemplate` | ⏳ | Только после smoke-теста SPA в проде |

**Условные обозначения:** ✅ готово · ⏳ в работе/дальше · ❌ блокер

---

## 📔 Журнал

### 2026-07-04 — подготовка
- Прочитан код `internal/web/server.go`, `internal/store/sqlite.go`, `cmd/gateway/main.go`,
  `Dockerfile`, `docker-compose.yml`, `go.mod`.
- Создан глобальный opencode skill `worktree` (`C:\Users\denis\.config\opencode\skills\worktree\SKILL.md`)
  с едиными правилами: директория `.worktrees/<slug>/`, ветка = слаг, всё в корневом `.gitignore`.
- В основном дереве в `.gitignore` добавлено `/.worktrees/`.
- Создан worktree `.worktrees/spa-web` на ветке `spa-web` (от `08042c8 [main]`).
- Внутри worktree в `.gitignore` продублировано `/.worktrees/`.
- Этот файл (`REFACTOR_PLAN.md`) живёт в корне worktree и ведётся как рабочая тетрадь.

### Инвентаризация текущего UI (`internal/web/server.go`)
Что использует старый UI сегодня (точки контакта Go↔браузер):

| Метод | Путь | Тип ответа | Что делает |
|---|---|---|---|
| GET | `/` | HTML (`dashboardTemplate`) | Рендерит весь дашборд с инлайн-данными |
| GET | `/api/stats?provider=` | JSON | general + models + keys + top_models + usage_trend + refreshed_at |
| POST | `/keys/add` (form) | redirect `/?provider=` | Добавляет ключи пачкой |
| POST | `/keys/delete` (form) | redirect `/?provider=` | Удаляет один ключ по hash |
| POST | `/keys/bulk` (form) | redirect ИЛИ JSON `{"success":true}` | bulk enable/disable/delete; JSON только при `X-Requested-With: XMLHttpRequest` или `Accept: application/json` |

Итог инвентаризации: единственный настоящий JSON-эндпоинт — `/api/stats`.
Add/delete/bulk — form-post’ы с редиректом, bulk умеет JSON по заголовку.
В первом срезе добавляем полноценные JSON-аналоги рядом, не трогая старые,
чтобы не сломать текущий UI.

### 2026-07-04 — Срез 1 готов (backend JSON API)
- Добавлены JSON-теги в `store.DBKey`, `store.DBModel`, `store.GeneralStats`,
  `store.ModelStats`, `store.KeyUsageStats`. Поле `RawKey` помечено `json:"-"` —
  сырой ключ никогда не утекает в API (проверено тестом).
- Создан `internal/web/api.go` (~430 строк):
  - хелперы `writeJSON`/`writeAPIError`/`providerFromRequest` (конверт `{data}`/`error}`)
  - `GET /api/v2/providers` — список провайдеров со сводным статусом
  - `GET /api/v2/keys?provider=&status=` — ключи с usage, с опциональным фильтром по статусу
  - `POST /api/v2/keys` — добавить пачкой (JSON или form-encoded)
  - `POST /api/v2/keys/bulk` — enable/disable/delete (JSON или form-encoded)
  - `GET /api/v2/stats?provider=` — общий снимок (general+models+keys+top+trend)
  - `GET /api/v2/stats/models?provider=` — только модели
  - `GET /api/v2/stats/usage?provider=&range=N` — тренд по дням
  - `GET /api/v2/models?provider=` — top + free модели
- Роуты подключены в `server.go` через `ws.registerAPIRoutes(mux)`.
- **Версионирование v2:** новый API живёт под `/api/v2/*`, старый `/api/stats`
  не тронут — его использует текущий HTML UI (`fetch('/api/stats')` в server.go:572).
  Когда SPA покроет функционал, старый эндпоинт будет удалён.
- Тесты `internal/web/api_test.go`: 3 сценария (contract, keys lifecycle, error cases).
  Полный прогон `go test ./...` — зелёный.
- `go:embed` для `dist/` отложен до среза 2: пустой dist даёт невалидный embed,
  Vite создаст его первым билдом.

### 2026-07-04 — Срез 2 готов (frontend-каркас SPA)
- Создан Vite + React + TS проект в `web/` (`npm create vite@latest web -- --template react-ts`).
- Установлены: Tailwind CSS v4 (@tailwindcss/vite, CSS-first конфиг), TanStack Query,
  TanStack Table, React Router v7, Recharts.
- Структура `web/src/`:
  - `types/index.ts` — контракты API (ручная синк с Go-структурами)
  - `api/client.ts` — fetch-клиент, разворачивает конверт `{data,error}` в данные/исключение
  - `providers/query.ts` — QueryClient с дефолтами (retry 1, staleTime 3s)
  - `providers/provider.tsx` — Context с активным провайдером, синк в URL `?provider=`
  - `components/layout/Layout.tsx` — header + nav + provider switcher
  - `components/ui/basics.tsx` — StatCard, StatusBadge
  - `pages/Dashboard.tsx` — 7 сводочных карточек + топ-10 моделей
  - `pages/Keys.tsx` — полная таблица: TanStack Table (sort/filter/search/pagination),
    bulk-actions (enable/disable/delete), добавление пачкой, polling 5s
  - `pages/Stats.tsx` — 4 графика Recharts (requests/tokens/latency/errors), range 7/14/30d
  - `pages/Models.tsx` — top + free модели, поиск, copy-as-markdown
- `vite.config.ts`: dev-прокси `/api` и `/keys` → localhost:8080; build → `../internal/web/dist`.
- `npm run build` зелёный. JS bundle 685кб (gzip 204кб) — recharts/tanstack весят, норм для админки.
- `index.html`: `lang="ru"`, `<title>LLM Gateway</title>`.
- `.gitignore` (корневой): `/internal/web/dist/*` игнорируется, `!/internal/web/dist/.gitkeep` —
  чтобы Go-сборка работала без Node-сборки (dist пустой → Go-сервер просто не раздаёт статику,
  это ок для разработки).
- **Решение: `go:embed` отложен на срез 3.** Сейчас dist генерится Vite. Если зашить embed
  прямо сейчас, любой `go build` без Node будет падать (нет файлов). Срез 3 сделает это
  аккуратно с тестовым fallback.

### 2026-07-04 — Срез 3/4 готов (embed + Docker)
- SPA раздаётся из Go под `/spa/`:
  - `/spa` → редирект на `/spa/`
  - `/spa/` → `index.html`
  - `/spa/assets/...`, `/spa/favicon.svg`, `/spa/icons.svg` → файлы из `internal/web/dist`
  - неизвестные `/spa/*` → fallback на `index.html` для React Router
- Старый `/` и текущий `dashboardTemplate` **не тронуты**. Это безопасный шаг:
  текущий HTML UI остаётся рабочим, SPA можно smoke-тестить параллельно.
- `web/vite.config.ts`: `base: '/spa/'`, поэтому build пишет ассеты как `/spa/assets/...`.
- `web/src/App.tsx`: `BrowserRouter basename="/spa"`.
- Добавлен `internal/web/static.go` с `go:embed dist/*` и `handleSPA`.
- Добавлен `internal/web/static_test.go` на чистку SPA path (`cleanSPAPath`) — маленький тест
  для security/route-логики.
- Dockerfile стал multi-stage:
  - `node:24-alpine` собирает SPA (`npm ci`, `npm run build`)
  - `golang:1.26-alpine` копирует `internal/web/dist` из Node stage и собирает бинарник
  - финальный `alpine:3.20` оставлен как раньше (ca-certificates + user app)
- `.dockerignore`: исключены `.worktrees/`, `web/node_modules/`, `web/dist/`, `internal/web/dist/`.
- Проверки:
  - `npm run build` — зелёный (chunk-size warning не блокирует)
  - `go test ./...` — зелёный
  - `go build ./...` — зелёный
  - `docker build` не проверен: Docker daemon не запущен (`dockerDesktopLinuxEngine` отсутствует).

### Решения по архитектуре
- `web/` (SPA) — в корне репозитория, рядом с `internal/`. Не внутри `internal/web`,
  потому что Vite-проект — это отдельная toolchain и отдельный package.json.
- `internal/web/handlers/*.go` — **пока не дробим**. Один пакет `web`, 2 файла
  (server.go + api.go). Ponytail: дробление имеет смысл при ~80+ строк на ресурс.
- Реестр провайдеров (Этап 6 плана) — отложен. Сейчас провайдеров ровно два
  (`openrouter`, `aihubmix`), оба захардкожены в `main.go`. Динамический реестр
  через конфиг — YAGNI, пока не появится третий провайдер.
- shadcn/ui — подключаем во втором проходе, когда SPA уже работает и понятно,
  какие компоненты реально нужны. До этого — Tailwind + самописные компоненты.

---

## 🎯 Стек (референс)

| Слой | Технология | Почему |
|---|---|---|
| Frontend | **React + TypeScript + Vite** | Самый знакомый стек для LLM-агентов, больше примеров, проще генерить/чинить код. |
| UI | **Tailwind CSS + shadcn/ui** | Единый стиль, нет vendor lock-in, легко кастомизировать. shadcn — со второго прохода. |
| Данные | **TanStack Query** | Polling, кеш, invalidate. Не нужен Redux. |
| Таблицы | **TanStack Table** | Сортировка, фильтры, пагинация, bulk-actions из коробки. |
| Графики | **Recharts** | Минимум зависимостей, достаточно для админки. |
| Роутинг | **React Router** | Многостраничная админка. |
| Backend | Текущий Go `net/http` | Без изменений. |
| Сборка | Vite build → `internal/web/dist` → `go:embed` | Один бинарник, без отдельных статик-серверов. |

### Что НЕ берём
- **Next.js** — лишний Node-сервер рядом с Go.
- **Redux/Zustand** — не нужен, TanStack Query закрывает API-state.
- **Material UI / Ant Design** — тяжёлые, сложно сменить стиль.
- **HTMX/Alpine** — мало для растущего SPA.

---

## 🏗️ Целевая структура (ориентир, не догма)

```
cmd/gateway
internal/
  config/ store/ keys/ models/ proxy/ limits/
  web/
    server.go          // старый HTML UI (удаляется на финальном этапе)
    api.go             // JSON API endpoints (новое)
    static.go          // go:embed dist/, раздача SPA (новое)
web/                    // отдельная папка для SPA
  package.json
  vite.config.ts
  tsconfig.json
  index.html
  src/
    main.tsx
    App.tsx
    api/                // fetch-клиенты
    types/index.ts      // контракты API (ручная синк с Go)
    components/         // ui, layout, charts
    pages/              // Dashboard, Keys, Stats, Models
    providers/query.ts  // TanStack Query setup
```

`internal/web/handlers/*.go` и `middleware.go` из исходного плана — **пока не создаем**.
Один пакет `web`, плоско. Разделим когда объём реально того потребует.

---

## 📜 Принципы

1. **Backend сначала.** Сначала нормальный JSON API, потом SPA. SPA без API бесполезен.
2. **Не ломать старый UI**, пока новый не покрывает функционал.
3. **Go-бинарник остаётся self-contained.** SPA embed-ится через `go:embed`.
4. **Контракты TypeScript** синхронизируются с Go-структурами вручную через `types/index.ts`.
5. **Ponytail-принцип:** добавляем абстракции только когда их требует текущий этап, а не будущий.
6. **Docker multi-stage:** стадия Node для сборки SPA, стадия Go для сборки бинарника, финальный образ чистый.

---

## 🎓 Критерий готовности

- Старый HTML-шаблон удалён.
- Все текущие функции работают в SPA.
- Добавлен новый функционал: графики, multi-provider.
- `go build` собирает один бинарник со встроенным UI.
- Docker-образ собирается через `docker compose up -d --build`.
