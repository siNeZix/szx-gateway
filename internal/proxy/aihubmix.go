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
	"sync"
	"time"

	"openrouter-gateway/internal/config"
	"openrouter-gateway/internal/keys"
	"openrouter-gateway/internal/models"
	"openrouter-gateway/internal/store"
)

const aihubmixTarget = "https://aihubmix.com"
const aihubmixMaxKeySwitches = 12

var (
	aihubmixQuotaMarkers  = []string{"prevent abuse of free resources", "can only try"}
	aihubmixReturnMarkers = []string{
		"rate limited by provider", "model too many requests", "too many requests; please try again later",
		"shortage", "low-priced resources", "unknown model", "incorrect model id", "cannot be routed",
		"param incorrect", "are not supported", "not supported", "/v1/responses",
		"no available channel", "no available", "temporarily", "try again later", "overloaded",
	}
	aihubmixInvalidKeyMarkers = []string{
		"invalid key", "invalid token", "key is disabled", "token is disabled",
		"access token is invalid", "access token is invalid or expired", "please log in",
	}
	aihubmixAccountMarkers = []string{
		"insufficient_user_quota", "balance is insufficient", "insufficient balance",
		"recharge", "account suspended", "approved ip ranges",
	}
	aihubmixKeyRateMarkers = []string{
		"rate_limit_error", "rate limit", "rate-limit", "rate limited",
		"requests per", "quota exceeded", "key limit", "account limit",
	}
	aihubmixProvider429Markers = []string{
		"rate limited by provider", "provider rate", "model too many requests", "too many requests", "model overloaded",
		"model is overloaded", "overloaded", "temporarily rate-limited", "temporarily rate limited",
	}
	aihubmixKeyModelMarkers = []string{
		"not authorized to access the requested model", "not authorized to access",
	}
	aihubmixReasoningParams = []string{"reasoning_effort", "reasoningEffort", "reasoning"}
)

type aihubmixErrorAction int

const (
	aihubmixReturnToClient aihubmixErrorAction = iota
	aihubmixRetryInvalidKey
	aihubmixRetryAccountLimit
	aihubmixRetryKeyRateLimit
	aihubmixRetryProviderOnce
	aihubmixRetryKeyModelLimit
	aihubmixRetryTransient
)

type AihubmixHandler struct {
	cfg        *config.Config
	store      *store.Store
	pool       *keys.KeyPool
	rankingMgr *models.RankingManager
	client     *http.Client
	rate429Mu  sync.Mutex
	rate429    map[string]int
	rate429Day string
}

