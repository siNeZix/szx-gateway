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
	"szx-gateway/internal/keys"
	"szx-gateway/internal/models"
	"szx-gateway/internal/proxies"
	"szx-gateway/internal/store"
)

const oneMinAITarget = "https://api.1min.ai/api/chat-with-ai"

type OneMinAIHandler struct {
	cfg       *config.Config
	store     *store.Store
	pool      *keys.KeyPool
	ranking   *models.RankingManager
	client    *http.Client
	proxyPool *proxies.Pool
}

func NewOneMinAIHandler(cfg *config.Config, s *store.Store, p *keys.KeyPool, rm *models.RankingManager, proxyPool *proxies.Pool) *OneMinAIHandler {
	return &OneMinAIHandler{cfg: cfg, store: s, pool: p, ranking: rm, client: &http.Client{Timeout: 10 * time.Minute}, proxyPool: proxyPool}
}

func (h *OneMinAIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") != h.cfg.GatewayToken {
		writeProxyError(w, http.StatusUnauthorized, "Unauthorized: invalid gateway token")
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		h.models(w)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
		h.chat(w, r)
	default:
		writeProxyError(w, http.StatusNotFound, "Not Found")
	}
}

func (h *OneMinAIHandler) models(w http.ResponseWriter) {
	data := make([]map[string]any, 0)
	for _, model := range h.ranking.GetOneMinAIModels() {
		data = append(data, map[string]any{"id": model.ID, "object": "model", "created": model.UpdatedAt.Unix(), "owned_by": "1min.ai"})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

type oneMinOpenAIRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	} `json:"messages"`
	Stream bool `json:"stream"`
}

func (h *OneMinAIHandler) chat(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeProxyError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}
	var in oneMinOpenAIRequest
	if err := json.Unmarshal(body, &in); err != nil || in.Model == "" || len(in.Messages) == 0 {
		writeProxyError(w, http.StatusBadRequest, "Invalid OpenAI chat completion request")
		return
	}
	if !h.ranking.IsOneMinAIModel(in.Model) {
		writeProxyError(w, http.StatusBadRequest, fmt.Sprintf("Model %s is not supported by 1min.AI", in.Model))
		return
	}
	prompt, err := oneMinPrompt(in.Messages)
	if err != nil {
		writeProxyError(w, http.StatusBadRequest, err.Error())
		return
	}
	payload, _ := json.Marshal(map[string]any{"type": "UNIFY_CHAT_WITH_AI", "model": in.Model, "promptObject": map[string]any{"prompt": prompt}})

	settings, _ := h.store.GetProxySettings("1minai")
	tried := map[string]bool{}
	var finalErr error
	for attempt := 1; attempt <= h.cfg.MaxKeyRetries; attempt++ {
		key, err := h.pool.GetBestKeyExcluding(tried)
		if err != nil {
			writeProxyError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		tried[key.KeyHash] = true
		h.pool.SyncKeyToDB(key)
		target := oneMinAITarget
		if in.Stream {
			target += "?isStreaming=true"
		}
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, target, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("API-KEY", key.RawKey)
		start := time.Now()
		client := h.client
		if settings.UseForRequests && proxies.ShouldUse(settings, false) {
			client, _ = h.proxyPool.Client(true, 10*time.Minute)
		}
		resp, err := client.Do(req)
		if err != nil {
			key.RollbackUsage()
			key.SetCooldown(30*time.Second, "")
			h.pool.SyncKeyToDB(key)
			finalErr = err
			continue
		}
		if resp.StatusCode >= 400 {
			errBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			message := strings.ToLower(string(errBody))
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				key.SetStatus("invalid")
			} else if strings.Contains(message, "credit") && (strings.Contains(message, "insufficient") || strings.Contains(message, "limit") || strings.Contains(message, "exhaust")) {
				key.SetStatus("credit_exhausted")
			} else if resp.StatusCode == http.StatusTooManyRequests {
				key.RollbackUsage()
				key.SetCooldown(time.Minute, "rate_limited")
			} else if resp.StatusCode >= 500 {
				key.RollbackUsage()
			}
			h.pool.SyncKeyToDB(key)
			h.log(key, in.Model, resp.StatusCode, string(errBody), start, in.Stream)
			finalErr = fmt.Errorf("1min.AI returned %d", resp.StatusCode)
			continue
		}
		if in.Stream {
			h.stream(w, resp, key, in.Model, start)
		} else {
			h.normal(w, resp, key, in.Model, start)
		}
		return
	}
	writeProxyError(w, http.StatusBadGateway, fmt.Sprintf("1min.AI gateway exhausted all retries. Last error: %v", finalErr))
}

