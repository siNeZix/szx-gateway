package proxy

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"szx-gateway/internal/config"
	"szx-gateway/internal/keys"
	"szx-gateway/internal/models"
	"szx-gateway/internal/proxies"
	"szx-gateway/internal/store"
)

type ProxyHandler struct {
	cfg        *config.Config
	store      *store.Store
	pool       *keys.KeyPool
	rankingMgr *models.RankingManager
	client     *http.Client
	proxyPool  *proxies.Pool
}

type ChatCompletionsRequest struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream,omitempty"`
	// We preserve other fields as-is using dynamic mapping or raw JSON
}

func NewProxyHandler(cfg *config.Config, s *store.Store, p *keys.KeyPool, rm *models.RankingManager, proxyPool *proxies.Pool) *ProxyHandler {
	return &ProxyHandler{
		cfg:        cfg,
		store:      s,
		pool:       p,
		rankingMgr: rm,
		client:     &http.Client{Timeout: 10 * time.Minute}, // Large timeout for streaming/reasoning
		proxyPool:  proxyPool,
	}
}

func (ph *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	// 1. Verify Gateway Client Token
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		writeProxyError(w, http.StatusUnauthorized, "Missing or invalid Authorization header")
		return
	}
	clientToken := strings.TrimPrefix(authHeader, "Bearer ")
	if clientToken != ph.cfg.GatewayToken {
		writeProxyError(w, http.StatusUnauthorized, "Unauthorized: invalid gateway token")
		return
	}

	// 2. Route request
	if r.URL.Path == "/v1/models" && r.Method == http.MethodGet {
		ph.handleModels(w, r)
		return
	}

	if r.URL.Path == "/v1/chat/completions" && r.Method == http.MethodPost {
		ph.handleChatCompletions(w, r)
		return
	}

	writeProxyError(w, http.StatusNotFound, "Not Found")
}

func (ph *ProxyHandler) handleModels(w http.ResponseWriter, r *http.Request) {
	topModels := ph.rankingMgr.GetTopModels()

	type ModelItem struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}

	var data []ModelItem

	// Add aliases top1, top2, top3 with real model metadata if resolved
	modelMap := make(map[string]store.DBModel)
	for _, m := range topModels {
		modelMap[m.ID] = m
	}

	if resolved, ok := ph.rankingMgr.ResolveAlias("top1"); ok {
		if orig, exists := modelMap[resolved]; exists {
			data = append(data, ModelItem{
				ID:      "top1",
				Object:  "model",
				Created: orig.UpdatedAt.Unix(),
				OwnedBy: strings.Split(orig.ID, "/")[0],
			})
		} else {
			data = append(data, ModelItem{ID: "top1", Object: "model", Created: time.Now().Unix(), OwnedBy: "shir-man"})
		}
	} else {
		data = append(data, ModelItem{ID: "top1", Object: "model", Created: time.Now().Unix(), OwnedBy: "shir-man"})
	}

	if resolved, ok := ph.rankingMgr.ResolveAlias("top2"); ok {
		if orig, exists := modelMap[resolved]; exists {
			data = append(data, ModelItem{
				ID:      "top2",
				Object:  "model",
				Created: orig.UpdatedAt.Unix(),
				OwnedBy: strings.Split(orig.ID, "/")[0],
			})
		} else {
			data = append(data, ModelItem{ID: "top2", Object: "model", Created: time.Now().Unix(), OwnedBy: "shir-man"})
		}
	} else {
		data = append(data, ModelItem{ID: "top2", Object: "model", Created: time.Now().Unix(), OwnedBy: "shir-man"})
	}

	if resolved, ok := ph.rankingMgr.ResolveAlias("top3"); ok {
		if orig, exists := modelMap[resolved]; exists {
			data = append(data, ModelItem{
				ID:      "top3",
				Object:  "model",
				Created: orig.UpdatedAt.Unix(),
				OwnedBy: strings.Split(orig.ID, "/")[0],
			})
		} else {
			data = append(data, ModelItem{ID: "top3", Object: "model", Created: time.Now().Unix(), OwnedBy: "shir-man"})
		}
	} else {
		data = append(data, ModelItem{ID: "top3", Object: "model", Created: time.Now().Unix(), OwnedBy: "shir-man"})
	}

	// Add all free models from OpenRouter
	freeModels := ph.rankingMgr.GetFreeModels()
	for _, m := range freeModels {
		data = append(data, ModelItem{
			ID:      m.ID,
			Object:  "model",
			Created: m.UpdatedAt.Unix(),
			OwnedBy: strings.Split(m.ID, "/")[0],
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "list",
		"data":   data,
	})
}

