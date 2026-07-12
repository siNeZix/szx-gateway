package web

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"szx-gateway/internal/limits"
	"szx-gateway/internal/store"
)

// Единый конверт ответа API: { data: ... } или { error: "..." }.
// writeJSON отдаёт успех, writeAPIError — ошибку с единым форматом.

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func writeAPIError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": msg})
}

// providerFromRequest возвращает разрешённый провайдер из ?provider=.
// Дефолт — openrouter. Неизвестные → openrouter.
// Вторым возвратом идёт bool: false если провайдер неизвестен (caller решает, 400 или дефолт).
func providerFromRequest(r *http.Request) (string, bool) {
	p := strings.TrimSpace(r.URL.Query().Get("provider"))
	if p == "" || (p != "openrouter" && p != "aihubmix" && p != "google") {
		return "openrouter", true
	}
	return p, true
}

// requireProviderForm достаёт provider из JSON-body или form. Для POST /api/keys и bulk.
func requireProviderForm(r *http.Request) (string, bool) {
	p := strings.TrimSpace(r.FormValue("provider"))
	if p != "openrouter" && p != "aihubmix" && p != "google" {
		return "", false
	}
	return p, true
}

// registerAPIRoutes навешивает все /api/v2/* JSON-эндпоинты на mux.
// Версионирование v2: старый /api/stats (без версии) используется текущим HTML UI
// и возвращает плоский JSON без обёртки {data:...}. Новый API единообразно оборачивает
// ответы в {data:...}/{error:...}. Когда SPA покроет функционал, старый /api/stats
// и form-эндпоинты /keys/* будут удалены, а v2-префикс можно будет бросить.
func (ws *WebServer) registerAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v2/providers", ws.basicAuth(ws.apiProviders))
	mux.HandleFunc("/api/v2/keys", ws.basicAuth(ws.apiKeys))
	mux.HandleFunc("/api/v2/keys/bulk", ws.basicAuth(ws.apiKeysBulk))
	mux.HandleFunc("/api/v2/stats", ws.basicAuth(ws.apiStats))
	mux.HandleFunc("/api/v2/stats/models", ws.basicAuth(ws.apiStatsModels))
	mux.HandleFunc("/api/v2/stats/usage", ws.basicAuth(ws.apiStatsUsage))
	mux.HandleFunc("/api/v2/stats/usage/hourly", ws.basicAuth(ws.apiStatsUsageHourly))
	mux.HandleFunc("/api/v2/stats/usage/5m", ws.basicAuth(ws.apiStatsUsage5m))
	mux.HandleFunc("/api/v2/requests", ws.basicAuth(ws.apiRequests))
	mux.HandleFunc("/api/v2/models", ws.basicAuth(ws.apiModels))
	mux.HandleFunc("/api/v2/proxies", ws.basicAuth(ws.apiProxies))
	mux.HandleFunc("/api/v2/proxies/bulk", ws.basicAuth(ws.apiProxiesBulk))
	mux.HandleFunc("/api/v2/proxies/logs", ws.basicAuth(ws.apiProxyLogs))
	mux.HandleFunc("/api/v2/proxies/stats", ws.basicAuth(ws.apiProxyStats))
	mux.HandleFunc("/api/v2/proxies/usage/5m", ws.basicAuth(ws.apiProxyUsage5m))
	mux.HandleFunc("/api/v2/proxy-settings", ws.basicAuth(ws.apiProxySettings))
}

// ---- Ресурс: провайдеры ----

// providerInfo — статус одного провайдера для списка.
type providerInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	BaseURL    string `json:"base_url"`
	TotalKeys  int    `json:"total_keys"`
	ActiveKeys int    `json:"active_keys"`
}

type dailyLimitsInfo struct {
	Total     int64  `json:"total"`
	Used      int64  `json:"used"`
	Remaining int64  `json:"remaining"`
	Source    string `json:"source"`
	Models    []any  `json:"models"`
}

