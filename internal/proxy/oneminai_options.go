package proxy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"szx-gateway/internal/store"
)

func parseOneMinOptions(in oneMinOpenAIRequest, out *oneMinNormalizedRequest) error {
	if hasJSONValue(in.Metadata) {
		if len(in.Metadata) > 8192 {
			return fmt.Errorf("metadata: exceeds 8192 bytes")
		}
		if err := json.Unmarshal(in.Metadata, &out.Metadata); err != nil || out.Metadata == nil {
			return fmt.Errorf("metadata: must be a JSON object")
		}
	}
	if !hasJSONValue(in.OneMinAI) {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(in.OneMinAI, &raw); err != nil {
		return fmt.Errorf("oneMinAI: must be an object")
	}
	for name, value := range raw {
		switch name {
		case "brandVoiceId":
			if err := json.Unmarshal(value, &out.BrandVoiceID); err != nil || strings.TrimSpace(out.BrandVoiceID) == "" || len(out.BrandVoiceID) > 256 {
				return fmt.Errorf("oneMinAI.brandVoiceId: must be a non-empty string up to 256 bytes")
			}
		case "memory":
			var v bool
			if err := json.Unmarshal(value, &v); err != nil {
				return fmt.Errorf("oneMinAI.memory: must be a boolean")
			}
			out.Memory = &v
		case "conversationId":
			if err := json.Unmarshal(value, &out.ConversationID); err != nil || out.ConversationID == "" || len(out.ConversationID) > 64 {
				return fmt.Errorf("oneMinAI.conversationId: must be a non-empty opaque ID")
			}
		case "webSearch":
			var v struct {
				Enabled   bool `json:"enabled"`
				NumOfSite int  `json:"numOfSite"`
				MaxWord   int  `json:"maxWord"`
			}
			if err := json.Unmarshal(value, &v); err != nil {
				return fmt.Errorf("oneMinAI.webSearch: invalid object")
			}
			if v.NumOfSite < 0 || v.NumOfSite > 10 || v.MaxWord < 0 || v.MaxWord > 10000 {
				return fmt.Errorf("oneMinAI.webSearch: numOfSite must be 0..10 and maxWord 0..10000")
			}
			out.WebSearch = map[string]any{"webSearch": v.Enabled, "numOfSite": v.NumOfSite, "maxWord": v.MaxWord}
		case "history":
			var v struct {
				IsMixed bool `json:"isMixed"`
			}
			if err := json.Unmarshal(value, &v); err != nil {
				return fmt.Errorf("oneMinAI.history: invalid object")
			}
			out.MixedHistory = v.IsMixed
		default:
			return fmt.Errorf("oneMinAI.%s: unsupported provider option", name)
		}
	}
	return nil
}

func oneMinPayload(model string, normalized oneMinNormalizedRequest, images, files []string, upstreamConversationID string) ([]byte, error) {
	prompt := map[string]any{"prompt": normalized.Prompt}
	if len(images) > 0 || len(files) > 0 {
		prompt["attachments"] = map[string]any{"images": images, "files": files}
	}
	settings := map[string]any{}
	if normalized.WebSearch != nil {
		settings["webSearchSettings"] = normalized.WebSearch
	}
	if normalized.Memory != nil {
		settings["withMemories"] = *normalized.Memory
	}
	if normalized.MixedHistory {
		settings["historySettings"] = map[string]any{"isMixed": true}
	}
	if len(settings) > 0 {
		prompt["settings"] = settings
	}
	if upstreamConversationID != "" {
		prompt["conversationId"] = upstreamConversationID
	}
	out := map[string]any{"type": "UNIFY_CHAT_WITH_AI", "model": model, "promptObject": prompt}
	if normalized.BrandVoiceID != "" {
		out["brandVoiceId"] = normalized.BrandVoiceID
	}
	if normalized.Metadata != nil {
		out["metadata"] = normalized.Metadata
	}
	return json.Marshal(out)
}

func (h *OneMinAIHandler) validateAttachmentCapabilities(model string, normalized oneMinNormalizedRequest) error {
	if len(normalized.Images)+len(normalized.Files) == 0 {
		return nil
	}
	for _, m := range h.ranking.GetOneMinAIModels() {
		if m.ID != model {
			continue
		}
		modalities := strings.ToLower(m.Modalities)
		if len(normalized.Images) > 0 && !strings.Contains(modalities, "image") {
			return fmt.Errorf("model %s is not catalogued for image input", model)
		}
		if len(normalized.Files) > 0 && !(strings.Contains(modalities, "pdf") || strings.Contains(modalities, "document") || strings.Contains(modalities, "file")) {
			return fmt.Errorf("model %s is not catalogued for file input", model)
		}
		return nil
	}
	return fmt.Errorf("model catalogue is unavailable")
}

func oneMinErrorParam(err error) string {
	message := err.Error()
	if i := strings.Index(message, ":"); i > 0 {
		return message[:i]
	}
	return ""
}
func oneMinSecretHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func oneMinRandomID() (string, error) {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func (h *OneMinAIHandler) lookupConversation(r *http.Request, id string) (store.OneMinAIConversation, error) {
	if id == "" {
		return store.OneMinAIConversation{}, nil
	}
	secret := r.Header.Get("X-1min-Conversation-Secret")
	if secret == "" {
		return store.OneMinAIConversation{}, fmt.Errorf("X-1min-Conversation-Secret is required for conversation use")
	}
	v, ok, err := h.store.GetOneMinAIConversation(id, oneMinSecretHash(secret), time.Now().UTC())
	if err != nil {
		return v, fmt.Errorf("conversation lookup failed")
	}
	if !ok {
		return v, fmt.Errorf("conversation not found or secret is invalid")
	}
	return v, nil
}

func (h *OneMinAIHandler) createConversation(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Model string `json:"model"`
		Title string `json:"title"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&in); err != nil || in.Model == "" || !h.ranking.IsOneMinAIModel(in.Model) {
		writeOpenAIError(w, http.StatusBadRequest, "model must be a supported 1min.AI model", "model", "invalid_request_error")
		return
	}
	if len(in.Title) > 256 {
		writeOpenAIError(w, http.StatusBadRequest, "title exceeds 256 bytes", "title", "invalid_request_error")
		return
	}
	key, err := h.pool.GetBestKey()
	if err != nil {
		writeProxyError(w, http.StatusServiceUnavailable, "no 1min.AI key available")
		return
	}
	defer h.pool.SyncKeyToDB(key)
	payload, _ := json.Marshal(map[string]any{"type": "UNIFY_CHAT_WITH_AI", "title": in.Title, "model": in.Model})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "https://api.1min.ai/api/conversations", strings.NewReader(string(payload)))
	if err != nil {
		key.RollbackUsage()
		writeProxyError(w, 500, "failed to create upstream conversation request")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("API-KEY", key.RawKey)
	resp, err := h.client.Do(req)
	if err != nil {
		key.RollbackUsage()
		key.SetCooldown(30*time.Second, "")
		writeProxyError(w, 502, "1min.AI conversation creation failed")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		key.RollbackUsage()
		writeProxyError(w, 502, "1min.AI conversation creation failed")
		return
	}
	var out struct {
		ID           string `json:"id"`
		UUID         string `json:"uuid"`
		Conversation struct {
			ID   string `json:"id"`
			UUID string `json:"uuid"`
		} `json:"conversation"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		key.RollbackUsage()
		writeProxyError(w, 502, "invalid 1min.AI conversation response")
		return
	}
	upstream := out.ID
	if upstream == "" {
		upstream = out.UUID
	}
	if upstream == "" {
		upstream = out.Conversation.ID
	}
	if upstream == "" {
		upstream = out.Conversation.UUID
	}
	if upstream == "" {
		key.RollbackUsage()
		writeProxyError(w, 502, "invalid 1min.AI conversation response")
		return
	}
	id, err := oneMinRandomID()
	if err != nil {
		key.RollbackUsage()
		writeProxyError(w, 500, "failed to create conversation ID")
		return
	}
	secret, err := oneMinRandomID()
	if err != nil {
		key.RollbackUsage()
		writeProxyError(w, 500, "failed to create conversation secret")
		return
	}
	now := time.Now().UTC()
	v := store.OneMinAIConversation{ID: id, OwnerHash: oneMinSecretHash(secret), KeyHash: key.KeyHash, UpstreamID: upstream, Model: in.Model, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(h.cfg.OneMinAIConversationTTL)}
	if err = h.store.CreateOneMinAIConversation(v); err != nil {
		key.RollbackUsage()
		writeProxyError(w, 500, "failed to persist conversation")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "owner_secret": secret, "model": in.Model, "expires_at": v.ExpiresAt.Format(time.RFC3339)})
}