func oneMinPrompt(messages []struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}) (string, error) {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		text, ok := message.Content.(string)
		if !ok {
			return "", fmt.Errorf("only text message content is supported by the 1min.AI OpenAI adapter")
		}
		if text != "" {
			parts = append(parts, strings.ToUpper(message.Role)+": "+text)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("messages must contain text content")
	}
	return strings.Join(parts, "\n\n"), nil
}

func (h *OneMinAIHandler) normal(w http.ResponseWriter, resp *http.Response, key *keys.KeyState, model string, start time.Time) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeProxyError(w, http.StatusBadGateway, "Failed to read response from upstream")
		return
	}
	var record struct {
		AIRecord struct {
			Status   string `json:"status"`
			TeamUser struct {
				CreditLimit int64 `json:"creditLimit"`
				UsedCredit  int64 `json:"usedCredit"`
			} `json:"teamUser"`
			Detail struct {
				Result []string `json:"resultObject"`
			} `json:"aiRecordDetail"`
		} `json:"aiRecord"`
	}
	if err := json.Unmarshal(body, &record); err != nil || record.AIRecord.Status != "SUCCESS" {
		key.RollbackUsage()
		if strings.Contains(strings.ToLower(string(body)), "credit") {
			key.SetStatus("credit_exhausted")
		}
		h.pool.SyncKeyToDB(key)
		writeProxyError(w, http.StatusBadGateway, "1min.AI did not return a successful chat result")
		return
	}
	h.updateCredits(key, record.AIRecord.TeamUser.CreditLimit, record.AIRecord.TeamUser.UsedCredit)
	content := strings.Join(record.AIRecord.Detail.Result, "\n")
	out := map[string]any{"id": "chatcmpl-1minai", "object": "chat.completion", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": content}, "finish_reason": "stop"}}}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
	h.log(key, model, http.StatusOK, "", start, false)
}

func (h *OneMinAIHandler) stream(w http.ResponseWriter, resp *http.Response, key *keys.KeyState, model string, start time.Time) {
	defer resp.Body.Close()
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeProxyError(w, http.StatusInternalServerError, "Streaming not supported by gateway")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	scanner := bufio.NewScanner(resp.Body)
	var event string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if event == "content" {
			var chunk struct {
				Content string `json:"content"`
			}
			if json.Unmarshal([]byte(data), &chunk) == nil && chunk.Content != "" {
				out, _ := json.Marshal(map[string]any{"id": "chatcmpl-1minai", "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": chunk.Content}, "finish_reason": nil}}})
				fmt.Fprintf(w, "data: %s\n\n", out)
				flusher.Flush()
			}
		}
		if event == "result" {
			var result struct {
				AIRecord struct {
					TeamUser struct {
						CreditLimit int64 `json:"creditLimit"`
						UsedCredit  int64 `json:"usedCredit"`
					} `json:"teamUser"`
				} `json:"aiRecord"`
			}
			if json.Unmarshal([]byte(data), &result) == nil {
				h.updateCredits(key, result.AIRecord.TeamUser.CreditLimit, result.AIRecord.TeamUser.UsedCredit)
			}
		}
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
	h.log(key, model, http.StatusOK, "", start, true)
}

func (h *OneMinAIHandler) updateCredits(key *keys.KeyState, limit, used int64) {
	key.SetCreditBalance(limit, used)
	h.pool.SyncKeyToDB(key)
}

func (h *OneMinAIHandler) log(key *keys.KeyState, model string, status int, message string, start time.Time, stream bool) {
	if err := h.store.LogRequest(&store.DBRequest{Timestamp: time.Now(), KeyHash: key.KeyHash, Model: model, StatusCode: status, ErrorMsg: message, LatencyMs: time.Since(start).Milliseconds(), TTFTMs: time.Since(start).Milliseconds(), IsStream: stream, Provider: "1minai"}); err != nil {
		log.Printf("[1min.AI] request log failed: %v", err)
	}
}