func dailyLimits(provider string, keys []store.KeyUsageStats) dailyLimitsInfo {
	info := dailyLimitsInfo{Source: "static", Models: []any{}}
	for _, k := range keys {
		if k.Status == "disabled" || k.Status == "invalid" {
			continue
		}
		limit := k.Limit
		if provider == "openrouter" {
			limit = limits.OpenRouterFreeRequestsDay
		} else if provider == "google" {
			limit = limits.GoogleFreeRequestsDay
		}
		if limit <= 0 {
			continue
		}
		info.Total += limit
		used := k.TodayUsage
		if used > limit {
			used = limit
		}
		info.Used += used
	}
	info.Remaining = info.Total - info.Used
	return info
}

// GET /api/providers — список известных провайдеров со сводным статусом.
func (ws *WebServer) apiProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// ponytail: реестр захардкожен. Перенесётся в конфиг, когда появится 3-й провайдер (YAGNI).
	defs := []struct {
		ID, Name, BaseURL string
	}{
		{"openrouter", "OpenRouter", "https://openrouter.ai/api/v1"},
		{"aihubmix", "AIHubMix", "https://aihubmix.com/v1"},
		{"google", "Google AI Studio", "https://generativelanguage.googleapis.com/v1beta"},
	}

	out := make([]providerInfo, 0, len(defs))
	for _, d := range defs {
		g, err := ws.store.GetGeneralStats(d.ID)
		if err != nil {
			log.Printf("apiProviders: GetGeneralStats(%s): %v", d.ID, err)
			writeAPIError(w, http.StatusInternalServerError, "stats load failed")
			return
		}
		out = append(out, providerInfo{
			ID:         d.ID,
			Name:       d.Name,
			BaseURL:    d.BaseURL,
			TotalKeys:  g.TotalKeys,
			ActiveKeys: g.ActiveKeys,
		})
	}

	writeJSON(w, http.StatusOK, out)
}

// ---- Ресурс: ключи ----

// GET /api/keys?provider=openrouter|aihubmix&status=active|disabled|...
// Возвращает ключи с usage-статистикой. Polling каждые 5s.
// POST /api/keys — добавить ключи пачкой (см. apiKeysAdd).
func (ws *WebServer) apiKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		ws.apiKeysAdd(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	provider, _ := providerFromRequest(r)
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))

	stats, err := ws.store.GetKeyUsageStats(provider)
	if err != nil {
		log.Printf("apiKeys: GetKeyUsageStats(%s): %v", provider, err)
		writeAPIError(w, http.StatusInternalServerError, "keys load failed")
		return
	}

	now := time.Now()
	type keyItem struct {
		MaskedKey     string `json:"masked_key"`
		KeyHash       string `json:"key_hash"`
		Status        string `json:"status"`
		TodayUsage    int64  `json:"today_usage"`
		Limit         int64  `json:"limit"`
		TotalRequests int64  `json:"total_requests"`
		ErrorRequests int64  `json:"error_requests"`
		CooldownLeft  string `json:"cooldown_left"`  // человекочитаемое время; пусто если не на кулдауне
		CooldownUntil string `json:"cooldown_until"` // ISO-время для фронтенд-форматирования; пусто если never
		LastUsedAt    string `json:"last_used_at"`   // ISO-время; пусто если never
	}

	out := make([]keyItem, 0, len(stats))
	for _, k := range stats {
		if statusFilter != "" && k.Status != statusFilter {
			continue
		}

		cooldownLeft := ""
		cooldownUntil := ""
		lastUsedAt := ""
		if !k.CooldownUntil.IsZero() && k.CooldownUntil.Unix() > 0 {
			cooldownUntil = k.CooldownUntil.Format(time.RFC3339)
			if k.CooldownUntil.After(now) {
				cooldownLeft = time.Until(k.CooldownUntil).Truncate(time.Second).String()
			}
		}
		if !k.LastUsedAt.IsZero() && k.LastUsedAt.Unix() > 0 {
			lastUsedAt = k.LastUsedAt.Format(time.RFC3339)
		}

		out = append(out, keyItem{
			MaskedKey:     k.MaskedKey,
			KeyHash:       k.KeyHash,
			Status:        k.Status,
			TodayUsage:    k.TodayUsage,
			Limit:         k.Limit,
			TotalRequests: k.TotalRequests,
			ErrorRequests: k.ErrorRequests,
			CooldownLeft:  cooldownLeft,
			CooldownUntil: cooldownUntil,
			LastUsedAt:    lastUsedAt,
		})
	}

	writeJSON(w, http.StatusOK, out)
}

