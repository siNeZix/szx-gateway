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

type oneMinAIErrorAction int

const (
	oneMinAIRetryTemporary oneMinAIErrorAction = iota
	oneMinAIRetryRateLimited
	oneMinAIInvalidateKey
)

// classifyOneMinAIError uses only documented API signals. 1min.AI does not
// document a billing-credit exhaustion error, so unknown failures stay temporary.
func classifyOneMinAIError(status int) oneMinAIErrorAction {
	switch status {
	case http.StatusUnauthorized:
		return oneMinAIInvalidateKey
	case http.StatusTooManyRequests:
		return oneMinAIRetryRateLimited
	default:
		return oneMinAIRetryTemporary
	}
}

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
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-1min-Conversation-Secret")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
		h.models(w)
		return
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") != h.cfg.GatewayToken {
		writeProxyError(w, http.StatusUnauthorized, "Unauthorized: invalid gateway token")
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
		h.chat(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/conversations":
		h.createConversation(w, r)
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
	Model             string            `json:"model"`
	Messages          []oneMinAIMessage `json:"messages"`
	Stream            bool              `json:"stream"`
	Tools             json.RawMessage   `json:"tools"`
	ToolChoice        json.RawMessage   `json:"tool_choice"`
	ParallelToolCalls json.RawMessage   `json:"parallel_tool_calls"`
	ResponseFormat    json.RawMessage   `json:"response_format"`
	ReasoningEffort   json.RawMessage   `json:"reasoning_effort"`
	Modalities        json.RawMessage   `json:"modalities"`
	Audio             json.RawMessage   `json:"audio"`
	OneMinAI          json.RawMessage   `json:"oneMinAI"`
	Metadata          json.RawMessage   `json:"metadata"`
}

type oneMinAIMessage struct {
	Role         string          `json:"role"`
	Content      any             `json:"content"`
	ToolCalls    json.RawMessage `json:"tool_calls"`
	FunctionCall json.RawMessage `json:"function_call"`
	ToolCallID   string          `json:"tool_call_id"`
	Reasoning    json.RawMessage `json:"reasoning"`
	Refusal      json.RawMessage `json:"refusal"`
}

type oneMinAssetInput struct {
	DataURL  string
	Filename string
	MIME     string
	Kind     string
	Param    string
}

type oneMinNormalizedRequest struct {
	Prompt         string
	Images         []oneMinAssetInput
	Files          []oneMinAssetInput
	BrandVoiceID   string
	WebSearch      map[string]any
	Memory         *bool
	ConversationID string
	MixedHistory   bool
	Metadata       map[string]any
}

