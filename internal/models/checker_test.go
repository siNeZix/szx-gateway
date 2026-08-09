package models

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"szx-gateway/internal/store"
)

func TestModelChecker_CheckUsesOnlyContent(t *testing.T) {
	tests := []struct {
		name    string
		response string
		success bool
	}{
		{
			name:     "long content succeeds",
			response: `{"choices":[{"message":{"content":"ответ модели"}}]}`,
			success:  true,
		},
		{
			name:     "reasoning is ignored",
			response: `{"choices":[{"message":{"content":"","reasoning_content":"длинное размышление модели"}}]}`,
			success:  false,
		},
		{
			name:     "short content fails",
			response: `{"choices":[{"message":{"content":"42"}}]}`,
			success:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tt.response)
			}))
			defer server.Close()

			s, err := store.New(filepath.Join(t.TempDir(), "models.db"))
			if err != nil {
				t.Fatalf("store.New: %v", err)
			}
			defer s.Close()

			checker := NewModelChecker(s, "token", map[string]string{"openrouter": server.URL})
			checker.Check("openrouter", "test-model")

			result, exists, err := s.GetLatestModelCheckResult("openrouter", "test-model")
			if err != nil {
				t.Fatalf("GetLatestModelCheckResult: %v", err)
			}
			if !exists {
				t.Fatal("check result was not saved")
			}
			if result.Success != tt.success {
				t.Errorf("success = %v, want %v; error = %q", result.Success, tt.success, result.Error)
			}
		})
	}
}