// POST /api/keys — добавить ключи пачкой.
// Тело: { "provider": "openrouter", "keys": ["sk-...", "sk-..."] }
// Либо form-encoded (обратная совместимость): provider=...&keys=sk-...\nsk-...
func (ws *WebServer) apiKeysAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Принимаем JSON или form. Различаем по Content-Type.
	var provider string
	var rawKeys []string

	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		var body struct {
			Provider string   `json:"provider"`
			Keys     []string `json:"keys"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		provider = strings.TrimSpace(body.Provider)
		rawKeys = body.Keys
	} else {
		if err := r.ParseForm(); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid form body")
			return
		}
		provider = strings.TrimSpace(r.FormValue("provider"))
		// form-режим: keys могут быть multiline-текстом, как в старом UI
		rawKeys = splitKeyLines(r.FormValue("keys"))
	}

	if provider != "openrouter" && provider != "aihubmix" && provider != "google" {
		writeAPIError(w, http.StatusBadRequest, "unknown provider")
		return
	}

	pool, ok := ws.pools[provider]
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "unknown provider")
		return
	}

	rawKeys = cleanKeyList(rawKeys)
	if len(rawKeys) == 0 {
		writeAPIError(w, http.StatusBadRequest, "no keys provided")
		return
	}

	added, err := pool.AddKeys(rawKeys)
	if err != nil {
		log.Printf("apiKeysAdd: pool.AddKeys(%s): %v", provider, err)
		writeAPIError(w, http.StatusInternalServerError, "failed to add keys")
		return
	}

	log.Printf("API: added %d new %s keys", added, provider)
	writeJSON(w, http.StatusOK, map[string]int{"added": added})
}

// POST /api/keys/bulk — enable/disable/delete набора ключей.
// Тело: { "provider": "...", "hashes": ["...","..."], "action": "enable|disable|delete" }
// Либо form-encoded (обратная совместимость).
func (ws *WebServer) apiKeysBulk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var provider, action string
	var hashes []string

	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		var body struct {
			Provider string   `json:"provider"`
			Hashes   []string `json:"hashes"`
			Action   string   `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		provider = strings.TrimSpace(body.Provider)
		action = strings.TrimSpace(body.Action)
		hashes = body.Hashes
	} else {
		if err := r.ParseForm(); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid form body")
			return
		}
		provider = strings.TrimSpace(r.FormValue("provider"))
		action = strings.TrimSpace(r.FormValue("action"))
		rawHashes := r.Form["hashes"]
		if len(rawHashes) == 1 && strings.Contains(rawHashes[0], ",") {
			rawHashes = strings.Split(rawHashes[0], ",")
		}
		hashes = rawHashes
	}

	if provider != "openrouter" && provider != "aihubmix" && provider != "google" {
		writeAPIError(w, http.StatusBadRequest, "unknown provider")
		return
	}

	pool, ok := ws.pools[provider]
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "unknown provider")
		return
	}

	hashes = cleanHashList(hashes)
	if len(hashes) == 0 || action == "" {
		writeAPIError(w, http.StatusBadRequest, "missing hashes or action")
		return
	}

	var poolErr error
	switch action {
	case "delete":
		poolErr = pool.RemoveKeys(hashes)
		log.Printf("API: bulk deleted %d %s keys", len(hashes), provider)
	case "enable":
		// "enable" = вернуть в ротацию: чекер перепроверит и поставит active.
		poolErr = pool.UpdateKeysStatus(hashes, "unchecked")
		log.Printf("API: bulk enabled %d %s keys", len(hashes), provider)
	case "disable":
		poolErr = pool.UpdateKeysStatus(hashes, "disabled")
		log.Printf("API: bulk disabled %d %s keys", len(hashes), provider)
	default:
		writeAPIError(w, http.StatusBadRequest, "unknown action")
		return
	}

	if poolErr != nil {
		log.Printf("apiKeysBulk: %s: %v", action, poolErr)
		writeAPIError(w, http.StatusInternalServerError, "database error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"action":   action,
		"affected": len(hashes),
	})
}

