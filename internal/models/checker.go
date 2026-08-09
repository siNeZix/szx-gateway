package models

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"szx-gateway/internal/store"
)

type ModelChecker struct {
	store  *store.Store
	token  string
	urls   map[string]string
	client *http.Client
	stop   chan struct{}
	done   chan struct{}
}

func NewModelChecker(s *store.Store, token string, urls map[string]string) *ModelChecker {
	return &ModelChecker{store: s, token: token, urls: urls, client: &http.Client{Timeout: 2 * time.Minute}, stop: make(chan struct{}), done: make(chan struct{})}
}

func (c *ModelChecker) Start() { go c.run() }
func (c *ModelChecker) Stop()  { close(c.stop); <-c.done }

func (c *ModelChecker) run() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	defer close(c.done)
	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			c.checkDue()
		}
	}
}

func (c *ModelChecker) checkDue() {
	configs, err := c.store.GetEnabledModelChecks()
	if err != nil {
		return
	}
	var wg sync.WaitGroup
	for _, config := range configs {
		latest, exists, err := c.store.GetLatestModelCheckResult(config.Provider, config.Model)
		if err != nil {
			continue
		}
		interval := 10 * time.Minute
		if exists && !latest.Success {
			interval = 5 * time.Minute
		}
		currentHour := time.Now().UTC().Truncate(time.Hour)
		if exists && latest.Timestamp.UTC().Truncate(time.Hour).Equal(currentHour) && time.Since(latest.Timestamp) < interval {
			continue
		}
		if exists && !latest.Timestamp.UTC().Truncate(time.Hour).Equal(currentHour) {
			// New hour must receive a real probe, even when regular interval has not elapsed.
		} else if exists && time.Since(latest.Timestamp) < interval {
			continue
		}
		wg.Add(1)
		go func(config store.ModelCheckConfig) { defer wg.Done(); c.Check(config.Provider, config.Model) }(config)
	}
	wg.Wait()
}

func (c *ModelChecker) Check(provider, model string) {
	a, b := rand.IntN(900000)+10000, rand.IntN(900000)+10000
	prompt := fmt.Sprintf("Return decimal sum of %d and %d. Output only result number.", a, b)
	body, _ := json.Marshal(map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": prompt}}, "temperature": 0, "max_tokens": 16})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, c.urls[provider]+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		_ = c.store.AddModelCheckResult(provider, model, false, err.Error())
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.client.Do(req)
	if err != nil {
		_ = c.store.AddModelCheckResult(provider, model, false, err.Error())
		return
	}
	defer res.Body.Close()
	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		_ = c.store.AddModelCheckResult(provider, model, false, err.Error())
		return
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		errMsg := payload.Error.Message
		if errMsg == "" {
			errMsg = "HTTP " + res.Status
		}
		_ = c.store.AddModelCheckResult(provider, model, false, errMsg)
		return
	}
	content := ""
	if len(payload.Choices) > 0 {
		content = strings.TrimSpace(payload.Choices[0].Message.Content)
	}
	if utf8.RuneCountInString(content) <= 2 {
		_ = c.store.AddModelCheckResult(provider, model, false, fmt.Sprintf("ожидался ответ длиннее 2 символов, получено %q", content))
		return
	}
	_ = c.store.AddModelCheckResult(provider, model, true, "")
}
