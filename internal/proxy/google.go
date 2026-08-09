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

	"szx-gateway/internal/config"
	"szx-gateway/internal/firewall"
	"szx-gateway/internal/keys"
	"szx-gateway/internal/limits"
	"szx-gateway/internal/models"
	"szx-gateway/internal/proxies"
	"szx-gateway/internal/store"
)

const googleTarget = "https://generativelanguage.googleapis.com/v1beta"
const googleMaxKeySwitches = 12

type GoogleHandler struct {
	cfg        *config.Config
	store      *store.Store
	pool       *keys.KeyPool
	rankingMgr *models.RankingManager
	client     *http.Client
	proxyPool  *proxies.Pool
}

func NewGoogleHandler(cfg *config.Config, s *store.Store, p *keys.KeyPool, rm *models.RankingManager, proxyPool *proxies.Pool) *GoogleHandler {
	return &GoogleHandler{
		cfg:        cfg,
		store:      s,
		pool:       p,
		rankingMgr: rm,
		client:     &http.Client{Timeout: 10 * time.Minute},
		proxyPool:  proxyPool,
	}
}

func (h *GoogleHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		writeProxyError(w, http.StatusUnauthorized, "Missing or invalid Authorization header")
		return
	}
	clientToken := strings.TrimPrefix(authHeader, "Bearer ")
	if clientToken != h.cfg.GatewayToken {
		writeProxyError(w, http.StatusUnauthorized, "Unauthorized: invalid gateway token")
		return
	}

	if r.URL.Path == "/v1/models" && r.Method == http.MethodGet {
		h.handleModels(w, r)
		return
	}

	if r.URL.Path == "/v1/chat/completions" && r.Method == http.MethodPost {
		h.handleChatCompletions(w, r)
		return
	}

	writeProxyError(w, http.StatusNotFound, "Not Found")
}

func (h *GoogleHandler) handleModels(w http.ResponseWriter, r *http.Request) {
	type ModelItem struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}

	free := h.rankingMgr.GetGoogleFreeModels()
	data := make([]ModelItem, 0, len(free))
	for _, m := range free {
		data = append(data, ModelItem{
			ID:      m.ID,
			Object:  "model",
			Created: m.UpdatedAt.Unix(),
			OwnedBy: "google",
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "list",
		"data":   data,
	})
}

// ---- OpenAI -> Gemini конвертация ----

type openAIMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type openAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Stream      bool            `json:"stream,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	MaxTokens   *int64          `json:"max_tokens,omitempty"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiGenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	MaxOutputTokens *int64   `json:"maxOutputTokens,omitempty"`
}

type geminiRequest struct {
	Contents          []geminiContent         `json:"contents"`
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

func extractText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var sb strings.Builder
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					sb.WriteString(t)
				}
			}
		}
		return sb.String()
	default:
		b, _ := json.Marshal(content)
		return string(b)
	}
}

func openAIToGemini(req *openAIChatRequest) *geminiRequest {
	var systemText string
	var contents []geminiContent

	for _, msg := range req.Messages {
		text := extractText(msg.Content)
		switch msg.Role {
		case "system":
			if systemText != "" {
				systemText += "\n"
			}
			systemText += text
		case "user":
			contents = append(contents, geminiContent{Role: "user", Parts: []geminiPart{{Text: text}}})
		case "assistant":
			contents = append(contents, geminiContent{Role: "model", Parts: []geminiPart{{Text: text}}})
		default:
			contents = append(contents, geminiContent{Role: "user", Parts: []geminiPart{{Text: text}}})
		}
	}

	gr := &geminiRequest{Contents: contents}
	if systemText != "" {
		gr.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: systemText}}}
	}
	if req.Temperature != nil || req.TopP != nil || req.MaxTokens != nil {
		gr.GenerationConfig = &geminiGenerationConfig{
			Temperature:     req.Temperature,
			TopP:            req.TopP,
			MaxOutputTokens: req.MaxTokens,
		}
	}
	return gr
}

// ---- Gemini -> OpenAI конвертация ----

type geminiCandidate struct {
	Content struct {
		Parts []geminiPart `json:"parts"`
		Role  string       `json:"role"`
	} `json:"content"`
	FinishReason string `json:"finishReason"`
}