func (ws *WebServer) apiProxies(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			Proxies []string `json:"proxies"`
			Scheme  string   `json:"scheme"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		body.Proxies = cleanKeyList(body.Proxies)
		if len(body.Proxies) == 0 {
			writeAPIError(w, http.StatusBadRequest, "no proxies provided")
			return
		}
		added, checked, err := ws.proxies.Add(body.Proxies, body.Scheme)
		if err != nil {
			log.Printf("apiProxies add: %v", err)
			writeAPIError(w, http.StatusInternalServerError, "failed to add proxies")
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"added": added, "checked": checked})
		return
	}
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	items, err := ws.store.GetProxies(false)
	if err != nil {
		log.Printf("apiProxies list: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "proxies load failed")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (ws *WebServer) apiProxiesBulk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		IDs    []int64 `json:"ids"`
		Action string  `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(body.IDs) == 0 || body.Action == "" {
		writeAPIError(w, http.StatusBadRequest, "missing ids or action")
		return
	}
	var err error
	switch body.Action {
	case "enable":
		err = ws.store.UpdateProxiesStatus(body.IDs, "active")
	case "disable":
		err = ws.store.UpdateProxiesStatus(body.IDs, "disabled")
	case "delete":
		err = ws.store.DeleteProxies(body.IDs)
	case "recheck":
		err = ws.proxies.Recheck(body.IDs)
	default:
		writeAPIError(w, http.StatusBadRequest, "unknown action")
		return
	}
	if err != nil {
		log.Printf("apiProxiesBulk %s: %v", body.Action, err)
		writeAPIError(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"action": body.Action, "affected": len(body.IDs)})
}

func (ws *WebServer) apiProxySettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var ps store.ProxySettings
		if err := json.NewDecoder(r.Body).Decode(&ps); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if ps.Provider != "openrouter" && ps.Provider != "aihubmix" && ps.Provider != "google" {
			writeAPIError(w, http.StatusBadRequest, "unknown provider")
			return
		}
		if err := ws.store.SaveProxySettings(ps); err != nil {
			log.Printf("apiProxySettings save: %v", err)
			writeAPIError(w, http.StatusInternalServerError, "settings save failed")
			return
		}
		writeJSON(w, http.StatusOK, ps)
		return
	}
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	items := []store.ProxySettings{}
	for _, provider := range []string{"openrouter", "aihubmix", "google"} {
		ps, err := ws.store.GetProxySettings(provider)
		if err != nil {
			log.Printf("apiProxySettings get: %v", err)
			writeAPIError(w, http.StatusInternalServerError, "settings load failed")
			return
		}
		items = append(items, ps)
	}
	writeJSON(w, http.StatusOK, items)
}

