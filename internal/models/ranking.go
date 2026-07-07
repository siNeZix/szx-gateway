package models

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"szx-gateway/internal/store"
)

type ShirManResponse struct {
	Models []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Rank          int    `json:"rank"`
		ContextLength int64  `json:"contextLength"`
	} `json:"models"`
}

type OpenRouterModelsResponse struct {
	Data []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		ContextLength int64  `json:"context_length"`
		Pricing       struct {
			Prompt     string `json:"prompt"`
			Completion string `json:"completion"`
		} `json:"pricing"`
		Architecture struct {
			Modality string `json:"modality"`
		} `json:"architecture"`
		TopProvider struct {
			MaxCompletionTokens int64 `json:"max_completion_tokens"`
		} `json:"top_provider"`
	} `json:"data"`
}

type AihubmixModelsResponse struct {
	Data []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		OwnedBy string `json:"owned_by"`
	} `json:"data"`
}

type AihubmixModelDetailsResponse struct {
	Data []AihubmixModelDetails `json:"data"`
}

type AihubmixModelDetails struct {
	ModelID         string `json:"model_id"`
	Desc            string `json:"desc"`
	Types           string `json:"types"`
	Features        string `json:"features"`
	InputModalities string `json:"input_modalities"`
	MaxOutput       int64  `json:"max_output"`
	ContextLength   int64  `json:"context_length"`
	Pricing         struct {
		Input  float64 `json:"input"`
		Output float64 `json:"output"`
	} `json:"pricing"`
}

type RankingManager struct {
	store      *store.Store
	refreshInt time.Duration

	shirManURL    string
	openRouterURL string
	aihubmixURL   string
	aihubmixInfo  string

	mu           sync.RWMutex
	models       []store.DBModel
	freeModels   []store.DBModel
	aihubmixFree []store.DBModel
	fallbackID   string
}

func NewRankingManager(s *store.Store, refreshInterval time.Duration) *RankingManager {
	return &RankingManager{
		store:         s,
		refreshInt:    refreshInterval,
		shirManURL:    "https://shir-man.com/api/free-llm/top-models",
		openRouterURL: "https://openrouter.ai/api/v1/models",
		aihubmixURL:   "https://aihubmix.com/v1/models",
		aihubmixInfo:  "https://aihubmix.com/api/v1/models?type=llm",
		fallbackID:    "openrouter/free",
	}
}

func (rm *RankingManager) Start() {
	// Try loading from SQLite cache first
	if cached, err := rm.store.GetCachedModels(); err == nil && len(cached) > 0 {
		rm.mu.Lock()
		rm.models = cached
		rm.mu.Unlock()
		log.Printf("Loaded %d models from database cache", len(cached))
	}
	if cachedFree, err := rm.store.GetCachedFreeModels(); err == nil && len(cachedFree) > 0 {
		rm.mu.Lock()
		rm.freeModels = cachedFree
		rm.mu.Unlock()
		log.Printf("Loaded %d free models from database cache", len(cachedFree))
	}
	if cachedAm, err := rm.store.GetCachedAihubmixFreeModels(); err == nil && len(cachedAm) > 0 {
		rm.mu.Lock()
		rm.aihubmixFree = cachedAm
		rm.mu.Unlock()
		log.Printf("Loaded %d AIHubMix free models from database cache", len(cachedAm))
	}

	// Initial fetch
	if err := rm.fetch(); err != nil {
		log.Printf("Initial Shir-Man ranking fetch failed: %v", err)
	}
	if err := rm.fetchFree(); err != nil {
		log.Printf("Initial OpenRouter free models fetch failed: %v", err)
	}
	if err := rm.fetchAihubmixFree(); err != nil {
		log.Printf("Initial AIHubMix free models fetch failed: %v", err)
	}

	// Periodical background fetch
	go func() {
		ticker := time.NewTicker(rm.refreshInt)
		defer ticker.Stop()
		for range ticker.C {
			if err := rm.fetch(); err != nil {
				log.Printf("Shir-Man ranking fetch failed: %v", err)
			}
			if err := rm.fetchFree(); err != nil {
				log.Printf("OpenRouter free models fetch failed: %v", err)
			}
			if err := rm.fetchAihubmixFree(); err != nil {
				log.Printf("AIHubMix free models fetch failed: %v", err)
			}
		}
	}()
}

