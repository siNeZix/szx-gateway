package proxy

import (
	"net/http"
	"testing"
)

func TestClassifyOneMinAIError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   oneMinAIErrorAction
	}{
		{"unauthorized invalidates key", http.StatusUnauthorized, oneMinAIInvalidateKey},
		{"forbidden is temporary", http.StatusForbidden, oneMinAIRetryTemporary},
		{"rate limited gets dedicated cooldown", http.StatusTooManyRequests, oneMinAIRetryRateLimited},
		{"invalid request is temporary", http.StatusBadRequest, oneMinAIRetryTemporary},
		{"validation error is temporary", http.StatusUnprocessableEntity, oneMinAIRetryTemporary},
		{"server error is temporary", http.StatusBadGateway, oneMinAIRetryTemporary},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyOneMinAIError(tt.status); got != tt.want {
				t.Fatalf("classifyOneMinAIError(%d) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}