func (h *OneMinAIHandler) chat(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.cfg.OneMinAIAssetMaxBytes*2+1024*1024)
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
	normalized, err := normalizeOneMinRequest(in)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, err.Error(), oneMinErrorParam(err), "unsupported_content_type")
		return
	}
	if err := h.validateAttachmentCapabilities(in.Model, normalized); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, err.Error(), "messages", "unsupported_content_type")
		return
	}

	settings, _ := h.store.GetProxySettings("1minai")
	tried := map[string]bool{}
	conversation, err := h.lookupConversation(r, normalized.ConversationID)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, err.Error(), "oneMinAI.conversationId", "invalid_conversation")
		return
	}
	if conversation.ID != "" && conversation.Model != in.Model && !normalized.MixedHistory {
		writeOpenAIError(w, http.StatusBadRequest, "conversation model differs; set oneMinAI.history.isMixed=true to allow it", "model", "invalid_request_error")
		return
	}
	var finalErr error
	maxAttempts := h.cfg.MaxKeyRetries
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var key *keys.KeyState
		if conversation.ID != "" {
			key, err = h.pool.ReserveKeyByHash(conversation.KeyHash)
		} else {
			key, err = h.pool.GetBestKeyExcluding(tried)
		}
		if err != nil {
			status := http.StatusServiceUnavailable
			if conversation.ID != "" {
				status = http.StatusConflict
			}
			writeProxyError(w, status, "1min.AI conversation affinity key is unavailable")
			return
		}
		tried[key.KeyHash] = true
		h.pool.SyncKeyToDB(key)
		images, files, uploaded, err := h.uploadAssets(r, key, normalized)
		if err != nil {
			key.RollbackUsage()
			key.SetCooldown(30*time.Second, "asset_upload_error")
			h.pool.SyncKeyToDB(key)
			writeProxyError(w, http.StatusBadGateway, "1min.AI asset upload failed")
			return
		}
		payload, err := oneMinPayload(in.Model, normalized, images, files, conversation.UpstreamID)
		if err != nil {
			key.RollbackUsage()
			h.pool.SyncKeyToDB(key)
			writeProxyError(w, http.StatusInternalServerError, "Failed to encode 1min.AI request")
			return
		}
		target := oneMinAITarget
		if in.Stream {
			target += "?isStreaming=true"
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target, bytes.NewReader(payload))
		if err != nil {
			key.RollbackUsage()
			h.pool.SyncKeyToDB(key)
			writeProxyError(w, http.StatusInternalServerError, "Failed to create 1min.AI request")
			return
		}
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
			if uploaded || conversation.ID != "" {
				break
			}
			continue
		}
		if resp.StatusCode >= 400 {
			errBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			switch classifyOneMinAIError(resp.StatusCode) {
			case oneMinAIInvalidateKey:
				key.SetStatus("invalid")
			case oneMinAIRetryRateLimited:
				key.RollbackUsage()
				key.SetCooldown(time.Minute, "rate_limited")
			default:
				key.RollbackUsage()
				key.SetCooldown(30*time.Second, "")
			}
			h.pool.SyncKeyToDB(key)
			h.log(key, in.Model, resp.StatusCode, string(errBody), start, in.Stream)
			finalErr = fmt.Errorf("1min.AI returned %d", resp.StatusCode)
			if uploaded || conversation.ID != "" {
				break
			}
			continue
		}
		if in.Stream {
			h.stream(w, resp, key, in.Model, start)
		} else {
			h.normal(w, resp, key, in.Model, start)
		}
		if conversation.ID != "" {
			_ = h.store.TouchOneMinAIConversation(conversation.ID, time.Now().UTC())
		}
		return
	}
	writeProxyError(w, http.StatusBadGateway, fmt.Sprintf("1min.AI gateway exhausted all retries. Last error: %v", finalErr))
}

func oneMinPrompt(in oneMinOpenAIRequest) (string, error) {
	normalized, err := normalizeOneMinRequest(in)
	if err != nil {
		return "", err
	}
	if len(normalized.Images) > 0 || len(normalized.Files) > 0 {
		attachment := append(append([]oneMinAssetInput{}, normalized.Images...), normalized.Files...)[0]
		contentType := "file"
		if attachment.Kind == "image" {
			contentType = "image_url"
		}
		return "", fmt.Errorf("%s: unsupported content type %q; attachments require the chat adapter", attachment.Param, contentType)
	}
	return normalized.Prompt, nil
}

func normalizeOneMinRequest(in oneMinOpenAIRequest) (oneMinNormalizedRequest, error) {
	var out oneMinNormalizedRequest
	if hasToolDefinitions(in.Tools) || hasToolChoice(in.ToolChoice) || hasParallelToolCalls(in.ParallelToolCalls) {
		return out, fmt.Errorf("tools: tool calls are not supported by the 1min.AI OpenAI adapter")
	}
	if hasJSONValue(in.ResponseFormat) || hasJSONValue(in.ReasoningEffort) || hasJSONValue(in.Audio) || hasJSONValue(in.Modalities) {
		return out, fmt.Errorf("request: structured output, reasoning and audio/video are not supported by the 1min.AI OpenAI adapter")
	}
	if err := parseOneMinOptions(in, &out); err != nil {
		return out, err
	}

	parts := make([]string, 0, len(in.Messages))
	for i, message := range in.Messages {
		if hasToolCalls(message.ToolCalls) || hasJSONValue(message.FunctionCall) || message.ToolCallID != "" || hasJSONValue(message.Reasoning) || hasJSONValue(message.Refusal) {
			return out, fmt.Errorf("messages[%d]: tool calls and tool results are not supported by the 1min.AI OpenAI adapter", i)
		}
		switch message.Role {
		case "system", "developer", "user", "assistant":
		default:
			return out, fmt.Errorf("messages[%d]: unsupported role %q", i, message.Role)
		}
		text, images, files, err := oneMinMessageContent(message.Content, i, message.Role == "user")
		if err != nil {
			return out, err
		}
		if len(images)+len(files) > 0 && i != len(in.Messages)-1 {
			return out, fmt.Errorf("messages[%d].content: attachments are supported only in the last user message", i)
		}
		if len(images)+len(files) > 0 && message.Role != "user" {
			return out, fmt.Errorf("messages[%d].content: attachments are supported only in user messages", i)
		}
		if text == "" {
			return out, fmt.Errorf("messages[%d]: content must contain text", i)
		}
		out.Images, out.Files = images, files
		parts = append(parts, strings.ToUpper(message.Role)+": "+text)
	}
	out.Prompt = strings.Join(parts, "\n\n")
	return out, nil
}

