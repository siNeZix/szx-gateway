package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
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

type oneMinToolDefinition struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
		InputSchema json.RawMessage `json:"input_schema"`
	} `json:"function"`
}

type oneMinEmulatedToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type oneMinAIRecord struct {
	Status   string `json:"status"`
	TeamUser struct {
		CreditLimit int64 `json:"creditLimit"`
		UsedCredit  int64 `json:"usedCredit"`
	} `json:"teamUser"`
	Detail struct {
		Result json.RawMessage `json:"resultObject"`
	} `json:"aiRecordDetail"`
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
	EmulatedTools  bool
	Tools          []oneMinToolDefinition
	ToolChoice     string
	HasToolResult  bool
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
	if normalized.EmulatedTools && conversation.ID != "" {
		writeOpenAIError(w, http.StatusBadRequest, "oneMinAI.conversationId is not supported with oneMinAI.emulatedTools", "oneMinAI.conversationId", "invalid_request_error")
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
		if in.Stream && !normalized.EmulatedTools {
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
		if in.Stream && !normalized.EmulatedTools {
			h.stream(w, resp, key, in.Model, start)
		} else {
			h.normal(w, resp, key, in.Model, start, normalized.EmulatedTools, normalized.Tools, normalized.ToolChoice, normalized.HasToolResult, in.Stream)
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
	if err := parseOneMinOptions(in, &out); err != nil {
		return out, err
	}
	out.EmulatedTools = hasToolDefinitions(in.Tools) || hasToolChoice(in.ToolChoice) || hasParallelToolCalls(in.ParallelToolCalls) || hasOneMinToolHistory(in.Messages)
	if out.EmulatedTools {
		if !hasToolDefinitions(in.Tools) {
			return out, fmt.Errorf("tools: definitions are required when continuing tool-call history")
		}
		if err := parseOneMinTools(in, &out); err != nil {
			return out, err
		}
	} else if hasToolDefinitions(in.Tools) || hasToolChoice(in.ToolChoice) || hasParallelToolCalls(in.ParallelToolCalls) {
		return out, fmt.Errorf("tools: tool calls are not supported by the 1min.AI OpenAI adapter")
	}
	if hasJSONValue(in.ResponseFormat) || hasJSONValue(in.ReasoningEffort) || hasJSONValue(in.Audio) || hasJSONValue(in.Modalities) {
		return out, fmt.Errorf("request: structured output, reasoning and audio/video are not supported by the 1min.AI OpenAI adapter")
	}
	parts := make([]string, 0, len(in.Messages))
	toolCallIDs := map[string]bool{}
	for i, message := range in.Messages {
		if out.EmulatedTools {
			if err := appendOneMinEmulatedToolMessage(&parts, message, i, out.Tools, toolCallIDs, &out.HasToolResult); err != nil {
				return out, err
			}
			if hasToolCalls(message.ToolCalls) || message.ToolCallID != "" {
				continue
			}
		} else if hasToolCalls(message.ToolCalls) || hasJSONValue(message.FunctionCall) || message.ToolCallID != "" || hasJSONValue(message.Reasoning) || hasJSONValue(message.Refusal) {
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
	if out.EmulatedTools {
		// Keep the protocol after untrusted conversation history so it remains the last instruction.
		parts = append(parts, oneMinEmulatedToolsPrompt(out.Tools, out.ToolChoice, out.HasToolResult))
	}
	out.Prompt = strings.Join(parts, "\n\n")
	return out, nil
}

func parseOneMinTools(in oneMinOpenAIRequest, out *oneMinNormalizedRequest) error {
	if !hasToolDefinitions(in.Tools) {
		if hasToolChoice(in.ToolChoice) {
			return fmt.Errorf("tool_choice: requires at least one tool definition")
		}
		return nil
	}
	if len(in.Tools) > 64*1024 {
		return fmt.Errorf("tools: exceeds 65536 bytes")
	}
	if err := json.Unmarshal(in.Tools, &out.Tools); err != nil || len(out.Tools) == 0 || len(out.Tools) > 32 {
		return fmt.Errorf("tools: must be an array of 1..32 function definitions")
	}
	names := make(map[string]bool, len(out.Tools))
	for i, tool := range out.Tools {
		if tool.Function.Parameters == nil && tool.Function.InputSchema != nil {
			tool.Function.Parameters = tool.Function.InputSchema
			out.Tools[i] = tool
		}
		if tool.Function.Parameters == nil {
			tool.Function.Parameters = json.RawMessage(`{}`)
			out.Tools[i] = tool
		}
		if tool.Type != "function" || tool.Function.Name == "" || len(tool.Function.Name) > 64 || !oneMinToolName(tool.Function.Name) || !json.Valid(tool.Function.Parameters) {
			return fmt.Errorf("tools[%d]: must be a valid function definition", i)
		}
		var parameters map[string]any
		if json.Unmarshal(tool.Function.Parameters, &parameters) != nil || parameters == nil {
			return fmt.Errorf("tools[%d].function.parameters: must be a JSON object", i)
		}
		if names[tool.Function.Name] {
			return fmt.Errorf("tools[%d].function.name: duplicate tool name %q", i, tool.Function.Name)
		}
		names[tool.Function.Name] = true
	}
	return parseOneMinToolChoice(in.ToolChoice, out)
}

func hasOneMinToolHistory(messages []oneMinAIMessage) bool {
	for _, message := range messages {
		if hasToolCalls(message.ToolCalls) || message.ToolCallID != "" {
			return true
		}
	}
	return false
}

func parseOneMinToolChoice(value json.RawMessage, out *oneMinNormalizedRequest) error {
	if !hasJSONValue(value) {
		return nil
	}
	var choice string
	if json.Unmarshal(value, &choice) == nil {
		if choice == "auto" || choice == "none" || choice == "required" {
			out.ToolChoice = choice
			return nil
		}
		return fmt.Errorf("tool_choice: must be auto, none, required, or a function object")
	}
	var forced struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(value, &forced) != nil || forced.Type != "function" || forced.Function.Name == "" {
		return fmt.Errorf("tool_choice: must be auto, none, required, or a function object")
	}
	for _, tool := range out.Tools {
		if tool.Function.Name == forced.Function.Name {
			out.ToolChoice = "function " + forced.Function.Name
			return nil
		}
	}
	return fmt.Errorf("tool_choice: refers to an undeclared tool")
}

func oneMinToolName(name string) bool {
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func appendOneMinEmulatedToolMessage(parts *[]string, message oneMinAIMessage, index int, tools []oneMinToolDefinition, toolCallIDs map[string]bool, hasToolResult *bool) error {
	if hasJSONValue(message.FunctionCall) || hasJSONValue(message.Reasoning) || hasJSONValue(message.Refusal) {
		return fmt.Errorf("messages[%d]: legacy function calls, reasoning and refusal are not supported", index)
	}
	if hasToolCalls(message.ToolCalls) {
		if message.Role != "assistant" {
			return fmt.Errorf("messages[%d].tool_calls: only assistant messages may contain tool calls", index)
		}
		calls, err := parseOneMinToolCalls(message.ToolCalls, tools)
		if err != nil {
			return fmt.Errorf("messages[%d].tool_calls: %w", index, err)
		}
		for _, call := range calls {
			if toolCallIDs[call.ID] {
				return fmt.Errorf("messages[%d].tool_calls: duplicate tool call ID %q", index, call.ID)
			}
			toolCallIDs[call.ID] = true
			*parts = append(*parts, "ASSISTANT TOOL CALL "+call.ID+" "+call.Name+": "+string(call.Arguments))
		}
		return nil
	}
	if message.ToolCallID != "" {
		if message.Role != "tool" {
			return fmt.Errorf("messages[%d].tool_call_id: only tool messages may provide tool_call_id", index)
		}
		if !toolCallIDs[message.ToolCallID] {
			return fmt.Errorf("messages[%d].tool_call_id: does not match an earlier assistant tool call", index)
		}
		text, err := oneMinTextContent(message.Content, index)
		if err != nil {
			return err
		}
		*parts = append(*parts, "TOOL RESULT "+message.ToolCallID+": "+text)
		*hasToolResult = true
		return nil
	}
	return nil
}

func parseOneMinToolCalls(value json.RawMessage, tools []oneMinToolDefinition) ([]oneMinEmulatedToolCall, error) {
	var raw []struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"function"`
	}
	if json.Unmarshal(value, &raw) != nil || len(raw) == 0 || len(raw) > 32 {
		return nil, fmt.Errorf("must be an array of 1..32 OpenAI function calls")
	}
	allowed := make(map[string]bool, len(tools))
	for _, tool := range tools {
		allowed[tool.Function.Name] = true
	}
	calls := make([]oneMinEmulatedToolCall, 0, len(raw))
	for i, call := range raw {
		if call.ID == "" || len(call.ID) > 128 || call.Type != "function" || !allowed[call.Function.Name] || !json.Valid(call.Function.Arguments) {
			return nil, fmt.Errorf("call %d is invalid or refers to an undeclared tool", i)
		}
		var arguments any
		if json.Unmarshal(call.Function.Arguments, &arguments) != nil {
			return nil, fmt.Errorf("call %d arguments are invalid JSON", i)
		}
		calls = append(calls, oneMinEmulatedToolCall{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments})
	}
	return calls, nil
}

func oneMinEmulatedToolsPrompt(tools []oneMinToolDefinition, toolChoice string, hasToolResult bool) string {
	definitions := make([]string, 0, len(tools))
	for _, tool := range tools {
		definitions = append(definitions, fmt.Sprintf(`{"name":%q,"parameters":%s}`, tool.Function.Name, oneMinCompactToolSchema(tool.Function.Parameters)))
	}
	sort.Strings(definitions)
	instruction := "Call at least one tool before answering."
	if hasToolResult {
		instruction = "Answer the user using provided tool results, or call another tool if needed."
	}
	switch toolChoice {
	case "none":
		instruction = "Do not call tools; answer directly."
	case "required":
		instruction = "Call at least one tool before answering."
	default:
		if strings.HasPrefix(toolChoice, "function ") {
			instruction = "Call only the required tool " + strings.TrimPrefix(toolChoice, "function ") + "."
		}
	}
	return "SYSTEM: Reply with exactly one JSON object and no Markdown. To call tools: {\"tool_calls\":[{\"name\":\"tool_name\",\"arguments\":{...}}]}. To answer: {\"content\":\"answer\"}. " + instruction + " Never claim workspace, file, command, network, or other external facts without first calling an appropriate tool. Tools: [" + strings.Join(definitions, ",") + "]"
}

func oneMinCompactToolSchema(raw json.RawMessage) string {
	var schema struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if json.Unmarshal(raw, &schema) != nil {
		return `{}`
	}
	properties := make(map[string]string, len(schema.Properties))
	for name, property := range schema.Properties {
		properties[name] = property.Type
	}
	compact, err := json.Marshal(map[string]any{"type": schema.Type, "properties": properties, "required": schema.Required})
	if err != nil {
		return `{}`
	}
	return string(compact)
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

func (h *OneMinAIHandler) normal(w http.ResponseWriter, resp *http.Response, key *keys.KeyState, model string, start time.Time, emulatedTools bool, tools []oneMinToolDefinition, toolChoice string, hasToolResult, stream bool) {
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
		AIRecord oneMinAIRecord `json:"aiRecord"`
	}
	if err := json.Unmarshal(body, &record); err != nil || record.AIRecord.Status != "SUCCESS" {
		key.RollbackUsage()
		if oneMinAICreditExhausted(record.AIRecord.Detail.Result) {
			// 1min.AI may reject an expensive request while the key remains usable for others.
			key.SetCooldown(0, "active")
			h.pool.SyncKeyToDB(key)
			h.log(key, model, http.StatusPaymentRequired, "1min.AI account credits unavailable", start, stream)
			writeOpenAIError(w, http.StatusPaymentRequired, "1min.AI account credits are exhausted", "", "insufficient_credits")
			return
		}
		key.SetCooldown(30*time.Second, "")
		h.pool.SyncKeyToDB(key)
		h.log(key, model, http.StatusBadGateway, string(body), start, false)
		writeProxyError(w, http.StatusBadGateway, "1min.AI did not return a successful chat result")
		return
	}
	var results []string
	if err := json.Unmarshal(record.AIRecord.Detail.Result, &results); err != nil || len(results) == 0 {
		key.RollbackUsage()
		key.SetCooldown(30*time.Second, "")
		h.pool.SyncKeyToDB(key)
		h.log(key, model, http.StatusBadGateway, "1min.AI returned a successful chat without content", start, false)
		writeProxyError(w, http.StatusBadGateway, "1min.AI returned a successful chat without content")
		return
	}
	h.updateCredits(key, record.AIRecord.TeamUser.CreditLimit, record.AIRecord.TeamUser.UsedCredit)
	content := strings.Join(results, "\n")
	message := map[string]any{"role": "assistant", "content": content}
	finishReason := "stop"
	if emulatedTools {
		if calls, ok := oneMinEmulatedToolCalls(content, tools); ok {
			message = map[string]any{"role": "assistant", "content": nil, "tool_calls": calls}
			finishReason = "tool_calls"
		} else if finalContent, ok := oneMinEmulatedContent(content); ok {
			message["content"] = finalContent
		}
		if oneMinToolCallRequired(toolChoice, hasToolResult) && finishReason != "tool_calls" {
			h.log(key, model, http.StatusBadGateway, "1min.AI did not return required tool calls", start, stream)
			writeOpenAIError(w, http.StatusBadGateway, "1min.AI did not return required tool calls", "tool_choice", "upstream_error")
			return
		}
	}
	if stream {
		h.emulatedStream(w, model, message, finishReason)
		h.log(key, model, http.StatusOK, "", start, true)
		return
	}
	out := map[string]any{"id": "chatcmpl-1minai", "object": "chat.completion", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finishReason}}}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
	h.log(key, model, http.StatusOK, "", start, false)
}

func oneMinAICreditExhausted(raw json.RawMessage) bool {
	var failure struct {
		Code string `json:"code"`
	}
	return json.Unmarshal(raw, &failure) == nil && failure.Code == "INSUFFICIENT_CREDITS"
}

func oneMinToolCallRequired(toolChoice string, hasToolResult bool) bool {
	if toolChoice == "none" {
		return false
	}
	return toolChoice == "required" || strings.HasPrefix(toolChoice, "function ") || !hasToolResult
}

func (h *OneMinAIHandler) emulatedStream(w http.ResponseWriter, model string, message map[string]any, finishReason string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeProxyError(w, http.StatusInternalServerError, "Streaming not supported by gateway")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	writeOneMinStreamChunk(w, flusher, model, map[string]any{"role": "assistant"}, nil)
	if calls, ok := message["tool_calls"].([]map[string]any); ok {
		for index, call := range calls {
			function := call["function"].(map[string]string)
			writeOneMinStreamChunk(w, flusher, model, map[string]any{"tool_calls": []any{map[string]any{"index": index, "id": call["id"], "type": "function", "function": function}}}, nil)
		}
	} else if content, ok := message["content"].(string); ok && content != "" {
		writeOneMinStreamChunk(w, flusher, model, map[string]any{"content": content}, nil)
	}
	writeOneMinStreamChunk(w, flusher, model, map[string]any{}, &finishReason)
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func oneMinEmulatedToolCalls(content string, tools []oneMinToolDefinition) ([]map[string]any, bool) {
	var response struct {
		ToolCalls []struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"tool_calls"`
	}
	if json.Unmarshal([]byte(content), &response) != nil || len(response.ToolCalls) == 0 || len(response.ToolCalls) > 32 {
		return nil, false
	}
	allowed := make(map[string]bool, len(tools))
	for _, tool := range tools {
		allowed[tool.Function.Name] = true
	}
	calls := make([]map[string]any, 0, len(response.ToolCalls))
	for i, call := range response.ToolCalls {
		if !allowed[call.Name] || !json.Valid(call.Arguments) {
			return nil, false
		}
		var arguments any
		if json.Unmarshal(call.Arguments, &arguments) != nil {
			return nil, false
		}
		encoded, err := json.Marshal(arguments)
		if err != nil {
			return nil, false
		}
		calls = append(calls, map[string]any{"id": fmt.Sprintf("call_1min_%d", i+1), "type": "function", "function": map[string]string{"name": call.Name, "arguments": string(encoded)}})
	}
	return calls, true
}

func oneMinEmulatedContent(content string) (string, bool) {
	var response struct {
		Content *string `json:"content"`
	}
	if json.Unmarshal([]byte(content), &response) != nil || response.Content == nil {
		return "", false
	}
	return *response.Content, true
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
