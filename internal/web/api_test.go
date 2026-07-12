package web_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"szx-gateway/internal/config"
	"szx-gateway/internal/keys"
	"szx-gateway/internal/models"
	"szx-gateway/internal/proxies"
	"szx-gateway/internal/store"
	"szx-gateway/internal/web"
)

// apiEnvelope распаковывает единый конверт ответа {data:...} или {error:...}.
type apiEnvelope struct {
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
}

func doJSON(t *testing.T, srv *httptest.Server, method, path string, body any) apiEnvelope {
	t.Helper()

	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("path=%s status=%d body not JSON envelope: %s\nraw=%s", path, resp.StatusCode, err, raw)
	}
	t.Logf("%s %s → %d, data=%d bytes, error=%q", method, path, resp.StatusCode, len(env.Data), env.Error)

	if env.Error != "" && resp.StatusCode < 400 {
		t.Errorf("error set but status=%d", resp.StatusCode)
	}
	if env.Error == "" && resp.StatusCode >= 400 {
		t.Errorf("status=%d but no error field", resp.StatusCode)
	}
	return env
}

// newTestServer поднимает WebServer с in-memory-like SQLite и реальными KeyPool.
// Конфиг без auth, чтобы тесты не возились с Basic Auth.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "api_test.db")
	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	cfg := &config.Config{} // пустой = без auth

	pool, err := keys.NewKeyPool(s, "openrouter")
	if err != nil {
		t.Fatalf("NewKeyPool: %v", err)
	}
	pools := map[string]*keys.KeyPool{"openrouter": pool}

	// RankingManager не стартуем — нужен только как zero-value для API.
	rm := models.NewRankingManager(s, 0)

	ws := web.NewWebServer(cfg, s, rm, pools, proxies.NewPool(s))
	mux := http.NewServeMux()
	ws.Start(mux)

	return httptest.NewServer(mux)
}

// TestAPI_Contract проверяет единый конверт {data}/{error} на наборе эндпоинтов.
func TestAPI_Contract(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	// 1. GET /api/v2/providers → массив с двумя провайдерами.
	env := doJSON(t, srv, "GET", "/api/v2/providers", nil)
	if env.Error != "" {
		t.Fatalf("providers: %s", env.Error)
	}
	var providers []map[string]any
	if err := json.Unmarshal(env.Data, &providers); err != nil {
		t.Fatalf("unmarshal providers: %v", err)
	}
	if len(providers) != 3 {
		t.Fatalf("expected 3 providers, got %d", len(providers))
	}
	for _, p := range providers {
		id, _ := p["id"].(string)
		if id != "openrouter" && id != "aihubmix" && id != "google" {
			t.Errorf("unexpected provider id: %v", p["id"])
		}
	}

	// 2. GET /api/v2/stats?provider=openrouter → есть general/models/keys/top_models.
	env = doJSON(t, srv, "GET", "/api/v2/stats?provider=openrouter", nil)
	var stats map[string]any
	if err := json.Unmarshal(env.Data, &stats); err != nil {
		t.Fatalf("unmarshal stats: %v", err)
	}
	for _, key := range []string{"general", "models", "keys", "daily_limits", "top_models", "refreshed_at"} {
		if _, ok := stats[key]; !ok {
			t.Errorf("stats missing key %q; got %v", key, keysOf(stats))
		}
	}
	for _, key := range []string{"models", "keys", "top_models", "free_models", "usage_trend"} {
		if _, ok := stats[key].([]any); !ok {
			t.Errorf("stats %s not array: %T", key, stats[key])
		}
	}

	// 3. GET /api/v2/models?provider=openrouter → top_models + free_models как массивы.
	env = doJSON(t, srv, "GET", "/api/v2/models?provider=openrouter", nil)
	var models map[string]any
	if err := json.Unmarshal(env.Data, &models); err != nil {
		t.Fatalf("unmarshal models: %v", err)
	}
	if _, ok := models["top_models"].([]any); !ok {
		t.Errorf("top_models not array: %T", models["top_models"])
	}
	if _, ok := models["free_models"].([]any); !ok {
		t.Errorf("free_models not array: %T", models["free_models"])
	}

	// 4. GET на POST-only эндпоинт → 405 с error.
	env = doJSON(t, srv, "GET", "/api/v2/keys/bulk", nil)
	if env.Error == "" {
		t.Errorf("expected error on GET /api/v2/keys/bulk")
	}
}