type geminiUsageMetadata struct {
	PromptTokenCount     int64 `json:"promptTokenCount"`
	CandidatesTokenCount int64 `json:"candidatesTokenCount"`
	TotalTokenCount      int64 `json:"totalTokenCount"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate   `json:"candidates"`
	UsageMetadata geminiUsageMetadata `json:"usageMetadata"`
}

func geminiToOpenAI(gr *geminiResponse, model string) map[string]any {
	var text string
	var finishReason string
	if len(gr.Candidates) > 0 {
		for _, p := range gr.Candidates[0].Content.Parts {
			text += p.Text
		}
		finishReason = gr.Candidates[0].FinishReason
	}

	finish := "stop"
	if finishReason == "MAX_TOKENS" {
		finish = "length"
	} else if finishReason == "SAFETY" {
		finish = "content_filter"
	}

	return map[string]any{
		"object": "chat.completion",
		"model":  model,
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": text,
			},
			"finish_reason": finish,
		}},
		"usage": map[string]any{
			"prompt_tokens":     gr.UsageMetadata.PromptTokenCount,
			"completion_tokens": gr.UsageMetadata.CandidatesTokenCount,
			"total_tokens":      gr.UsageMetadata.TotalTokenCount,
		},
	}
}

// ---- Streaming: Gemini SSE -> OpenAI SSE ----

type geminiStreamCandidate struct {
	Content struct {
		Parts []geminiPart `json:"parts"`
		Role  string       `json:"role"`
	} `json:"content"`
}

type geminiStreamChunk struct {
	Candidates    []geminiStreamCandidate `json:"candidates"`
	UsageMetadata *geminiUsageMetadata    `json:"usageMetadata,omitempty"`
}

func writeOpenAIStreamChunk(w http.ResponseWriter, flusher http.Flusher, model, content string, finish *string) {
	choice := map[string]any{
		"index": 0,
		"delta": map[string]any{},
	}
	if content != "" {
		choice["delta"] = map[string]any{"content": content}
	}
	if finish != nil {
		choice["finish_reason"] = *finish
	} else {
		choice["finish_reason"] = nil
	}

	chunk := map[string]any{
		"object":  "chat.completion.chunk",
		"model":   model,
		"choices": []map[string]any{choice},
	}
	data, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

func streamGeminiToOpenAI(w http.ResponseWriter, reader *bufio.Reader, model string) (int64, int64) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	if flusher == nil {
		return 0, 0
	}

	// initial role chunk
	writeOpenAIStreamChunk(w, flusher, model, "", nil)

	var promptTokens, completionTokens int64

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimSpace(line)
			if len(line) > 0 {
				var chunk geminiStreamChunk
				if json.Unmarshal(line, &chunk) == nil {
					for _, c := range chunk.Candidates {
						for _, p := range c.Content.Parts {
							if p.Text != "" {
								writeOpenAIStreamChunk(w, flusher, model, p.Text, nil)
							}
						}
					}
					if chunk.UsageMetadata != nil {
						promptTokens = chunk.UsageMetadata.PromptTokenCount
						completionTokens = chunk.UsageMetadata.CandidatesTokenCount
					}
				}
			}
		}
		if err != nil {
			break
		}
	}

	finish := "stop"
	writeOpenAIStreamChunk(w, flusher, model, "", &finish)
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()

	return promptTokens, completionTokens
}

// ---- Основной handler ----

func (h *GoogleHandler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeProxyError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}
	r.Body.Close()

	var chatReq openAIChatRequest
	if err := json.Unmarshal(bodyBytes, &chatReq); err != nil {
		writeProxyError(w, http.StatusBadRequest, "Failed to parse JSON request")
		return
	}

	if chatReq.Model == "" || !h.rankingMgr.IsGoogleFreeModel(chatReq.Model) {
		writeProxyError(w, http.StatusBadRequest, fmt.Sprintf("Model %s is not supported (only Google free models allowed)", chatReq.Model))
		return
	}

	gemReq := openAIToGemini(&chatReq)
	gemBody, err := json.Marshal(gemReq)
	if err != nil {
		writeProxyError(w, http.StatusInternalServerError, "Failed to convert request")
		return
	}

	// Firewall
	if h.cfg.FirewallEnabled {
		v := firewall.InspectRequest(gemBody, h.cfg.FirewallBlock, h.cfg.FirewallRedact)
		if v.Action == firewall.Block {
			log.Printf("[firewall] google request blocked: %s (types: %v)", strings.Join(v.Reasons, "; "), v.SecretTypes)
			writeProxyError(w, http.StatusForbidden, "Request blocked by firewall: "+strings.Join(v.Reasons, "; "))
			return
		}
		if v.Action == firewall.Redact {
			gemBody = v.Body
		}
	}

	maxAttempts := h.cfg.MaxKeyRetries
	if maxAttempts <= 0 || maxAttempts > googleMaxKeySwitches {
		maxAttempts = googleMaxKeySwitches
	}

	settings, _ := h.store.GetProxySettings("google")
	proxyAfter429 := false
	triedKeys := make(map[string]bool)
	var finalErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		keyState, err := h.pool.GetBestKeyForModelExcluding(chatReq.Model, triedKeys)
		if err != nil {
			writeProxyError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		triedKeys[keyState.KeyHash] = true
		limit, _ := limits.GoogleFree(chatReq.Model)
		reservedModelUsage, err := h.store.ReserveModelUsage("google", keyState.KeyHash, chatReq.Model, time.Now(), limit.RequestsDay)
		if err != nil {
			writeProxyError(w, http.StatusInternalServerError, "Failed to reserve Google daily quota")
			return
		}
		if !reservedModelUsage {
			continue
		}
		h.pool.SyncKeyToDB(keyState)

		log.Printf("[Google %d/%d] %s via key %s", attempt, maxAttempts, chatReq.Model, keyState.MaskedKey)

		action := "generateContent"
		if chatReq.Stream {
			action = "streamGenerateContent"
		}
		targetURL := fmt.Sprintf("%s/models/%s:%s?key=%s", googleTarget, chatReq.Model, action, keyState.RawKey)
		if chatReq.Stream {
			targetURL += "&alt=sse"
		}

		req, err := http.NewRequest("POST", targetURL, bytes.NewReader(gemBody))
		if err != nil {
			keyState.RollbackUsage()
			_ = h.store.RollbackModelUsage("google", keyState.KeyHash, chatReq.Model, time.Now())
			h.pool.SyncKeyToDB(keyState)
			writeProxyError(w, http.StatusInternalServerError, "Internal gateway error creating request")
			return
		}
		req.Header.Set("Content-Type", "application/json")

		startTime := time.Now()
		client := h.client
		usingProxy := settings.UseForRequests && proxies.ShouldUse(settings, proxyAfter429)
		var proxyID int64
		if usingProxy {
			client, proxyID = h.proxyPool.Client(true, 10*time.Minute)
		}

		resp, err := client.Do(req)
		if err != nil {
			h.logProxy(proxyID, int64(len(gemBody)), 0, 0, false, err.Error(), startTime)
			log.Printf("[Google] network error: %v", err)
			keyState.RollbackUsage()
			_ = h.store.RollbackModelUsage("google", keyState.KeyHash, chatReq.Model, time.Now())
			h.pool.SyncKeyToDB(keyState)
			finalErr = err
			continue
		}

		if resp.StatusCode >= 400 {
			bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()

			switch resp.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				log.Printf("[Google] key %s invalid (status %d)", keyState.MaskedKey, resp.StatusCode)
				keyState.RollbackUsage()
				_ = h.store.RollbackModelUsage("google", keyState.KeyHash, chatReq.Model, time.Now())
				keyState.SetStatus("invalid")
				h.pool.SyncKeyToDB(keyState)
				finalErr = fmt.Errorf("key invalid with status %d", resp.StatusCode)
				continue
			case http.StatusTooManyRequests:
				log.Printf("[Google] key %s rate-limited (status %d)", keyState.MaskedKey, resp.StatusCode)
				keyState.RollbackUsage()
				_ = h.store.RollbackModelUsage("google", keyState.KeyHash, chatReq.Model, time.Now())
				keyState.SetCooldown(5*time.Minute, "rate_limited")
				h.pool.SyncKeyToDB(keyState)
				finalErr = fmt.Errorf("key rate-limited with status %d", resp.StatusCode)
				continue
			case http.StatusBadRequest:
				// Модель не поддерживается этим ключом или плохой запрос
				if strings.Contains(strings.ToLower(string(bodySnippet)), "not found") || strings.Contains(strings.ToLower(string(bodySnippet)), "does not support") {
					log.Printf("[Google] key %s model %s not available", keyState.MaskedKey, chatReq.Model)
					keyState.RollbackUsage()
					_ = h.store.RollbackModelUsage("google", keyState.KeyHash, chatReq.Model, time.Now())
					keyState.SetModelCooldown(chatReq.Model, time.Now().Add(24*time.Hour))
					h.pool.SyncKeyToDB(keyState)
					finalErr = fmt.Errorf("model not available for key")
					continue
				}
				// Прочие 400 — возвращаем клиенту
				log.Printf("[Google] bad request (status 400)")
				keyState.RollbackUsage()
				_ = h.store.RollbackModelUsage("google", keyState.KeyHash, chatReq.Model, time.Now())
				h.pool.SyncKeyToDB(keyState)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				w.Write(bodySnippet)
				h.logRequest(keyState, chatReq.Model, 400, 0, startTime, false, proxyID, int64(len(gemBody)))
				return
			default:
				if resp.StatusCode >= 500 {
					log.Printf("[Google] key %s transient upstream error (status %d)", keyState.MaskedKey, resp.StatusCode)
					keyState.RollbackUsage()
					_ = h.store.RollbackModelUsage("google", keyState.KeyHash, chatReq.Model, time.Now())
					keyState.SetCooldown(30*time.Second, "")
					h.pool.SyncKeyToDB(keyState)
					finalErr = fmt.Errorf("transient upstream error with status %d", resp.StatusCode)
					continue
				}
				// Прочие ошибки — релеим
				log.Printf("[Google] status %d, relaying to client", resp.StatusCode)
				keyState.RollbackUsage()
				_ = h.store.RollbackModelUsage("google", keyState.KeyHash, chatReq.Model, time.Now())
				h.pool.SyncKeyToDB(keyState)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(resp.StatusCode)
				w.Write(bodySnippet)
				h.logRequest(keyState, chatReq.Model, resp.StatusCode, 0, startTime, false, proxyID, int64(len(gemBody)))
				return
			}
		}

		// Успех
		isStream := chatReq.Stream

		if isStream {
			reader := bufio.NewReader(resp.Body)
			promptTokens, completionTokens := streamGeminiToOpenAI(w, reader, chatReq.Model)
			resp.Body.Close()
			totalTokens := promptTokens + completionTokens
			h.logRequest(keyState, chatReq.Model, 200, totalTokens, startTime, true, proxyID, int64(len(gemBody)))
			h.logProxy(proxyID, int64(len(gemBody)), 0, 200, true, "", startTime)
			return
		}

		// Non-stream
		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Printf("[Google] failed to read response: %v", err)
			keyState.RollbackUsage()
			_ = h.store.RollbackModelUsage("google", keyState.KeyHash, chatReq.Model, time.Now())
			h.pool.SyncKeyToDB(keyState)
			finalErr = err
			continue
		}

		var gemResp geminiResponse
		if err := json.Unmarshal(respBody, &gemResp); err != nil {
			log.Printf("[Google] failed to decode Gemini response: %v", err)
			keyState.RollbackUsage()
			_ = h.store.RollbackModelUsage("google", keyState.KeyHash, chatReq.Model, time.Now())
			h.pool.SyncKeyToDB(keyState)
			finalErr = err
			continue
		}

		openAIResp := geminiToOpenAI(&gemResp, chatReq.Model)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(openAIResp)

		totalTokens := gemResp.UsageMetadata.TotalTokenCount
		h.logRequest(keyState, chatReq.Model, 200, totalTokens, startTime, false, proxyID, int64(len(gemBody)))
		h.logProxy(proxyID, int64(len(gemBody)), int64(len(respBody)), 200, true, "", startTime)
		return
	}

	log.Printf("[Google] all %d retries failed: %v", maxAttempts, finalErr)
	writeProxyError(w, http.StatusBadGateway, fmt.Sprintf("Google gateway exhausted all retries. Last error: %v", finalErr))
}

func (h *GoogleHandler) logRequest(ks *keys.KeyState, model string, statusCode int, tokens int64, startTime time.Time, isStream bool, proxyID, requestBytes int64) {
	latencyMs := time.Since(startTime).Milliseconds()
	if err := h.store.LogRequest(&store.DBRequest{
		Timestamp:        time.Now(),
		KeyHash:          ks.KeyHash,
		Model:            model,
		StatusCode:       statusCode,
		CompletionTokens: int(tokens),
		LatencyMs:        latencyMs,
		TTFTMs:           latencyMs,
		IsStream:         isStream,
		Provider:         "google",
	}); err != nil {
		log.Printf("[Google] failed to log request: %v", err)
	}

	var errCount int
	if statusCode >= 400 {
		errCount = 1
	}
	tokForAgg := tokens
	if statusCode >= 400 {
		tokForAgg = 0
	}
	if err := h.store.AddModelUsage("google", ks.KeyHash, model, time.Now(), 0, tokForAgg, latencyMs, errCount); err != nil {
		log.Printf("[Google] failed to log model usage: %v", err)
	}
}

func (h *GoogleHandler) logProxy(proxyID, requestBytes, responseBytes int64, status int, success bool, msg string, start time.Time) {
	if proxyID == 0 {
		return
	}
	if status >= 500 || status == 0 {
		success = false
	}
	if err := h.store.LogProxyRequest(store.DBProxyLog{
		Timestamp:     time.Now(),
		ProxyID:       proxyID,
		Provider:      "google",
		UseCase:       "request",
		Success:       success,
		RequestBytes:  requestBytes,
		ResponseBytes: responseBytes,
		LatencyMs:     time.Since(start).Milliseconds(),
		ErrorMsg:      msg,
	}); err != nil {
		log.Printf("[Google] proxy request log failed: %v", err)
	}
}