func NewAihubmixHandler(cfg *config.Config, s *store.Store, p *keys.KeyPool, rm *models.RankingManager) *AihubmixHandler {
	return &AihubmixHandler{
		cfg:        cfg,
		store:      s,
		pool:       p,
		rankingMgr: rm,
		client:     &http.Client{Timeout: 10 * time.Minute},
		rate429:    make(map[string]int),
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

	maxAttempts := h.cfg.MaxKeyRetries
	if maxAttempts <= 0 || maxAttempts > aihubmixMaxKeySwitches {
		maxAttempts = aihubmixMaxKeySwitches
	}
	provider429Retries := 0
	triedKeys := make(map[string]bool)
	var finalErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var keyState *keys.KeyState
		var err error
		if modelForLimits != "" {
			keyState, err = h.pool.GetBestKeyForModelExcluding(modelForLimits, triedKeys)
		} else {
			keyState, err = h.pool.GetBestKeyExcluding(triedKeys)
		}
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":{"message":"%v"}}`, err), http.StatusServiceUnavailable)
			return
		}
		triedKeys[keyState.KeyHash] = true
		h.pool.SyncKeyToDB(keyState)

		log.Printf("[AIHubMix %d/%d] %s %s via key %s", attempt, maxAttempts, r.Method, r.URL.Path, keyState.MaskedKey)

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
			keyState.RollbackUsage()
			h.pool.SyncKeyToDB(keyState)
			finalErr = err
			break
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
		if resp.StatusCode == 200 && containsAnyLower(firstBytes, aihubmixQuotaMarkers) {
			resp.Body.Close()
			log.Printf("[AIHubMix] key %s free-quota exhausted (account daily limit reached)", keyState.MaskedKey)
			// ponytail: лимит 10 запросов — на аккаунт, не на модель. Морозим весь ключ до конца суток.
			keyState.MarkDayExhausted()
			h.pool.SyncKeyToDB(keyState)
			continue
		}

		// 2) ошибки классифицируем по телу, не по одному status code.
		if resp.StatusCode >= 400 {
			switch classifyAIHubMixError(resp.StatusCode, firstBytes) {
			case aihubmixRetryInvalidKey:
				resp.Body.Close()
				log.Printf("[AIHubMix] key %s dead/account blocked (status %d)", keyState.MaskedKey, resp.StatusCode)
				keyState.SetStatus("invalid")
				h.pool.SyncKeyToDB(keyState)
				finalErr = fmt.Errorf("key failed with status %d", resp.StatusCode)
				continue
			case aihubmixRetryAccountLimit:
				resp.Body.Close()
				log.Printf("[AIHubMix] key %s account/key limit (status %d), trying next key", keyState.MaskedKey, resp.StatusCode)
				keyState.MarkDayExhausted()
				h.pool.SyncKeyToDB(keyState)
				finalErr = fmt.Errorf("key/account limit with status %d", resp.StatusCode)
				continue
			case aihubmixRetryKeyRateLimit:
				resp.Body.Close()
				cooldown := h.nextAIHubMix429Cooldown(keyState)
				log.Printf("[AIHubMix] key %s rate-limited for %s (status %d), trying next key", keyState.MaskedKey, cooldown, resp.StatusCode)
				keyState.RollbackUsage()
				keyState.SetCooldown(cooldown, "rate_limited")
				h.pool.SyncKeyToDB(keyState)
				finalErr = fmt.Errorf("key rate-limited with status %d", resp.StatusCode)
				continue
			case aihubmixRetryProviderOnce:
				if provider429Retries >= 3 {
					log.Printf("[AIHubMix] provider/model 429 repeated, returning upstream response")
					keyState.RollbackUsage()
					h.pool.SyncKeyToDB(keyState)
					resp.Status = "provider-rate-limit"
					h.relayResponse(w, resp, firstBytes, bufReader, keyState, modelForLimits, startTime, ttftMs, isStream, true)
					return
				}
				resp.Body.Close()
				backoff := time.Duration(1<<provider429Retries) * time.Second
				provider429Retries++
				log.Printf("[AIHubMix] provider/model 429, backing off %s before trying another key", backoff)
				keyState.RollbackUsage()
				h.pool.SyncKeyToDB(keyState)
				finalErr = fmt.Errorf("provider/model rate-limited with status %d", resp.StatusCode)
				time.Sleep(backoff)
				continue
			case aihubmixRetryKeyModelLimit:
				resp.Body.Close()
				log.Printf("[AIHubMix] key %s cannot use model %s (status %d), trying next key", keyState.MaskedKey, modelForLimits, resp.StatusCode)
				if modelForLimits != "" {
					keyState.SetModelCooldown(modelForLimits, time.Now().Add(24*time.Hour))
				}
				h.pool.SyncKeyToDB(keyState)
				finalErr = fmt.Errorf("key/model limit with status %d", resp.StatusCode)
				continue
			case aihubmixRetryTransient:
				resp.Body.Close()
				log.Printf("[AIHubMix] key %s transient upstream error (status %d), trying next key", keyState.MaskedKey, resp.StatusCode)
				keyState.RollbackUsage()
				keyState.SetCooldown(30*time.Second, "")
				h.pool.SyncKeyToDB(keyState)
				finalErr = fmt.Errorf("transient upstream error with status %d", resp.StatusCode)
				continue
			default:
				log.Printf("[AIHubMix] %d model/provider/request error (key NOT banned)", resp.StatusCode)
				keyState.RollbackUsage()
				h.pool.SyncKeyToDB(keyState)
				h.relayResponse(w, resp, firstBytes, bufReader, keyState, modelForLimits, startTime, ttftMs, isStream, true)
				return
			}
		}
		// 6) успех или прочий статус — релеим как есть
		logModel := modelForLimits
		if logModel == "" {
			logModel = r.URL.Path
		}
		h.relayResponse(w, resp, firstBytes, bufReader, keyState, logModel, startTime, ttftMs, isStream, true)
		return
	}

	log.Printf("[AIHubMix] all %d retries failed: %v", maxAttempts, finalErr)
	http.Error(w, fmt.Sprintf(`{"error":{"message":"AIHubMix gateway exhausted all retries. Last error: %v"}}`, finalErr), http.StatusBadGateway)
}