func hasJSONValue(value json.RawMessage) bool {
	return len(value) > 0 && string(value) != "null"
}

func hasToolDefinitions(value json.RawMessage) bool {
	return hasJSONValue(value) && string(value) != "[]"
}

func hasToolCalls(value json.RawMessage) bool {
	return hasJSONValue(value) && string(value) != "[]"
}

func hasToolChoice(value json.RawMessage) bool {
	return hasJSONValue(value) && string(value) != `"none"`
}

func hasParallelToolCalls(value json.RawMessage) bool {
	return hasJSONValue(value) && string(value) != "false"
}

func oneMinTextContent(content any, messageIndex int) (string, error) {
	text, images, files, err := oneMinMessageContent(content, messageIndex, false)
	if err != nil {
		return "", err
	}
	if len(images)+len(files) > 0 {
		return "", fmt.Errorf("messages[%d].content: attachments are not supported here", messageIndex)
	}
	return text, nil
}

func oneMinMessageContent(content any, messageIndex int, allowAttachments bool) (string, []oneMinAssetInput, []oneMinAssetInput, error) {
	switch value := content.(type) {
	case string:
		return value, nil, nil, nil
	case []any:
		var text strings.Builder
		var images, files []oneMinAssetInput
		for partIndex, part := range value {
			object, ok := part.(map[string]any)
			if !ok {
				return "", nil, nil, fmt.Errorf("messages[%d].content[%d]: content part must be an object", messageIndex, partIndex)
			}
			partType, ok := object["type"].(string)
			if !ok || partType == "" {
				return "", nil, nil, fmt.Errorf("messages[%d].content[%d]: content part type must be a string", messageIndex, partIndex)
			}
			param := fmt.Sprintf("messages[%d].content[%d]", messageIndex, partIndex)
			switch partType {
			case "text":
				partText, ok := object["text"].(string)
				if !ok {
					return "", nil, nil, fmt.Errorf("%s: text must be a string", param)
				}
				text.WriteString(partText)
			case "image_url":
				if !allowAttachments {
					return "", nil, nil, fmt.Errorf("%s: attachments are supported only in the last user message", param)
				}
				v, ok := object["image_url"].(map[string]any)
				url, ok2 := v["url"].(string)
				if !ok || !ok2 || url == "" {
					return "", nil, nil, fmt.Errorf("%s.image_url.url: must be a non-empty string", param)
				}
				images = append(images, oneMinAssetInput{DataURL: url, Kind: "image", Param: param})
			case "file":
				if !allowAttachments {
					return "", nil, nil, fmt.Errorf("%s: attachments are supported only in the last user message", param)
				}
				v, ok := object["file"].(map[string]any)
				data, ok2 := v["file_data"].(string)
				filename, ok3 := v["filename"].(string)
				if !ok || !ok2 || !ok3 || data == "" || filename == "" {
					return "", nil, nil, fmt.Errorf("%s.file: filename and file_data must be non-empty strings", param)
				}
				files = append(files, oneMinAssetInput{DataURL: data, Filename: filename, Kind: "file", Param: param})
			default:
				return "", nil, nil, fmt.Errorf("%s: unsupported content type %q", param, partType)
			}
		}
		return text.String(), images, files, nil
	default:
		return "", nil, nil, fmt.Errorf("messages[%d].content: only text string or text content parts are supported by the 1min.AI OpenAI adapter", messageIndex)
	}
}