func (rm *RankingManager) fetch() error {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(rm.shirManURL)
	if err != nil {
		return fmt.Errorf("failed to make HTTP request to Shir-Man API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad HTTP status from Shir-Man API: %s", resp.Status)
	}

	var data ShirManResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("failed to decode Shir-Man response: %w", err)
	}

	if len(data.Models) == 0 {
		return fmt.Errorf("Shir-Man API returned 0 models")
	}

	var dbModels []store.DBModel
	now := time.Now()
	for _, m := range data.Models {
		dbModels = append(dbModels, store.DBModel{
			ID:            m.ID,
			Name:          m.Name,
			Rank:          m.Rank,
			ContextLength: m.ContextLength,
			UpdatedAt:     now,
		})
	}

	// Update memory cache
	rm.mu.Lock()
	rm.models = dbModels
	rm.mu.Unlock()

	// Cache in SQLite
	if err := rm.store.CacheModels(dbModels); err != nil {
		log.Printf("Failed to cache models in DB: %v", err)
	}

	log.Printf("Updated model rankings. Total free models: %d. Top-1: %s", len(dbModels), dbModels[0].ID)
	return nil
}

func (rm *RankingManager) fetchFree() error {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(rm.openRouterURL)
	if err != nil {
		return fmt.Errorf("failed to make HTTP request to OpenRouter API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad HTTP status from OpenRouter API: %s", resp.Status)
	}

	var data OpenRouterModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("failed to decode OpenRouter models: %w", err)
	}

	var dbModels []store.DBModel
	now := time.Now()
	for _, m := range data.Data {
		// ponytail: free if :free suffix OR both pricing fields are exactly "0".
		// We only want chat-compatible models ending with "->text" (excludes audio-only like lyria).
		// Comparing string "0" is faster and safer than parsing float64.
		isFree := (len(m.ID) > 5 && m.ID[len(m.ID)-5:] == ":free") || (m.Pricing.Prompt == "0" && m.Pricing.Completion == "0")
		isChat := len(m.Architecture.Modality) > 6 && m.Architecture.Modality[len(m.Architecture.Modality)-6:] == "->text"

		if isFree && isChat {
			inputPrice, _ := strconv.ParseFloat(m.Pricing.Prompt, 64)
			outputPrice, _ := strconv.ParseFloat(m.Pricing.Completion, 64)
			dbModels = append(dbModels, store.DBModel{
				ID:            m.ID,
				Name:          m.Name,
				ContextLength: m.ContextLength,
				MaxOutput:     m.TopProvider.MaxCompletionTokens,
				Type:          "llm",
				Modalities:    m.Architecture.Modality,
				InputPrice:    inputPrice,
				OutputPrice:   outputPrice,
				UpdatedAt:     now,
			})
		}
	}

	if len(dbModels) == 0 {
		return fmt.Errorf("OpenRouter API returned 0 free models")
	}

	// Update memory cache
	rm.mu.Lock()
	rm.freeModels = dbModels
	rm.mu.Unlock()

	// Cache in SQLite
	if err := rm.store.CacheFreeModels(dbModels); err != nil {
		log.Printf("Failed to cache free models in DB: %v", err)
	}

	log.Printf("Updated OpenRouter free models cache. Total free models: %d", len(dbModels))
	return nil
}