func (h *AihubmixHandler) nextAIHubMix429Cooldown(ks *keys.KeyState) time.Duration {
	h.rate429Mu.Lock()
	defer h.rate429Mu.Unlock()

	now := time.Now().UTC()
	day := now.Format("2006-01-02")
	if h.rate429Day != day {
		h.rate429 = make(map[string]int)
		h.rate429Day = day
	}

	h.rate429[ks.KeyHash]++
	switch h.rate429[ks.KeyHash] {
	case 1:
		return time.Minute
	case 2:
		return time.Hour
	default:
		return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC).Sub(now)
	}
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
		Timestamp:        time.Now(),
		KeyHash:          ks.KeyHash,
		Model:            model,
		StatusCode:       resp.StatusCode,
		ErrorMsg:         statusText(resp),
		CompletionTokens: int(tokens),
		LatencyMs:        latencyMs,
		TTFTMs:           ttftMs,
		IsStream:         isStream,
		Provider:         "aihubmix",
	}); err != nil {
		log.Printf("[AIHubMix] failed to log request: %v", err)
	}
}

func statusText(resp *http.Response) string {
	if resp.StatusCode < 400 && resp.StatusCode != 0 {
		return ""
	}
	// ponytail: upstream может прислать статус-строку без reason phrase,
	// тогда resp.Status пустой — фолбэчим на stdlib-текст по коду.
	if s := strings.TrimSpace(resp.Status); s != "" {
		return s
	}
	return http.StatusText(resp.StatusCode)
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
	if containsAnyLower(data, aihubmixInvalidKeyMarkers) {
		return true
	}
	return status == 401 && len(data) == 0
}

func classifyAIHubMixError(status int, data []byte) aihubmixErrorAction {
	text := strings.ToLower(string(data))
	var gwErr struct {
		Error struct {
			Message string      `json:"message"`
			Type    string      `json:"type"`
			Param   string      `json:"param"`
			Code    interface{} `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &gwErr) == nil {
		text = strings.ToLower(fmt.Sprintf("%s %s %s %v", gwErr.Error.Message, gwErr.Error.Type, gwErr.Error.Param, gwErr.Error.Code))
	}

	has := func(markers []string) bool {
		for _, m := range markers {
			if strings.Contains(text, m) {
				return true
			}
		}
		return false
	}

	if isKeyDead(status, data) {
		return aihubmixRetryInvalidKey
	}
	if has(aihubmixKeyModelMarkers) {
		return aihubmixRetryKeyModelLimit
	}
	if has(aihubmixAccountMarkers) {
		return aihubmixRetryAccountLimit
	}
	if status == http.StatusTooManyRequests {
		if has(aihubmixProvider429Markers) {
			return aihubmixRetryProviderOnce
		}
		if has(aihubmixKeyRateMarkers) {
			return aihubmixRetryKeyRateLimit
		}
		return aihubmixRetryProviderOnce
	}
	if has(aihubmixReturnMarkers) {
		return aihubmixReturnToClient
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return aihubmixRetryInvalidKey
	}
	if status >= 500 {
		return aihubmixRetryTransient
	}
	return aihubmixReturnToClient
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