func (ws *WebServer) apiProxyLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := ws.store.GetProxyLogs(limit)
	if err != nil {
		log.Printf("apiProxyLogs: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "proxy logs load failed")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (ws *WebServer) apiProxyStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	st, err := ws.store.GetProxyStats(time.Now().Add(-24 * time.Hour))
	if err != nil {
		log.Printf("apiProxyStats: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "proxy stats load failed")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (ws *WebServer) apiProxyUsage5m(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	now := time.Now()
	items, err := ws.store.GetProxyUsageBuckets(now, now.Add(-24*time.Hour), 5*time.Minute)
	if err != nil {
		log.Printf("apiProxyUsage5m: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "proxy usage load failed")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// ---- Ресурс: статистика ----

// GET /api/stats?provider= — общий снимок: general + models + keys + top_models + usage_trend.
// Формат совместим с текущим handleAPIStats, чтобы фронтенд мог стартовать с уже привычной структуры.
func (ws *WebServer) apiStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	provider, _ := providerFromRequest(r)

	general, err := ws.store.GetGeneralStats(provider)
	if err != nil {
		log.Printf("apiStats: GetGeneralStats(%s): %v", provider, err)
		writeAPIError(w, http.StatusInternalServerError, "general stats failed")
		return
	}

	modelsStats, err := ws.store.GetModelStats(provider)
	if err != nil {
		log.Printf("apiStats: GetModelStats(%s): %v", provider, err)
		writeAPIError(w, http.StatusInternalServerError, "model stats failed")
		return
	}

	keyStats, err := ws.store.GetKeyUsageStats(provider)
	if err != nil {
		log.Printf("apiStats: GetKeyUsageStats(%s): %v", provider, err)
		writeAPIError(w, http.StatusInternalServerError, "key stats failed")
		return
	}

	usageTrend := []store.ModelUsageTrend{}
	if trend, err := ws.store.GetModelUsageTrend(provider, 14); err == nil {
		usageTrend = trend
	} else {
		log.Printf("apiStats: GetModelUsageTrend(%s): %v", provider, err)
	}

	// Топ-модели (только openrouter — у aihubmix нет shir-man-ранкинга).
	topModels := []store.DBModel{}
	freeModels := []store.DBModel{}
	if provider == "openrouter" {
		topModels = ws.rankingMgr.GetTopModels()
		freeModels = ws.rankingMgr.GetFreeModels()
	} else if provider == "aihubmix" {
		freeModels = ws.rankingMgr.GetAihubmixFreeModels()
	} else {
		freeModels = ws.rankingMgr.GetGoogleFreeModels()
	}
	if topModels == nil {
		topModels = []store.DBModel{}
	}
	if freeModels == nil {
		freeModels = []store.DBModel{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"general":      general,
		"models":       modelsStats,
		"keys":         keyStats,
		"daily_limits": dailyLimits(provider, keyStats),
		"top_models":   topModels,
		"free_models":  freeModels,
		"usage_trend":  usageTrend,
		"refreshed_at": time.Now().Format("15:04:05 (02.01.2006)"),
	})
}

// GET /api/stats/models?provider= — только статистика по моделям.
func (ws *WebServer) apiStatsModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	provider, _ := providerFromRequest(r)

	stats, err := ws.store.GetModelStats(provider)
	if err != nil {
		log.Printf("apiStatsModels: GetModelStats(%s): %v", provider, err)
		writeAPIError(w, http.StatusInternalServerError, "model stats failed")
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// GET /api/stats/usage?provider=&range=N — тренд использования по дням.
// Параметр range в днях, по умолчанию 14.
func (ws *WebServer) apiStatsUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	provider, _ := providerFromRequest(r)
	days := 14
	if d := r.URL.Query().Get("range"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}

	trend, err := ws.store.GetModelUsageTrend(provider, days)
	if err != nil {
		log.Printf("apiStatsUsage: GetModelUsageTrend(%s, %d): %v", provider, days, err)
		writeAPIError(w, http.StatusInternalServerError, "usage trend failed")
		return
	}

	writeJSON(w, http.StatusOK, trend)
}

// GET /api/stats/usage/hourly?provider= — последние 24 часа по локальным часам сервера.
func (ws *WebServer) apiStatsUsageHourly(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	provider, _ := providerFromRequest(r)
	now := time.Now().In(time.Local)
	buckets, err := ws.store.GetUsageBuckets(provider, now, now.Add(-24*time.Hour), time.Hour)
	if err != nil {
		log.Printf("apiStatsUsageHourly: GetUsageBuckets(%s): %v", provider, err)
		writeAPIError(w, http.StatusInternalServerError, "usage buckets failed")
		return
	}

	writeJSON(w, http.StatusOK, buckets)
}

// GET /api/stats/usage/5m?provider= — последние 6 часов по 5 минутам.
func (ws *WebServer) apiStatsUsage5m(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	provider, _ := providerFromRequest(r)
	now := time.Now().In(time.Local)
	buckets, err := ws.store.GetUsageBuckets(provider, now, now.Add(-6*time.Hour), 5*time.Minute)
	if err != nil {
		log.Printf("apiStatsUsage5m: GetUsageBuckets(%s): %v", provider, err)
		writeAPIError(w, http.StatusInternalServerError, "usage buckets failed")
		return
	}

	writeJSON(w, http.StatusOK, buckets)
}

// GET /api/v2/requests?provider=&limit=100|all — последние запросы без payload/body.
// DELETE /api/v2/requests?provider= — очистить логи провайдера.
func (ws *WebServer) apiRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		provider, _ := providerFromRequest(r)
		deleted, err := ws.store.ClearRequestLog(provider)
		if err != nil {
			log.Printf("apiRequests: ClearRequestLog(%s): %v", provider, err)
			writeAPIError(w, http.StatusInternalServerError, "request log clear failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]int64{"deleted": deleted})
		return
	}
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	provider, _ := providerFromRequest(r)
	limit := 100
	if r.URL.Query().Get("limit") == "all" {
		limit = 0
	} else if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 500 {
		limit = n
	}

	logs, err := ws.store.GetRequestLog(provider, limit)
	if err != nil {
		log.Printf("apiRequests: GetRequestLog(%s, %d): %v", provider, limit, err)
		writeAPIError(w, http.StatusInternalServerError, "request log failed")
		return
	}

	writeJSON(w, http.StatusOK, logs)
}

// ---- Ресурс: модели ----

// GET /api/models?provider= — top + free модели для провайдера.
// Для openrouter: top_models (shir-man ранкинг) + free_models.
// Для aihubmix: free_models (aihubmix-specific).
func (ws *WebServer) apiModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	provider, _ := providerFromRequest(r)

	var topModels, freeModels []store.DBModel
	if provider == "openrouter" {
		topModels = ws.rankingMgr.GetTopModels()
		freeModels = ws.rankingMgr.GetFreeModels()
	} else if provider == "aihubmix" {
		freeModels = ws.rankingMgr.GetAihubmixFreeModels()
	} else {
		freeModels = ws.rankingMgr.GetGoogleFreeModels()
	}

	// topModels может быть nil для aihubmix — нормализуем в пустой слайс для стабильного JSON.
	if topModels == nil {
		topModels = []store.DBModel{}
	}
	if freeModels == nil {
		freeModels = []store.DBModel{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"top_models":  topModels,
		"free_models": freeModels,
	})
}

// ---- Вспомогательные функции ----

// splitKeyLines разбирает multiline-текст с ключами в слайс.
func splitKeyLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// cleanKeyList триммит и удаляет пустые/комментарные строки.
func cleanKeyList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, k := range in {
		k = strings.TrimSpace(k)
		if k == "" || strings.HasPrefix(k, "#") || strings.HasPrefix(k, "//") {
			continue
		}
		out = append(out, k)
	}
	return out
}

// cleanHashList триммит и удаляет пустые хэши.
func cleanHashList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, h := range in {
		h = strings.TrimSpace(h)
		if h != "" {
			out = append(out, h)
		}
	}
	return out
}
