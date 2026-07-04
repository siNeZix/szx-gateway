package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"openrouter-gateway/internal/config"
	"openrouter-gateway/internal/keys"
	"openrouter-gateway/internal/models"
	"openrouter-gateway/internal/store"
)

const aihubmixTarget = "https://aihubmix.com"

var (
	aihubmixQuotaMarkers = []string{"prevent abuse of free resources", "can only try"}
	aihubmixRelayMarkers = []string{
		"shortage", "low-priced resources", "rate limited by provider",
		"unknown model", "incorrect model id", "cannot be routed",
		"param incorrect", "are not supported", "/v1/responses",
		"insufficient", "recharge", "no available channel", "no available",
		"temporarily", "try again later", "overloaded",
	}
	aihubmixKeyDeadMarkers = []string{
		"invalid key", "invalid token", "key is disabled", "token is disabled",
		"unauthorized", "please log in",
	}
	aihubmixReasoningParams = []string{"reasoning_effort", "reasoningEffort", "reasoning"}
)

type AihubmixHandler struct {
	cfg        *config.Config
	store      *store.Store
	pool       *keys.KeyPool
	rankingMgr *models.RankingManager
	client     *http.Client
}

func NewAihubmixHandler(cfg *config.Config, s *store.Store, p *keys.KeyPool, rm *models.RankingManager) *AihubmixHandler {
	return &AihubmixHandler{
		cfg:        cfg,
		store:      s,
		pool:       p,
		rankingMgr: rm,
		client:     &http.Client{Timeout: 10 * time.Minute},
	}
}

func (h *AihubmixHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, `{"error":{"message":"Missing or invalid Authorization header"}}`, http.StatusUnauthorized)
		return
	}
	clientToken := strings.TrimPrefix(authHeader, "Bearer ")
	if clientToken != h.cfg.GatewayToken {
		http.Error(w, `{"error":{"message":"Unauthorized: invalid gateway token"}}`, http.StatusUnauthorized)
		return
	}

	if r.URL.Path == "/v1/models" && r.Method == http.MethodGet {
		h.handleModels(w, r)
		return
	}

	h.proxyWithRetry(w, r)
}

func (h *AihubmixHandler) handleModels(w http.ResponseWriter, r *http.Request) {
	type ModelItem struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}

	free := h.rankingMgr.GetAihubmixFreeModels()
	data := make([]ModelItem, 0, len(free))
	for _, m := range free {
		data = append(data, ModelItem{
			ID:      m.ID,
			Object:  "model",
			Created: m.UpdatedAt.Unix(),
			OwnedBy: "aihubmix",
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "list",
		"data":   data,
	})
}