func (ph *ProxyHandler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	// Read request body
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeProxyError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}

	// Decode partially to inspect model and stream
	var chatReq ChatCompletionsRequest
	if err := json.Unmarshal(bodyBytes, &chatReq); err != nil {
		writeProxyError(w, http.StatusBadRequest, "Failed to parse JSON request")
		return
	}

	originalModel := chatReq.Model
	resolvedModel := originalModel

	// Resolve model if alias (top1, top2, top3)
	if aliasModel, ok := ph.rankingMgr.ResolveAlias(originalModel); ok {
		resolvedModel = aliasModel
	}

	// Verify model is free
	if !ph.rankingMgr.IsFreeModel(resolvedModel) {
		writeProxyError(w, http.StatusBadRequest, fmt.Sprintf("Model %s is not supported (only Shir-Man free models allowed)", originalModel))
		return
	}

	// Replace model in request body bytes if resolved to a different ID
	if resolvedModel != originalModel {
		var rawMap map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &rawMap); err == nil {
			rawMap["model"] = resolvedModel
			if updatedBytes, err := json.Marshal(rawMap); err == nil {
				bodyBytes = updatedBytes
			}
		}
	}

	// Execute request with retries over different keys
	var finalErr error
	settings, _ := ph.store.GetProxySettings("openrouter")
	proxyAfter429 := false
	triedKeys := make(map[string]bool)
	for attempt := 1; attempt <= ph.cfg.MaxKeyRetries; attempt++ {
		keyState, err := ph.pool.GetBestKeyExcluding(triedKeys)
		if err != nil {
			writeProxyError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		triedKeys[keyState.KeyHash] = true

		// GetBestKey already reserved (registered) this key atomically.
		ph.pool.SyncKeyToDB(keyState)

		log.Printf("[Attempt %d/%d] Proxying %s -> %s using key %s", attempt, ph.cfg.MaxKeyRetries, originalModel, resolvedModel, keyState.MaskedKey)

		// Create OpenRouter Request
		req, err := http.NewRequest("POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(bodyBytes))
		if err != nil {
			log.Printf("Failed to create OpenRouter request: %v", err)
			writeProxyError(w, http.StatusInternalServerError, "Internal gateway error creating request")
			return
		}

		// Set Headers
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+keyState.RawKey)
		req.Header.Set("User-Agent", "OpenRouterGateway/1.0")
		req.Header.Set("HTTP-Referer", "https://shir-man.com/free-llm")
		req.Header.Set("X-Title", "OpenRouter Free Gateway")

		// We must not set connection close, let client decide or keepalive
		startTime := time.Now()
		client := ph.client
		usingProxy := settings.UseForRequests && proxies.ShouldUse(settings, proxyAfter429)
		var proxyID int64
		if usingProxy {
			client, proxyID = ph.proxyPool.Client(true, 10*time.Minute)
		}
		resp, err := client.Do(req)
		if err != nil {
			ph.logProxy(proxyID, int64(len(bodyBytes)), 0, 0, false, err.Error(), startTime)
			log.Printf("Network error making OpenRouter request: %v", err)
			if logErr := ph.store.LogRequest(&store.DBRequest{
				Timestamp:  time.Now(),
				KeyHash:    keyState.KeyHash,
				Model:      resolvedModel,
				StatusCode: 0,
				ErrorMsg:   err.Error(),
				LatencyMs:  time.Since(startTime).Milliseconds(),
				Provider:   "openrouter",
			}); logErr != nil {
				log.Printf("Failed to log request to DB: %v", logErr)
			}
			keyState.SetCooldown(30*time.Second, "")
			ph.pool.SyncKeyToDB(keyState)
			finalErr = err
			continue
		}

		// Handle key cooldown / limits based on headers and status code
		ParseRateLimits(keyState, resp.Header)
		ph.logProxyRateLimits(keyState, resp.Header)

		if resp.StatusCode >= 400 {
			// Read body to inspect if it's a credit/quota issue or upstream rate limit
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			ph.logProxy(proxyID, int64(len(bodyBytes)), int64(len(respBody)), resp.StatusCode, resp.StatusCode < 500, resp.Status, startTime)
			latencyMs := time.Since(startTime).Milliseconds()
			if err := ph.store.LogRequest(&store.DBRequest{
				Timestamp:  time.Now(),
				KeyHash:    keyState.KeyHash,
				Model:      resolvedModel,
				StatusCode: resp.StatusCode,
				ErrorMsg:   resp.Status,
				LatencyMs:  latencyMs,
				TTFTMs:     latencyMs,
				IsStream:   chatReq.Stream,
				Provider:   "openrouter",
			}); err != nil {
				log.Printf("Failed to log request to DB: %v", err)
			}

			if resp.StatusCode == http.StatusTooManyRequests && IsUpstreamRateLimit(respBody) {
				if settings.UseForRequests && settings.Mode == "after_429" && !usingProxy {
					log.Printf("Detected upstream rate-limit for model %s, retrying through proxy.", resolvedModel)
					keyState.RollbackUsage()
					ph.pool.SyncKeyToDB(keyState)
					proxyAfter429 = true
					finalErr = fmt.Errorf("upstream rate-limit, retrying through proxy")
					continue
				}
				log.Printf("Detected upstream rate-limit for model %s (provider: %s). Fast-failing without putting key %s on cooldown.", resolvedModel, "upstream", keyState.MaskedKey)
				// Set headers and forward the upstream 429 directly to client
				for k, v := range resp.Header {
					if k != "Content-Length" && k != "Content-Encoding" {
						w.Header()[k] = v
					}
				}
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write(respBody)
				return
			}

			cooldown := HandleProxyError(keyState, resp)

			// If it's explicitly "credit exhausted" or similar, mark as day_exhausted
			if IsQuotaExhaustedError(respBody) {
				log.Printf("Key %s has exhausted its quota (detected in response body). Marking day_exhausted.", keyState.MaskedKey)
				keyState.SetStatus("day_exhausted")
			}

			ph.pool.SyncKeyToDB(keyState)

			log.Printf("Request failed with status %d on key %s. Cooldown applied: %v", resp.StatusCode, keyState.MaskedKey, cooldown)
			finalErr = fmt.Errorf("openrouter returned status %d: %s", resp.StatusCode, string(respBody))

			// Retry with another key
			continue
		}

		// Success! Stream or return response
		defer resp.Body.Close()

		if chatReq.Stream {
			ph.handleStreamResponse(w, resp, keyState, resolvedModel, startTime, proxyID, int64(len(bodyBytes)))
		} else {
			ph.handleNormalResponse(w, resp, keyState, resolvedModel, startTime, proxyID, int64(len(bodyBytes)))
		}
		return
	}

	// If we exhausted all retries
	log.Printf("All %d retries failed for request. Last error: %v", ph.cfg.MaxKeyRetries, finalErr)
	writeProxyError(w, http.StatusBadGateway, fmt.Sprintf("Gateway exhausted all retries. Last error: %v", finalErr))
}