func (h *OneMinAIHandler) normal(w http.ResponseWriter, resp *http.Response, key *keys.KeyState, model string, start time.Time) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		key.RollbackUsage()
		key.SetCooldown(30*time.Second, "")
		h.pool.SyncKeyToDB(key)
		h.log(key, model, http.StatusBadGateway, "failed to read response from upstream", start, false)
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
		key.SetCooldown(30*time.Second, "")
		h.pool.SyncKeyToDB(key)
		h.log(key, model, http.StatusBadGateway, string(body), start, false)
		writeProxyError(w, http.StatusBadGateway, "1min.AI did not return a successful chat result")
		return
	}
	if len(record.AIRecord.Detail.Result) == 0 {
		key.RollbackUsage()
		key.SetCooldown(30*time.Second, "")
		h.pool.SyncKeyToDB(key)
		h.log(key, model, http.StatusBadGateway, "1min.AI returned a successful chat without content", start, false)
		writeProxyError(w, http.StatusBadGateway, "1min.AI returned a successful chat without content")
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
	started := false
	startStream := func() {
		if started {
			return
		}
		started = true
		writeOneMinStreamChunk(w, flusher, model, map[string]any{"role": "assistant"}, nil)
	}

	reader := bufio.NewReader(resp.Body)
	event := "message"
	dataLines := make([]string, 0, 1)
	completed := false
	upstreamFailed := false
	processEvent := func() {
		if event == "done" {
			completed = true
		}
		if len(dataLines) == 0 {
			event = "message"
			return
		}
		data := strings.Join(dataLines, "\n")
		switch event {
		case "content":
			var chunk struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal([]byte(data), &chunk); err == nil && chunk.Content != "" {
				startStream()
				writeOneMinStreamChunk(w, flusher, model, map[string]any{"content": chunk.Content}, nil)
			}
		case "result":
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
		case "error":
			upstreamFailed = true
		}
		dataLines = dataLines[:0]
		event = "message"
	}

	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			processEvent()
		} else if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
		if err != nil {
			if err == io.EOF {
				processEvent()
				break
			}
			key.RollbackUsage()
			key.SetCooldown(30*time.Second, "stream_error")
			h.pool.SyncKeyToDB(key)
			h.log(key, model, http.StatusBadGateway, "failed to read streaming response: "+err.Error(), start, true)
			if !started {
				writeProxyError(w, http.StatusBadGateway, "Failed to read streaming response from upstream")
			}
			return
		}
		if upstreamFailed {
			key.RollbackUsage()
			key.SetCooldown(30*time.Second, "stream_error")
			h.pool.SyncKeyToDB(key)
			h.log(key, model, http.StatusBadGateway, "1min.AI streaming error event", start, true)
			if !started {
				writeProxyError(w, http.StatusBadGateway, "1min.AI streaming error")
			}
			return
		}
	}
	if !completed {
		key.RollbackUsage()
		key.SetCooldown(30*time.Second, "stream_incomplete")
		h.pool.SyncKeyToDB(key)
		h.log(key, model, http.StatusBadGateway, "1min.AI stream ended without done event", start, true)
		if !started {
			writeProxyError(w, http.StatusBadGateway, "1min.AI stream ended without done event")
		}
		return
	}
	startStream()
	finish := "stop"
	writeOneMinStreamChunk(w, flusher, model, map[string]any{}, &finish)
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
	h.log(key, model, http.StatusOK, "", start, true)
}

func writeOneMinStreamChunk(w http.ResponseWriter, flusher http.Flusher, model string, delta map[string]any, finish *string) {
	out, _ := json.Marshal(map[string]any{"id": "chatcmpl-1minai", "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
	fmt.Fprintf(w, "data: %s\n\n", out)
	flusher.Flush()
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