func (h *AihubmixHandler) proxyWithRetry(w http.ResponseWriter, r *http.Request) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":{"message":"Failed to read request body"}}`, http.StatusBadRequest)
		return
	}
	r.Body.Close()

	// На /chat/completions всегда парсим model и валидируем против free-кэша.
	// Без жёсткой проверки некорректный/пустой Content-Type обходил фильтр.
	modelForLimits := ""
	if strings.Contains(r.URL.Path, "/chat/completions") {
		var peek struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(bodyBytes, &peek); err != nil {
			http.Error(w, `{"error":{"message":"Invalid JSON body"}}`, http.StatusBadRequest)
			return
		}
		if peek.Model == "" || !h.rankingMgr.IsAihubmixFreeModel(peek.Model) {
			http.Error(w, fmt.Sprintf(`{"error":{"message":"Model %s is not supported (only AIHubMix free models allowed)"}}`, peek.Model), http.StatusBadRequest)
			return
		}
		modelForLimits = peek.Model
	}

	// reasoning-fix: на /chat/completions при наличии tools вырезаем reasoning-параметры,
	// иначе gpt-5.5 кидает "Function tools with reasoning_effort are not supported".
	bodyBytes = sanitizeReasoning(bodyBytes, r.URL.Path, r.Header.Get("Content-Type"))

	hopHeaders := map[string]bool{
		"host": true, "connection": true, "keep-alive": true,
		"proxy-authenticate": true, "proxy-authorization": true,
		"te": true, "trailers": true, "transfer-encoding": true,
		"upgrade": true, "content-length": true, "authorization": true,
	}

	upstreamHeaders := make(http.Header)
	for k, v := range r.Header {
		if !hopHeaders[strings.ToLower(k)] {
			for _, vv := range v {
				upstreamHeaders.Add(k, vv)
			}
		}
	}

	var finalErr error
	for attempt := 1; attempt <= h.cfg.MaxKeyRetries; attempt++ {
		var keyState *keys.KeyState
		var err error
		if modelForLimits != "" {
			keyState, err = h.pool.GetBestKeyForModel(modelForLimits)
		} else {
			keyState, err = h.pool.GetBestKey()
		}
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":{"message":"%v"}}`, err), http.StatusServiceUnavailable)
			return
		}
		h.pool.SyncKeyToDB(keyState)

		log.Printf("[AIHubMix %d/%d] %s %s via key %s", attempt, h.cfg.MaxKeyRetries, r.Method, r.URL.Path, keyState.MaskedKey)

		targetURL := aihubmixTarget + r.URL.Path
		if r.URL.RawQuery != "" {
			targetURL += "?" + r.URL.RawQuery
		}

		req, err := http.NewRequest(r.Method, targetURL, bytes.NewReader(bodyBytes))
		if err != nil {
			http.Error(w, `{"error":{"message":"Internal gateway error creating request"}}`, http.StatusInternalServerError)
			return
		}
		req.Header = upstreamHeaders
		req.Header.Set("Authorization", "Bearer "+keyState.RawKey)

		startTime := time.Now()
		resp, err := h.client.Do(req)
		if err != nil {
			log.Printf("[AIHubMix] network error: %v", err)
			h.freezeModelOrKey(keyState, modelForLimits, "network")
			finalErr = err
			continue
		}

		// Читаем первый чанк — нужен для детекта free-квоты (HTTP 200-заглушка)
		// и классификации ошибок по телу. ReadAll продвигает reader, поэтому
		// в relay передаём firstBytes отдельно + bufReader с остатком body.
		bufReader := bufio.NewReader(resp.Body)
		firstBytes, _ := io.ReadAll(io.LimitReader(bufReader, 8192))
		// TTFT: время до получения первого чанка от upstream.
		// Для не-стрим ответов это близко к полной латенси; для стримов — честный TTFT.
		ttftMs := time.Since(startTime).Milliseconds()
		isStream := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")

		// 1) free-квота исчерпана: HTTP 200 с заглушкой "...prevent abuse... can only try..."
		if resp.StatusCode == 200 && containsAny(firstBytes, aihubmixQuotaMarkers) {
			resp.Body.Close()
			log.Printf("[AIHubMix] key %s free-quota exhausted (account daily limit reached)", keyState.MaskedKey)
			// ponytail: лимит 10 запросов — на аккаунт, не на модель. Морозим весь ключ до конца суток.
			keyState.MarkDayExhausted()
			h.pool.SyncKeyToDB(keyState)
			continue
		}

		// 2) ошибка уровня модели/провайдера/биллинга — не вина ключа, отдаём клиенту
		if resp.StatusCode >= 400 && containsAnyLower(firstBytes, aihubmixRelayMarkers) {
			log.Printf("[AIHubMix] key %s → %d model/provider error (key NOT banned)", keyState.MaskedKey, resp.StatusCode)
			h.relayResponse(w, resp, firstBytes, bufReader, keyState, r.URL.Path, startTime, ttftMs, isStream, true)
			return
		}

		// 3) ключ мёртв (invalid/disabled)
		if isKeyDead(resp.StatusCode, firstBytes) {
			resp.Body.Close()
			log.Printf("[AIHubMix] key %s dead (status %d)", keyState.MaskedKey, resp.StatusCode)
			keyState.SetStatus("invalid")
			h.pool.SyncKeyToDB(keyState)
			continue
		}

		// 4) 402/429 — лимит: морозим модель, ротация
		if resp.StatusCode == 402 || resp.StatusCode == 429 {
			resp.Body.Close()
			log.Printf("[AIHubMix] key %s limited (status %d) for model %s", keyState.MaskedKey, resp.StatusCode, modelForLimits)
			h.freezeModelOrKey(keyState, modelForLimits, "rate_limit")
			continue
		}

		// 5) 401/403 без явного "мёртв" — тоже ротация
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			resp.Body.Close()
			log.Printf("[AIHubMix] key %s auth failed (status %d)", keyState.MaskedKey, resp.StatusCode)
			keyState.SetStatus("invalid")
			h.pool.SyncKeyToDB(keyState)
			continue
		}

		// 6) 5xx — upstream упал: морозим модель, ротация
		if resp.StatusCode >= 500 {
			resp.Body.Close()
			log.Printf("[AIHubMix] key %s upstream 5xx (status %d) for model %s", keyState.MaskedKey, resp.StatusCode, modelForLimits)
			h.freezeModelOrKey(keyState, modelForLimits, "upstream_5xx")
			continue
		}

		// 6) успех или прочий статус — релеим как есть
		logModel := modelForLimits
		if logModel == "" {
			logModel = r.URL.Path
		}
		h.relayResponse(w, resp, firstBytes, bufReader, keyState, logModel, startTime, ttftMs, isStream, true)
		return
	}

	log.Printf("[AIHubMix] all %d retries failed: %v", h.cfg.MaxKeyRetries, finalErr)
	http.Error(w, fmt.Sprintf(`{"error":{"message":"AIHubMix gateway exhausted all retries. Last error: %v"}}`, finalErr), http.StatusBadGateway)
}

