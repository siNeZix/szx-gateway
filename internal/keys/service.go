package keys

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const aihubmixChatURL = "https://aihubmix.com/v1/chat/completions"

// CheckJob is a stable, JSON-ready snapshot of one manually started check.
type CheckJob struct {
	Running      bool      `json:"running"`
	Mode         string    `json:"mode"`
	Total        int       `json:"total"`
	Completed    int       `json:"completed"`
	Active       int       `json:"active"`
	Invalid      int       `json:"invalid"`
	DayExhausted int       `json:"day_exhausted"`
	RateLimited  int       `json:"rate_limited"`
	Errors       int       `json:"errors"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
}

// CheckService serializes manual AIHubMix checks so a user cannot create an
// upstream burst. Each request is cancellable and targets one exact key.
type CheckService struct {
	pool    *KeyPool
	checker *KeyChecker
	model   func() string
	client  *http.Client

	mu     sync.Mutex
	job    CheckJob
	cancel context.CancelFunc
}

func NewCheckService(pool *KeyPool, checker *KeyChecker, model func() string) *CheckService {
	return &CheckService{pool: pool, checker: checker, model: model, client: &http.Client{Timeout: 30 * time.Second}}
}

func (s *CheckService) Start(mode string) (CheckJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job.Running || (mode != "keys" && mode != "limits") {
		return s.job, false
	}
	keys := make([]*KeyState, 0)
	for _, key := range s.pool.AllKeys() {
		key.mu.Lock()
		disabled := key.Status == "disabled"
		key.mu.Unlock()
		if !disabled {
			keys = append(keys, key)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.job = CheckJob{Running: true, Mode: mode, Total: len(keys), StartedAt: time.Now()}
	if len(keys) == 0 {
		s.job.Running = false
		s.job.FinishedAt = time.Now()
		s.cancel = nil
		return s.job, true
	}
	go s.run(ctx, mode, keys)
	return s.job, true
}

func (s *CheckService) Cancel() CheckJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	return s.job
}

func (s *CheckService) Snapshot() CheckJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.job
}

func (s *CheckService) run(ctx context.Context, mode string, keys []*KeyState) {
	for _, key := range keys {
		if ctx.Err() != nil {
			break
		}
		before := s.keyStatus(key)
		if mode == "keys" {
			s.checker.CheckKey(ctx, key)
		} else {
			s.checkLimit(ctx, key)
		}
		s.record(key, before)
	}
	s.mu.Lock()
	s.job.Running = false
	s.job.FinishedAt = time.Now()
	s.cancel = nil
	s.mu.Unlock()
}

func (s *CheckService) checkLimit(ctx context.Context, key *KeyState) {
	model := s.model()
	if model == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"model":       model,
		"messages":    []map[string]string{{"role": "user", "content": "Return OK."}},
		"temperature": 0,
		"max_tokens":  2,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, aihubmixChatURL, bytes.NewReader(body))
	if err != nil {
		return
	}
	key.mu.Lock()
	rawKey := key.RawKey
	key.mu.Unlock()
	req.Header.Set("Authorization", "Bearer "+rawKey)
	req.Header.Set("Content-Type", "application/json")
	// This probe consumes the same free quota as the Status check.
	key.RegisterRequest(time.Now())
	s.pool.SyncKeyToDB(key)
	resp, err := s.client.Do(req)
	if err != nil {
		key.RollbackUsage()
		if ctx.Err() == nil {
			key.SetCooldown(time.Minute, "")
		}
		s.pool.SyncKeyToDB(key)
		return
	}
	defer resp.Body.Close()
	content, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	now := time.Now()
	text := strings.ToLower(string(content))
	key.mu.Lock()
	key.LastCheckedAt = now
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300 && (strings.Contains(text, "prevent abuse of free resources") || strings.Contains(text, "can only try")):
		key.markDayExhaustedLocked(now)
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		key.Status = "active"
	case resp.StatusCode == http.StatusUnauthorized || (resp.StatusCode == http.StatusForbidden && (strings.Contains(text, "invalid key") || strings.Contains(text, "invalid token") || strings.Contains(text, "key is disabled") || strings.Contains(text, "token is disabled"))):
		key.Status = "invalid"
	case resp.StatusCode == http.StatusTooManyRequests:
		if key.UsageToday > 0 {
			key.UsageToday--
		}
		key.Status = "rate_limited"
		key.CooldownUntil = now.Add(5 * time.Minute)
	default:
		if key.UsageToday > 0 {
			key.UsageToday--
		}
		key.CooldownUntil = now.Add(time.Minute)
	}
	key.mu.Unlock()
	s.pool.SyncKeyToDB(key)
}

func (s *CheckService) keyStatus(key *KeyState) string {
	key.mu.Lock()
	defer key.mu.Unlock()
	return key.Status
}

func (s *CheckService) record(key *KeyState, before string) {
	status := s.keyStatus(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.job.Completed++
	switch status {
	case "active":
		s.job.Active++
	case "invalid":
		s.job.Invalid++
	case "day_exhausted":
		s.job.DayExhausted++
	case "rate_limited":
		s.job.RateLimited++
	}
	if status == before && status != "active" && status != "invalid" && status != "day_exhausted" && status != "rate_limited" {
		s.job.Errors++
	}
}
