package proxy

import (
	"encoding/json"
	"net/http"
)

func writeProxyError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": msg}})
}

func writeOpenAIError(w http.ResponseWriter, status int, message, param, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
		"message": message, "type": "invalid_request_error", "param": param, "code": code,
	}})
}