func (h *AihubmixHandler) relayResponse(w http.ResponseWriter, resp *http.Response, firstBytes []byte, reader *bufio.Reader, ks *keys.KeyState, model string, startTime time.Time, ttftMs int64, isStream, recordRequest bool) {
	defer resp.Body.Close()
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	flusher, _ := w.(http.Flusher)
	tokens := usageTokens(firstBytes)
	if len(firstBytes) > 0 {
		w.Write(firstBytes)
		if flusher != nil {
			flusher.Flush()
		}
	}
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if n := usageTokens(line); n > 0 {
				tokens = n
			}
			w.Write(line)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			break
		}
	}

	if !recordRequest {
		return
	}

	latencyMs := time.Since(startTime).Milliseconds()
	isSuccess := resp.StatusCode == 200

	// Агрегируем в model_usage: запросы/токены/латенси/ошибки.
	// Запрос всегда учитывается; токены — только на успехе (при ошибке их нет).
	var errCount int
	if !isSuccess {
		errCount = 1
	}
	tokForAgg := tokens
	if !isSuccess {
		tokForAgg = 0
	}
	if strings.HasSuffix(model, "-free") {
		if err := h.store.AddModelUsage("aihubmix", ks.KeyHash, model, time.Now(), 1, tokForAgg, latencyMs, errCount); err != nil {
			log.Printf("[AIHubMix] failed to log model usage: %v", err)
		}
	}

	if err := h.store.LogRequest(&store.DBRequest{
		Timestamp:  time.Now(),
		KeyHash:    ks.KeyHash,
		Model:      model,
		StatusCode: resp.StatusCode,
		LatencyMs:  latencyMs,
		TTFTMs:     ttftMs,
		IsStream:   isStream,
		Provider:   "aihubmix",
	}); err != nil {
		log.Printf("[AIHubMix] failed to log request: %v", err)
	}
}

func usageTokens(data []byte) int64 {
	s := strings.TrimSpace(string(data))
	if strings.HasPrefix(s, "data:") {
		s = strings.TrimSpace(strings.TrimPrefix(s, "data:"))
	}
	if s == "" || s == "[DONE]" {
		return 0
	}
	var v struct {
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return 0
	}
	return v.Usage.PromptTokens + v.Usage.CompletionTokens
}

func sanitizeReasoning(body []byte, path, contentType string) []byte {
	if !strings.Contains(strings.ToLower(contentType), "json") {
		return body
	}
	if !strings.Contains(path, "/chat/completions") {
		return body
	}
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return body
	}
	if data["tools"] == nil && data["functions"] == nil {
		return body
	}
	removed := false
	for _, k := range aihubmixReasoningParams {
		if _, ok := data[k]; ok {
			delete(data, k)
			removed = true
		}
	}
	if !removed {
		return body
	}
	out, err := json.Marshal(data)
	if err != nil {
		return body
	}
	return out
}

func containsAny(data []byte, markers []string) bool {
	s := string(data)
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func containsAnyLower(data []byte, markers []string) bool {
	s := strings.ToLower(string(data))
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func isKeyDead(status int, data []byte) bool {
	if containsAnyLower(data, aihubmixKeyDeadMarkers) {
		return true
	}
	return status == 401 && len(data) == 0
}

func copyHeaders(dst, src http.Header) {
	skip := map[string]bool{
		"content-length":    true,
		"content-encoding":  true,
		"transfer-encoding": true,
		"connection":        true,
	}
	for k, v := range src {
		if skip[strings.ToLower(k)] {
			continue
		}
		for _, vv := range v {
			dst.Add(k, vv)
		}
	}
}

// freezeModelOrKey замораживает конкретную модель ключа (1 мин → до конца суток).
// Если модель неизвестна (не /chat/completions), гасит весь ключ в cooldown.
func (h *AihubmixHandler) freezeModelOrKey(keyState *keys.KeyState, model, reason string) {
	if model != "" {
		dur, err := h.store.FreezeModel("aihubmix", keyState.KeyHash, model, time.Now())
		if err != nil {
			log.Printf("[AIHubMix] failed to freeze model %s: %v", model, err)
			keyState.SetCooldown(time.Minute, "")
		} else {
			keyState.SetModelCooldown(model, time.Now().Add(dur))
			log.Printf("[AIHubMix] key %s model %s frozen (%s) for %v", keyState.MaskedKey, model, reason, dur)
		}
	} else {
		keyState.SetCooldown(time.Minute, "")
	}
	h.pool.SyncKeyToDB(keyState)
}
