package models

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"szx-gateway/internal/store"
)

func TestRankingManager_FetchFree(t *testing.T) {
	// 1. Create a mock HTTP server to simulate OpenRouter models API
	mockORResponse := `{
		"data": [
			{"id": "google/gemini-2.5-flash:free", "name": "Gemini 2.5 Flash Free", "context_length": 1048576, "pricing": {"prompt": "0", "completion": "0"}, "architecture": {"modality": "text->text"}},
			{"id": "meta-llama/llama-3-8b-instruct:free", "name": "Llama 3 8B Free", "context_length": 8192, "pricing": {"prompt": "0.0001", "completion": "0.0002"}, "architecture": {"modality": "text->text"}},
			{"id": "openai/gpt-4o", "name": "GPT-4o Paid", "context_length": 128000, "pricing": {"prompt": "0.005", "completion": "0.015"}, "architecture": {"modality": "text->text"}},
			{"id": "openrouter/owl-alpha", "name": "Owl Alpha Cloaked Free", "context_length": 4096, "pricing": {"prompt": "0", "completion": "0"}, "architecture": {"modality": "text->text"}},
			{"id": "google/lyria-3-pro-preview", "name": "Lyria 3 Pro Preview Audio Free", "context_length": 4096, "pricing": {"prompt": "0", "completion": "0"}, "architecture": {"modality": "text+image->text+audio"}},
			{"id": "google/gemma-4-26b-a4b-it:free", "name": "Gemma 4 Vision Free", "context_length": 8192, "pricing": {"prompt": "0", "completion": "0"}, "architecture": {"modality": "text+image+video->text"}}
		]
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, mockORResponse)
	}))
	defer server.Close()

	// 2. Initialize a temporary sqlite store
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_models.db")
	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer s.Close()

	// 3. Create RankingManager, override URLs to avoid network requests
	rm := NewRankingManager(s, 10*time.Minute)
	rm.openRouterURL = server.URL
	rm.shirManURL = "http://localhost:12345/nonexistent" // will fail, that's fine

	// 4. Run fetchFree
	err = rm.fetchFree()
	if err != nil {
		t.Fatalf("fetchFree failed: %v", err)
	}

	// 5. Verify cached free models in memory
	freeModels := rm.GetFreeModels()
	// Should have:
	// - google/gemini-2.5-flash:free (explicit suffix + text->text)
	// - meta-llama/llama-3-8b-instruct:free (explicit suffix + text->text)
	// - openrouter/owl-alpha (cloaked zero-priced + text->text)
	// - google/gemma-4-26b-a4b-it:free (explicit suffix + ends with ->text)
	// Excuded:
	// - openai/gpt-4o (paid)
	// - google/lyria-3-pro-preview (zero priced, but audio output modality ->text+audio)
	if len(freeModels) != 4 {
		t.Fatalf("expected exactly 4 free models, got %d: %+v", len(freeModels), freeModels)
	}

	// Check if only valid free models are cached
	for _, m := range freeModels {
		if m.ID == "openai/gpt-4o" {
			t.Errorf("paid model openai/gpt-4o should not be present in free models list")
		}
		if m.ID == "google/lyria-3-pro-preview" {
			t.Errorf("non-chat model google/lyria-3-pro-preview should not be present in free models list")
		}
	}

	// Verify specific model data
	m1 := freeModels[0]
	if m1.ID != "google/gemini-2.5-flash:free" || m1.Name != "Gemini 2.5 Flash Free" || m1.ContextLength != 1048576 {
		t.Errorf("unexpected free model data: %+v", m1)
	}

	// Check cloaked free model
	foundOwl := false
	for _, m := range freeModels {
		if m.ID == "openrouter/owl-alpha" {
			foundOwl = true
			if m.Name != "Owl Alpha Cloaked Free" {
				t.Errorf("unexpected name for owl-alpha: %s", m.Name)
			}
		}
	}
	if !foundOwl {
		t.Errorf("openrouter/owl-alpha not found in free models list")
	}

	// 6. Test IsFreeModel
	if !rm.IsFreeModel("google/gemini-2.5-flash:free") {
		t.Errorf("IsFreeModel should return true for google/gemini-2.5-flash:free")
	}
	if !rm.IsFreeModel("openrouter/owl-alpha") {
		t.Errorf("IsFreeModel should return true for openrouter/owl-alpha")
	}
	if rm.IsFreeModel("openai/gpt-4o") {
		t.Errorf("IsFreeModel should return false for openai/gpt-4o")
	}
	if rm.IsFreeModel("google/lyria-3-pro-preview") {
		t.Errorf("IsFreeModel should return false for google/lyria-3-pro-preview")
	}

	// 7. Verify SQLite cache persistence
	cachedFromDB, err := s.GetCachedFreeModels()
	if err != nil {
		t.Fatalf("GetCachedFreeModels failed: %v", err)
	}
	if len(cachedFromDB) != 4 {
		t.Fatalf("expected 4 persisted free models in sqlite, got %d", len(cachedFromDB))
	}
}

func TestRankingManager_FetchAihubmixFree(t *testing.T) {
	// AIHubMix /v1/models filters available free models by suffix;
	// /api/v1/models enriches them with context/pricing/capabilities.
	mockAmResponse := `{
		"data": [
			{"id": "gpt-4o-mini", "name": "GPT-4o mini", "owned_by": "OpenAI"},
			{"id": "gpt-4o-mini-free", "name": "GPT-4o mini Free", "owned_by": "OpenAI"},
			{"id": "glm-4.7-flash-free", "name": "GLM 4.7 Flash Free", "owned_by": "Z.AI"},
			{"id": "kimi-for-coding-free", "name": "Kimi for coding Free", "owned_by": "Moonshot AI"},
			{"id": "gemini-2.5-flash", "name": "Gemini 2.5 Flash", "owned_by": "Google"}
		]
	}`
	mockDetailsResponse := `{
		"data": [
			{
				"model_id": "gpt-4o-mini-free",
				"desc": "Free GPT-4o mini",
				"types": "llm",
				"features": "tools,function_calling",
				"input_modalities": "text,image",
				"max_output": 16384,
				"context_length": 128000,
				"pricing": {"input": 0, "output": 0}
			},
			{
				"model_id": "glm-4.7-flash-free",
				"desc": "Free GLM flash",
				"types": "llm",
				"features": "thinking",
				"input_modalities": "text",
				"max_output": 8192,
				"context_length": 32000,
				"pricing": {"input": 0, "output": 0}
			}
		]
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.URL.Path == "/details" {
			fmt.Fprintln(w, mockDetailsResponse)
			return
		}
		fmt.Fprintln(w, mockAmResponse)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_am.db")
	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer s.Close()

	rm := NewRankingManager(s, 10*time.Minute)
	rm.aihubmixURL = server.URL
	rm.aihubmixInfo = server.URL + "/details"
	rm.openRouterURL = "http://localhost:12345/nonexistent"
	rm.shirManURL = "http://localhost:12345/nonexistent"

	if err := rm.fetchAihubmixFree(); err != nil {
		t.Fatalf("fetchAihubmixFree failed: %v", err)
	}

	free := rm.GetAihubmixFreeModels()
	if len(free) != 3 {
		t.Fatalf("expected 3 free models, got %d: %+v", len(free), free)
	}

	if !rm.IsAihubmixFreeModel("gpt-4o-mini-free") {
		t.Errorf("IsAihubmixFreeModel should return true for gpt-4o-mini-free")
	}
	if free[0].ContextLength != 128000 || free[0].MaxOutput != 16384 || free[0].Features != "tools,function_calling" {
		t.Errorf("AIHubMix free model details were not cached: %+v", free[0])
	}
	if !rm.IsAihubmixFreeModel("glm-4.7-flash-free") {
		t.Errorf("IsAihubmixFreeModel should return true for glm-4.7-flash-free")
	}
	if rm.IsAihubmixFreeModel("gpt-4o-mini") {
		t.Errorf("IsAihubmixFreeModel should return false for paid gpt-4o-mini")
	}
	if rm.IsAihubmixFreeModel("gemini-2.5-flash") {
		t.Errorf("IsAihubmixFreeModel should return false for paid gemini-2.5-flash")
	}

	cached, err := s.GetCachedAihubmixFreeModels()
	if err != nil {
		t.Fatalf("GetCachedAihubmixFreeModels failed: %v", err)
	}
	if len(cached) != 3 {
		t.Fatalf("expected 3 persisted AIHubMix free models, got %d", len(cached))
	}
}