// fetchAihubmixFree pulls the AIHubMix public /v1/models list and keeps only
// entries whose ID ends with "-free". The endpoint has no pricing/modality
// fields, so the suffix is the only signal. Public, no key required.
func (rm *RankingManager) fetchAihubmixFree() error {
	client := &http.Client{Timeout: 10 * time.Second}
	details, err := rm.fetchAihubmixModelDetails(client)
	if err != nil {
		log.Printf("AIHubMix model details fetch failed: %v", err)
	}

	resp, err := client.Get(rm.aihubmixURL)
	if err != nil {
		return fmt.Errorf("failed to make HTTP request to AIHubMix API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad HTTP status from AIHubMix API: %s", resp.Status)
	}

	var data AihubmixModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("failed to decode AIHubMix models: %w", err)
	}

	var dbModels []store.DBModel
	now := time.Now()
	for _, m := range data.Data {
		// ponytail: AIHubMix /v1/models has no pricing field; "-free" suffix is
		// the only free signal. Falls over if AIHubMix ships a free model
		// without the suffix; add a name/pricing check then.
		if strings.HasSuffix(m.ID, "-free") {
			d := details[m.ID]
			dbModels = append(dbModels, store.DBModel{
				ID:            m.ID,
				Name:          m.ID,
				ContextLength: d.ContextLength,
				MaxOutput:     d.MaxOutput,
				Type:          d.Types,
				Features:      d.Features,
				Modalities:    d.InputModalities,
				InputPrice:    d.Pricing.Input,
				OutputPrice:   d.Pricing.Output,
				Description:   d.Desc,
				UpdatedAt:     now,
			})
		}
	}

	if len(dbModels) == 0 {
		return fmt.Errorf("AIHubMix API returned 0 free models")
	}

	rm.mu.Lock()
	rm.aihubmixFree = dbModels
	rm.mu.Unlock()

	if err := rm.store.CacheAihubmixFreeModels(dbModels); err != nil {
		log.Printf("Failed to cache AIHubMix free models in DB: %v", err)
	}

	log.Printf("Updated AIHubMix free models cache. Total free models: %d", len(dbModels))
	return nil
}

func (rm *RankingManager) fetchAihubmixModelDetails(client *http.Client) (map[string]AihubmixModelDetails, error) {
	resp, err := client.Get(rm.aihubmixInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to make HTTP request to AIHubMix models API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad HTTP status from AIHubMix models API: %s", resp.Status)
	}

	var data AihubmixModelDetailsResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode AIHubMix model details: %w", err)
	}

	res := make(map[string]AihubmixModelDetails, len(data.Data))
	for _, m := range data.Data {
		res[m.ModelID] = m
	}
	return res, nil
}

func (rm *RankingManager) ResolveAlias(alias string) (string, bool) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	switch alias {
	case "top1":
		if len(rm.models) > 0 {
			return rm.models[0].ID, true
		}
	case "top2":
		if len(rm.models) > 1 {
			return rm.models[1].ID, true
		}
	case "top3":
		if len(rm.models) > 2 {
			return rm.models[2].ID, true
		}
	}

	return "", false
}

func (rm *RankingManager) IsFreeModel(modelID string) bool {
	if modelID == rm.fallbackID {
		return true
	}

	rm.mu.RLock()
	defer rm.mu.RUnlock()

	for _, m := range rm.models {
		if m.ID == modelID {
			return true
		}
	}
	for _, m := range rm.freeModels {
		if m.ID == modelID {
			return true
		}
	}
	return false
}

func (rm *RankingManager) GetTopModels() []store.DBModel {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	// Return a copy to prevent race conditions or modifications
	res := make([]store.DBModel, len(rm.models))
	copy(res, rm.models)
	return res
}

func (rm *RankingManager) GetFreeModels() []store.DBModel {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	res := make([]store.DBModel, len(rm.freeModels))
	copy(res, rm.freeModels)
	return res
}

func (rm *RankingManager) IsAihubmixFreeModel(modelID string) bool {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	for _, m := range rm.aihubmixFree {
		if m.ID == modelID {
			return true
		}
	}
	return false
}

func (rm *RankingManager) GetAihubmixFreeModels() []store.DBModel {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	res := make([]store.DBModel, len(rm.aihubmixFree))
	copy(res, rm.aihubmixFree)
	return res
}