// TestAPI_KeysLifecycle прогоняет add → list → disable → list → delete → list.
func TestAPI_KeysLifecycle(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	// Добавляем 2 ключа.
	env := doJSON(t, srv, "POST", "/api/v2/keys", map[string]any{
		"provider": "openrouter",
		"keys":     []string{"sk-or-v1-aaaaaaaaaaaaaaa", "sk-or-v1-bbbbbbbbbbbbbbb"},
	})
	var addResp struct {
		Added int `json:"added"`
	}
	if err := json.Unmarshal(env.Data, &addResp); err != nil {
		t.Fatalf("unmarshal add resp: %v", err)
	}
	if addResp.Added != 2 {
		t.Fatalf("expected added=2, got %d", addResp.Added)
	}

	// Список: 2 ключа.
	env = doJSON(t, srv, "GET", "/api/v2/keys?provider=openrouter", nil)
	var list []map[string]any
	if err := json.Unmarshal(env.Data, &list); err != nil {
		t.Fatalf("unmarshal keys list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(list))
	}
	// Сырой ключ не должен утекать.
	for _, k := range list {
		if _, hasRaw := k["raw_key"]; hasRaw {
			t.Errorf("raw_key leaked into API: %v", k)
		}
		if _, hasMasked := k["masked_key"]; !hasMasked {
			t.Errorf("missing masked_key: %v", k)
		}
	}

	// Соберём хэши для bulk.
	hashes := make([]string, 0, len(list))
	for _, k := range list {
		if h, ok := k["key_hash"].(string); ok {
			hashes = append(hashes, h)
		}
	}
	if len(hashes) != 2 {
		t.Fatalf("expected 2 hashes, got %d", len(hashes))
	}

	// Disable обоих.
	env = doJSON(t, srv, "POST", "/api/v2/keys/bulk", map[string]any{
		"provider": "openrouter",
		"hashes":   hashes,
		"action":   "disable",
	})
	var bulk struct {
		Action   string `json:"action"`
		Affected int    `json:"affected"`
	}
	if err := json.Unmarshal(env.Data, &bulk); err != nil {
		t.Fatalf("unmarshal bulk resp: %v", err)
	}
	if bulk.Affected != 2 {
		t.Fatalf("expected affected=2, got %d", bulk.Affected)
	}

	// Фильтр по status=disabled → 2 ключа.
	env = doJSON(t, srv, "GET", "/api/v2/keys?provider=openrouter&status=disabled", nil)
	if err := json.Unmarshal(env.Data, &list); err != nil {
		t.Fatalf("unmarshal disabled list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 disabled keys, got %d", len(list))
	}

	// Delete обоих.
	doJSON(t, srv, "POST", "/api/v2/keys/bulk", map[string]any{
		"provider": "openrouter",
		"hashes":   hashes,
		"action":   "delete",
	})

	// Список пуст.
	env = doJSON(t, srv, "GET", "/api/v2/keys?provider=openrouter", nil)
	if err := json.Unmarshal(env.Data, &list); err != nil {
		t.Fatalf("unmarshal final list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 keys after delete, got %d", len(list))
	}
}

// TestAPI_ErrorCases проверяет валидацию на граничных входах.
func TestAPI_ErrorCases(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	// Неизвестный action в bulk → 400.
	env := doJSON(t, srv, "POST", "/api/v2/keys/bulk", map[string]any{
		"provider": "openrouter",
		"hashes":   []string{"deadbeef"},
		"action":   "nuke-everything",
	})
	if env.Error == "" {
		t.Errorf("expected error for unknown action")
	}

	// Пустой список keys в POST /api/v2/keys → 400.
	env = doJSON(t, srv, "POST", "/api/v2/keys", map[string]any{
		"provider": "openrouter",
		"keys":     []string{},
	})
	if env.Error == "" {
		t.Errorf("expected error for empty keys")
	}

	// Неизвестный provider → 400.
	env = doJSON(t, srv, "POST", "/api/v2/keys", map[string]any{
		"provider": "evil-corp",
		"keys":     []string{"sk-xxx"},
	})
	if env.Error == "" {
		t.Errorf("expected error for unknown provider")
	}
}

// keysOf — имена ключей мапы для отладочного вывода.
func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Удержим unused, если какой-то helper временно не используется.
var _ = sql.NullInt64{}
var _ = strings.TrimSpace
