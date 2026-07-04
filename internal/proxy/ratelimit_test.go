package proxy

import (
	"testing"
)

func TestIsUpstreamRateLimit(t *testing.T) {
	// From real user logs
	upstreamBody := []byte(`{"error":{"message":"Provider returned error","code":429,"metadata":{"raw":"google/gemma-4-31b-it:free is temporarily rate-limited upstream. Retry shortly...","provider_name":"Google AI Studio","is_byok":false}},"user_id":"user_3E7vwYlaMGIuYQMPJ3mmb8JljS9"}`)
	if !IsUpstreamRateLimit(upstreamBody) {
		t.Error("expected true for upstream rate-limit error")
	}

	// BYOK key rate-limit
	byokBody := []byte(`{"error":{"message":"Provider returned error","code":429,"metadata":{"raw":"rate-limited upstream...","provider_name":"Google AI Studio","is_byok":true}}}`)
	if !IsUpstreamRateLimit(byokBody) {
		t.Error("expected true when raw contains rate-limited upstream even with byok")
	}

	// Normal 429 rate limit (key limit, no provider metadata)
	normal429 := []byte(`{"error":{"message":"You have exceeded your request rate. Please try again later.","code":429}}`)
	if IsUpstreamRateLimit(normal429) {
		t.Error("expected false for regular key limit 429")
	}

	// Quota exhausted
	quotaExhausted := []byte(`{"error":{"message":"Credit exhausted","code":402}}`)
	if IsUpstreamRateLimit(quotaExhausted) {
		t.Error("expected false for credit exhausted")
	}
}

func TestClassifyAIHubMixError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   aihubmixErrorAction
	}{
		{"provider rate limit", 429, `{"error":{"message":"[429]: Model coding-glm-5.2-free rate limited by provider – contact support to request higher concurrency or try again later. (tid: 1)"}}`, aihubmixReturnToClient},
		{"model too many requests", 429, `{"error":{"message":"The coding-glm-5.2-free model Too many requests; please try again later."}}`, aihubmixReturnToClient},
		{"no channel", 503, `{"error":{"message":"No available channel for this model"}}`, aihubmixReturnToClient},
		{"bad model", 503, `{"error":{"message":"Incorrect model ID. Please request to view the model page or you do not have permission to use this model"}}`, aihubmixReturnToClient},
		{"invalid token", 401, `{"error":{"message":"Unauthorized – access token is invalid or expired."}}`, aihubmixRetryInvalidKey},
		{"disabled key", 403, `{"error":{"message":"key is disabled"}}`, aihubmixRetryInvalidKey},
		{"quota", 403, `{"error":{"code":"insufficient_user_quota","message":"Your account balance is insufficient. Please recharge your account."}}`, aihubmixRetryAccountLimit},
		{"key model", 403, `{"error":{"message":"Forbidden – key abc123 not authorized to access the requested model."}}`, aihubmixRetryKeyModelLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyAIHubMixError(tt.status, []byte(tt.body)); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