func (ph *ProxyHandler) handleNormalResponse(w http.ResponseWriter, resp *http.Response, ks *keys.KeyState, model string, startTime time.Time, proxyID, requestBytes int64) {
	// Read body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		ph.logProxy(proxyID, requestBytes, 0, resp.StatusCode, false, err.Error(), startTime)
		log.Printf("Failed to read response body: %v", err)
		writeProxyError(w, http.StatusBadGateway, "Failed to read response from upstream")
		return
	}

	// Copy Headers
	for k, v := range resp.Header {
		if k != "Content-Length" && k != "Content-Encoding" {
			w.Header()[k] = v
		}
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
	ph.logProxy(proxyID, requestBytes, int64(len(respBody)), resp.StatusCode, true, "", startTime)

	// Parse Usage
	var promptTokens, completionTokens int
	var usageStruct struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(respBody, &usageStruct); err == nil {
		promptTokens = usageStruct.Usage.PromptTokens
		completionTokens = usageStruct.Usage.CompletionTokens
	}

	latencyMs := time.Since(startTime).Milliseconds()

	// Log request to DB
	err = ph.store.LogRequest(&store.DBRequest{
		Timestamp:        time.Now(),
		KeyHash:          ks.KeyHash,
		Model:            model,
		StatusCode:       resp.StatusCode,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		LatencyMs:        latencyMs,
		TTFTMs:           latencyMs, // For non-stream requests, TTFT equals total latency
		IsStream:         false,
		Provider:         "openrouter",
	})
	if err != nil {
		log.Printf("Failed to log request to DB: %v", err)
	}
}

func (ph *ProxyHandler) handleStreamResponse(w http.ResponseWriter, resp *http.Response, ks *keys.KeyState, model string, startTime time.Time, proxyID, requestBytes int64) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Printf("ResponseWriter does not support Flusher")
		writeProxyError(w, http.StatusInternalServerError, "Streaming not supported by gateway")
		return
	}

	// Copy Headers
	for k, v := range resp.Header {
		if k != "Content-Encoding" && k != "Content-Length" {
			w.Header()[k] = v
		}
	}
	w.WriteHeader(resp.StatusCode)

	reader := bufio.NewReader(resp.Body)
	var promptTokens, completionTokens int
	var ttftMs int64
	var responseBytes int64
	hasLoggedTTFT := false

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			responseBytes += int64(len(line))
			// Write chunk to client
			_, writeErr := w.Write(line)
			if writeErr != nil {
				log.Printf("Client disconnected during stream: %v", writeErr)
				break
			}
			flusher.Flush()

			// Parse usage in stream chunks if present.
			// OpenRouter SSE stream contains lines: `data: {"id":"...","usage":{"prompt_tokens":10,"completion_tokens":20}}`
			lineStr := string(line)
			if strings.HasPrefix(lineStr, "data:") {
				dataJSON := strings.TrimPrefix(lineStr, "data:")
				dataJSON = strings.TrimSpace(dataJSON)

				if dataJSON != "[DONE]" {
					// Measure TTFT on the first data chunk
					if !hasLoggedTTFT {
						ttftMs = time.Since(startTime).Milliseconds()
						hasLoggedTTFT = true
					}

					var usageStruct struct {
						Usage struct {
							PromptTokens     int `json:"prompt_tokens"`
							CompletionTokens int `json:"completion_tokens"`
						} `json:"usage"`
					}
					if err := json.Unmarshal([]byte(dataJSON), &usageStruct); err == nil {
						if usageStruct.Usage.PromptTokens > 0 {
							promptTokens = usageStruct.Usage.PromptTokens
							completionTokens = usageStruct.Usage.CompletionTokens
						}
					}
				}
			}
		}

		if err != nil {
			if err != io.EOF {
				log.Printf("Error reading stream from upstream: %v", err)
			}
			break
		}
	}

	// If we somehow got no chunks before EOF
	if !hasLoggedTTFT {
		ttftMs = time.Since(startTime).Milliseconds()
	}

	latencyMs := time.Since(startTime).Milliseconds()
	ph.logProxy(proxyID, requestBytes, responseBytes, resp.StatusCode, true, "", startTime)

	// Log request to DB
	err := ph.store.LogRequest(&store.DBRequest{
		Timestamp:        time.Now(),
		KeyHash:          ks.KeyHash,
		Model:            model,
		StatusCode:       resp.StatusCode,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		LatencyMs:        latencyMs, // For stream, this is now the accurate total response time
		TTFTMs:           ttftMs,    // Time To First Token
		IsStream:         true,
		Provider:         "openrouter",
	})
	if err != nil {
		log.Printf("Failed to log request to DB: %v", err)
	}
}

func (ph *ProxyHandler) logProxy(proxyID, requestBytes, responseBytes int64, status int, success bool, msg string, start time.Time) {
	if proxyID == 0 {
		return
	}
	if status >= 500 || status == 0 {
		success = false
	}
	if err := ph.store.LogProxyRequest(store.DBProxyLog{
		Timestamp:     time.Now(),
		ProxyID:       proxyID,
		Provider:      "openrouter",
		UseCase:       "request",
		Success:       success,
		RequestBytes:  requestBytes,
		ResponseBytes: responseBytes,
		LatencyMs:     time.Since(start).Milliseconds(),
		ErrorMsg:      msg,
	}); err != nil {
		log.Printf("proxy request log failed: %v", err)
	}
}

func (ph *ProxyHandler) logProxyRateLimits(ks *keys.KeyState, headers http.Header) {
	var limitTotalVal, limitRemainingVal sql.NullInt64
	var resetRawVal sql.NullString

	if limStr := headers.Get("X-RateLimit-Limit"); limStr != "" {
		if lim, err := strconv.ParseInt(limStr, 10, 64); err == nil {
			limitTotalVal = sql.NullInt64{Int64: lim, Valid: true}
		}
	}
	if remStr := headers.Get("X-RateLimit-Remaining"); remStr != "" {
		if rem, err := strconv.ParseInt(remStr, 10, 64); err == nil {
			limitRemainingVal = sql.NullInt64{Int64: rem, Valid: true}
		}
	}
	if resetStr := headers.Get("X-RateLimit-Reset"); resetStr != "" {
		resetRawVal = sql.NullString{String: resetStr, Valid: true}
	}

	// Only log if we actually have some limit headers (usually at least remaining is present)
	if limitRemainingVal.Valid || limitTotalVal.Valid || resetRawVal.Valid {
		rl := &store.DBRateLimit{
			Timestamp:      time.Now(),
			KeyHash:        ks.KeyHash,
			Source:         "proxy",
			LimitTotal:     limitTotalVal,
			LimitRemaining: limitRemainingVal,
			ResetRaw:       resetRawVal,
		}
		if err := ph.store.LogRateLimit(rl); err != nil {
			log.Printf("Failed to log proxy rate limit: %v", err)
		}
	}
}
