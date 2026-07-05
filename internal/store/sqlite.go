package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type DBKey struct {
	KeyHash           string    `json:"key_hash"`
	MaskedKey         string    `json:"masked_key"`
	Status            string    `json:"status"`
	LimitRemaining    int64     `json:"limit_remaining"`
	UsageToday        int64     `json:"usage_today"`
	MaxLimit          int64     `json:"max_limit"`
	IsFreeTier        bool      `json:"is_free_tier"`
	RateLimitReq      int       `json:"rate_limit_req"`
	RateLimitInterval string    `json:"rate_limit_interval"`
	CooldownUntil     time.Time `json:"cooldown_until"`
	LastCheckedAt     time.Time `json:"last_checked_at"`
	LastUsedAt        time.Time `json:"last_used_at"`
	RawKey            string    `json:"-"` // критично: никогда не отдаём сырой ключ в API
}

type DBRequest struct {
	ID               int64
	Timestamp        time.Time
	KeyHash          string
	Model            string
	StatusCode       int
	PromptTokens     int
	CompletionTokens int
	LatencyMs        int64
	ErrorMsg         string
	TTFTMs           int64
	IsStream         bool
	Provider         string
}

type DBRateLimit struct {
	ID                int64
	Timestamp         time.Time
	KeyHash           string
	Source            string // 'proxy' or 'checker'
	LimitTotal        sql.NullInt64
	LimitRemaining    sql.NullInt64
	Usage             sql.NullInt64
	RateLimitReq      sql.NullInt64
	RateLimitInterval sql.NullString
	ResetRaw          sql.NullString
}

type DBModel struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Rank          int       `json:"rank"`
	ContextLength int64     `json:"context_length"`
	MaxOutput     int64     `json:"max_output"`
	Type          string    `json:"type"`
	Features      string    `json:"features"`
	Modalities    string    `json:"modalities"`
	InputPrice    float64   `json:"input_price"`
	OutputPrice   float64   `json:"output_price"`
	Description   string    `json:"description"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type ModelUsage struct {
	Provider  string
	KeyHash   string
	Model     string
	Day       string
	Requests  int64
	Tokens    int64
	Exhausted bool
	Frozen    bool
}

type ModelUsageStats struct {
	Model    string
	Requests int64
	Tokens   int64
}

// ModelUsageTrend — агрегированная статистика за один день по всему провайдеру.
type ModelUsageTrend struct {
	Day        string `json:"day"`
	Requests   int64  `json:"requests"`
	Tokens     int64  `json:"tokens"`
	LatencyAvg int64  `json:"latency_avg_ms"`
	Errors     int64  `json:"errors"`
}

// UsageBucket — агрегат запросов за локальный временной бакет.
type UsageBucket struct {
	Bucket     string `json:"bucket"`
	Requests   int64  `json:"requests"`
	Tokens     int64  `json:"tokens"`
	LatencyAvg int64  `json:"latency_avg_ms"`
	Errors     int64  `json:"errors"`
}

type RequestLogItem struct {
	ID         int64  `json:"id"`
	Timestamp  string `json:"timestamp"`
	Provider   string `json:"provider"`
	KeyHash    string `json:"key_hash"`
	Model      string `json:"model"`
	Status     int    `json:"status_code"`
	StatusText string `json:"status_text"`
	Tokens     int64  `json:"tokens"`
	LatencyMs  int64  `json:"latency_ms"`
	TTFTMs     int64  `json:"ttft_ms"`
	IsStream   bool   `json:"is_stream"`
}

func HashKey(key string) string {
	h := sha256.New()
	h.Write([]byte(key))
	return hex.EncodeToString(h.Sum(nil))
}

func MaskKey(key string) string {
	if len(key) <= 15 {
		return "sk-or-v1-***"
	}
	return fmt.Sprintf("%s...%s", key[:12], key[len(key)-6:])
}

func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// Optimize SQLite performance for concurrent usage
	db.SetMaxOpenConns(1) // SQLite is single-writer anyway, modernc does best with 1 open conn or WAL mode

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to run database migrations: %w", err)
	}

	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	queries := []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA synchronous=NORMAL;`,
		`CREATE TABLE IF NOT EXISTS keys (
			key_hash TEXT PRIMARY KEY,
			masked_key TEXT NOT NULL,
			status TEXT NOT NULL,
			limit_remaining INTEGER NOT NULL DEFAULT 0,
			usage_today INTEGER NOT NULL DEFAULT 0,
			max_limit INTEGER NOT NULL DEFAULT 0,
			is_free_tier INTEGER NOT NULL DEFAULT 1,
			rate_limit_req INTEGER NOT NULL DEFAULT 20,
			rate_limit_interval TEXT NOT NULL DEFAULT '1m',
			cooldown_until DATETIME NOT NULL,
			last_checked_at DATETIME NOT NULL,
			last_used_at DATETIME NOT NULL,
			raw_key TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE TABLE IF NOT EXISTS requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp DATETIME NOT NULL,
			key_hash TEXT NOT NULL,
			model TEXT NOT NULL,
			status_code INTEGER NOT NULL,
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			latency_ms INTEGER NOT NULL DEFAULT 0,
			error_msg TEXT,
			ttft_ms INTEGER NOT NULL DEFAULT 0,
			is_stream INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE INDEX IF NOT EXISTS idx_requests_timestamp ON requests(timestamp);`,
		`CREATE INDEX IF NOT EXISTS idx_requests_key_hash ON requests(key_hash);`,
		`CREATE TABLE IF NOT EXISTS rate_limits_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp DATETIME NOT NULL,
			key_hash TEXT NOT NULL,
			source TEXT NOT NULL,
			limit_total INTEGER,
			limit_remaining INTEGER,
			usage INTEGER,
			rate_limit_req INTEGER,
			rate_limit_interval TEXT,
			reset_raw TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_rl_timestamp ON rate_limits_log(timestamp);`,
		`CREATE INDEX IF NOT EXISTS idx_rl_key_hash ON rate_limits_log(key_hash);`,
		`CREATE TABLE IF NOT EXISTS models_cache (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			rank INTEGER NOT NULL,
			context_length INTEGER NOT NULL,
			updated_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS free_models_cache (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			context_length INTEGER NOT NULL,
			max_output INTEGER NOT NULL DEFAULT 0,
			type TEXT NOT NULL DEFAULT '',
			features TEXT NOT NULL DEFAULT '',
			modalities TEXT NOT NULL DEFAULT '',
			input_price REAL NOT NULL DEFAULT 0,
			output_price REAL NOT NULL DEFAULT 0,
			description TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS aihubmix_free_models_cache (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			context_length INTEGER NOT NULL DEFAULT 0,
			max_output INTEGER NOT NULL DEFAULT 0,
			type TEXT NOT NULL DEFAULT '',
			features TEXT NOT NULL DEFAULT '',
			modalities TEXT NOT NULL DEFAULT '',
			input_price REAL NOT NULL DEFAULT 0,
			output_price REAL NOT NULL DEFAULT 0,
			description TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS model_usage (
			provider TEXT NOT NULL,
			key_hash TEXT NOT NULL,
			model TEXT NOT NULL,
			day TEXT NOT NULL,
			requests INTEGER NOT NULL DEFAULT 0,
			tokens INTEGER NOT NULL DEFAULT 0,
			exhausted INTEGER NOT NULL DEFAULT 0,
			latency_sum_ms INTEGER NOT NULL DEFAULT 0,
			errors INTEGER NOT NULL DEFAULT 0,
			updated_at DATETIME NOT NULL,
			PRIMARY KEY (provider, key_hash, model, day)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_model_usage_day ON model_usage(provider, day, model);`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("migration query failed (%s): %w", q, err)
		}
	}

	// Migration for databases created before raw_key/ttft_ms/is_stream/provider existed in the CREATE above.
	// On fresh DBs the columns already exist, so these errors are ignored.
	_, _ = s.db.Exec(`ALTER TABLE keys ADD COLUMN raw_key TEXT NOT NULL DEFAULT '';`)
	_, _ = s.db.Exec(`ALTER TABLE keys ADD COLUMN provider TEXT NOT NULL DEFAULT 'openrouter';`)
	_, _ = s.db.Exec(`ALTER TABLE requests ADD COLUMN ttft_ms INTEGER NOT NULL DEFAULT 0;`)
	_, _ = s.db.Exec(`ALTER TABLE requests ADD COLUMN is_stream INTEGER NOT NULL DEFAULT 0;`)
	_, _ = s.db.Exec(`ALTER TABLE requests ADD COLUMN provider TEXT NOT NULL DEFAULT 'openrouter';`)
	_, _ = s.db.Exec(`ALTER TABLE free_models_cache ADD COLUMN max_output INTEGER NOT NULL DEFAULT 0;`)
	_, _ = s.db.Exec(`ALTER TABLE free_models_cache ADD COLUMN type TEXT NOT NULL DEFAULT '';`)
	_, _ = s.db.Exec(`ALTER TABLE free_models_cache ADD COLUMN features TEXT NOT NULL DEFAULT '';`)
	_, _ = s.db.Exec(`ALTER TABLE free_models_cache ADD COLUMN modalities TEXT NOT NULL DEFAULT '';`)
	_, _ = s.db.Exec(`ALTER TABLE free_models_cache ADD COLUMN input_price REAL NOT NULL DEFAULT 0;`)
	_, _ = s.db.Exec(`ALTER TABLE free_models_cache ADD COLUMN output_price REAL NOT NULL DEFAULT 0;`)
	_, _ = s.db.Exec(`ALTER TABLE free_models_cache ADD COLUMN description TEXT NOT NULL DEFAULT '';`)
	_, _ = s.db.Exec(`ALTER TABLE aihubmix_free_models_cache ADD COLUMN context_length INTEGER NOT NULL DEFAULT 0;`)
	_, _ = s.db.Exec(`ALTER TABLE aihubmix_free_models_cache ADD COLUMN max_output INTEGER NOT NULL DEFAULT 0;`)
	_, _ = s.db.Exec(`ALTER TABLE aihubmix_free_models_cache ADD COLUMN type TEXT NOT NULL DEFAULT '';`)
	_, _ = s.db.Exec(`ALTER TABLE aihubmix_free_models_cache ADD COLUMN features TEXT NOT NULL DEFAULT '';`)
	_, _ = s.db.Exec(`ALTER TABLE aihubmix_free_models_cache ADD COLUMN modalities TEXT NOT NULL DEFAULT '';`)
	_, _ = s.db.Exec(`ALTER TABLE aihubmix_free_models_cache ADD COLUMN input_price REAL NOT NULL DEFAULT 0;`)
	_, _ = s.db.Exec(`ALTER TABLE aihubmix_free_models_cache ADD COLUMN output_price REAL NOT NULL DEFAULT 0;`)
	_, _ = s.db.Exec(`ALTER TABLE aihubmix_free_models_cache ADD COLUMN description TEXT NOT NULL DEFAULT '';`)
	_, _ = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_keys_provider ON keys(provider);`)
	_, _ = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_requests_provider ON requests(provider);`)
	_, _ = s.db.Exec(`ALTER TABLE model_usage ADD COLUMN latency_sum_ms INTEGER NOT NULL DEFAULT 0;`)
	_, _ = s.db.Exec(`ALTER TABLE model_usage ADD COLUMN errors INTEGER NOT NULL DEFAULT 0;`)
	_, _ = s.db.Exec(`ALTER TABLE model_usage ADD COLUMN freeze_count INTEGER NOT NULL DEFAULT 0;`)
	_, _ = s.db.Exec(`ALTER TABLE model_usage ADD COLUMN frozen_until TEXT NOT NULL DEFAULT '';`)
	// ponytail: 10 запросов/аккаунт/сутки — подтверждённый лимит AIHubMix.
	// Умные per-model лимиты отключены, возвращаемся к единому MaxLimit=10.
	_, _ = s.db.Exec(`UPDATE keys SET max_limit = 10 WHERE provider = 'aihubmix' AND (max_limit = 0 OR max_limit IS NULL);`)
	_, _ = s.db.Exec(`UPDATE keys SET status = 'unchecked' WHERE provider = 'aihubmix' AND status = 'day_exhausted';`)

	return nil
}

func UTCDay(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

func (s *Store) GetModelUsage(provider, keyHash, model string, now time.Time) (ModelUsage, error) {
	u := ModelUsage{Provider: provider, KeyHash: keyHash, Model: model, Day: UTCDay(now)}
	var exhausted int
	var frozenUntil string
	err := s.db.QueryRow(`
		SELECT requests, tokens, exhausted, frozen_until
		FROM model_usage
		WHERE provider = ? AND key_hash = ? AND model = ? AND day = ?
	`, provider, keyHash, model, u.Day).Scan(&u.Requests, &u.Tokens, &exhausted, &frozenUntil)
	if err == sql.ErrNoRows {
		return u, nil
	}
	if err != nil {
		return u, err
	}
	u.Exhausted = exhausted != 0
	if frozenUntil != "" {
		if t, perr := time.Parse(time.RFC3339, frozenUntil); perr == nil && t.After(now) {
			u.Frozen = true
		}
	}
	return u, nil
}

func (s *Store) AddModelUsage(provider, keyHash, model string, now time.Time, requests, tokens, latencySumMs int64, errors int) error {
	_, err := s.db.Exec(`
		INSERT INTO model_usage (provider, key_hash, model, day, requests, tokens, latency_sum_ms, errors, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider, key_hash, model, day) DO UPDATE SET
			requests = requests + excluded.requests,
			tokens = tokens + excluded.tokens,
			latency_sum_ms = latency_sum_ms + excluded.latency_sum_ms,
			errors = errors + excluded.errors,
			updated_at = excluded.updated_at
	`, provider, keyHash, model, UTCDay(now), requests, tokens, latencySumMs, errors, now.UTC())
	return err
}

func (s *Store) MarkModelExhausted(provider, keyHash, model string, now time.Time) error {
	_, err := s.db.Exec(`
		INSERT INTO model_usage (provider, key_hash, model, day, exhausted, updated_at)
		VALUES (?, ?, ?, ?, 1, ?)
		ON CONFLICT(provider, key_hash, model, day) DO UPDATE SET
			exhausted = 1,
			updated_at = excluded.updated_at
	`, provider, keyHash, model, UTCDay(now), now.UTC())
	return err
}

// FreezeModel замораживает модель ключа: 1-я ошибка → 1 мин, 2-я → до конца UTC-дня.
// Возвращает длительность заморозки.
func (s *Store) FreezeModel(provider, keyHash, model string, now time.Time) (time.Duration, error) {
	day := UTCDay(now)
	var freezeCount int
	err := s.db.QueryRow(`SELECT freeze_count FROM model_usage WHERE provider=? AND key_hash=? AND model=? AND day=?`, provider, keyHash, model, day).Scan(&freezeCount)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	freezeCount++
	var until time.Time
	if freezeCount == 1 {
		until = now.Add(time.Minute)
	} else {
		until = now.Add(24 * time.Hour)
	}
	_, err = s.db.Exec(`
		INSERT INTO model_usage (provider, key_hash, model, day, freeze_count, frozen_until, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider, key_hash, model, day) DO UPDATE SET
			freeze_count = excluded.freeze_count,
			frozen_until = excluded.frozen_until,
			updated_at = excluded.updated_at
	`, provider, keyHash, model, day, freezeCount, until.UTC().Format(time.RFC3339), now.UTC())
	if err != nil {
		return 0, err
	}
	return until.Sub(now), nil
}

func (s *Store) GetModelUsageStats(provider string, now time.Time) ([]ModelUsageStats, error) {
	rows, err := s.db.Query(`
		SELECT model, COALESCE(SUM(requests), 0), COALESCE(SUM(tokens), 0)
		FROM model_usage
		WHERE provider = ? AND day = ?
		GROUP BY model
		ORDER BY requests DESC
	`, provider, UTCDay(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []ModelUsageStats
	for rows.Next() {
		var u ModelUsageStats
		if err := rows.Scan(&u.Model, &u.Requests, &u.Tokens); err != nil {
			return nil, err
		}
		res = append(res, u)
	}
	return res, rows.Err()
}

// GetModelUsageTrend возвращает агрегированную статистику по всем моделям провайдера
// за последние `days` суток, отсортированные по возрастанию дня.
func (s *Store) GetModelUsageTrend(provider string, days int) ([]ModelUsageTrend, error) {
	if days <= 0 {
		days = 14
	}
	now := time.Now().In(time.Local)
	since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, -days+1)
	rows, err := s.db.Query(`
		SELECT timestamp, status_code, prompt_tokens + completion_tokens, latency_ms
		FROM requests
		WHERE provider = ? AND timestamp >= ?
		ORDER BY timestamp ASC
	`, provider, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byDay := map[string]*ModelUsageTrend{}
	order := []string{}
	for rows.Next() {
		var ts time.Time
		var status int
		var tokens, latency int64
		if err := rows.Scan(&ts, &status, &tokens, &latency); err != nil {
			return nil, err
		}
		day := ts.In(time.Local).Format("2006-01-02")
		item := byDay[day]
		if item == nil {
			item = &ModelUsageTrend{Day: day}
			byDay[day] = item
			order = append(order, day)
		}
		item.Requests++
		item.Tokens += tokens
		item.LatencyAvg += latency
		if status >= 400 || status == 0 {
			item.Errors++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	res := make([]ModelUsageTrend, 0, len(order))
	for _, day := range order {
		item := *byDay[day]
		if item.Requests > 0 {
			item.LatencyAvg /= item.Requests
		}
		res = append(res, item)
	}
	return res, nil
}

func (s *Store) GetUsageBuckets(provider string, now time.Time, since time.Time, step time.Duration) ([]UsageBucket, error) {
	if step <= 0 {
		step = time.Hour
	}
	loc := time.Local
	now = localBucketStart(now, step, loc)
	since = localBucketStart(since, step, loc)
	if since.After(now) {
		since = now
	}

	buckets := map[time.Time]*UsageBucket{}
	order := []time.Time{}
	for t := since; !t.After(now); t = t.Add(step) {
		buckets[t] = &UsageBucket{Bucket: t.Format(time.RFC3339)}
		order = append(order, t)
	}

	rows, err := s.db.Query(`
		SELECT timestamp, status_code, prompt_tokens + completion_tokens, latency_ms
		FROM requests
		WHERE provider = ? AND timestamp >= ? AND timestamp < ?
		ORDER BY timestamp ASC
	`, provider, since, now.Add(step))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var ts time.Time
		var status int
		var tokens, latency int64
		if err := rows.Scan(&ts, &status, &tokens, &latency); err != nil {
			return nil, err
		}
		bucketTime := localBucketStart(ts, step, loc)
		bucket := buckets[bucketTime]
		if bucket == nil {
			continue
		}
		bucket.Requests++
		bucket.Tokens += tokens
		bucket.LatencyAvg += latency
		if status >= 400 || status == 0 {
			bucket.Errors++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	res := make([]UsageBucket, 0, len(order))
	for _, t := range order {
		bucket := *buckets[t]
		if bucket.Requests > 0 {
			bucket.LatencyAvg /= bucket.Requests
		}
		res = append(res, bucket)
	}
	return res, nil
}

func localBucketStart(t time.Time, step time.Duration, loc *time.Location) time.Time {
	t = t.In(loc)
	stepMinutes := int(step / time.Minute)
	if stepMinutes <= 0 {
		stepMinutes = 60
	}
	y, m, d := t.Date()
	minutes := t.Hour()*60 + t.Minute()
	minutes = minutes / stepMinutes * stepMinutes
	return time.Date(y, m, d, minutes/60, minutes%60, 0, 0, loc)
}

func (s *Store) AddKeys(keys []string, provider string) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO keys (key_hash, masked_key, status, cooldown_until, last_checked_at, last_used_at, raw_key, provider)
		VALUES (?, ?, 'unchecked', ?, ?, ?, ?, ?)
		ON CONFLICT(key_hash) DO UPDATE SET masked_key=excluded.masked_key, raw_key=excluded.raw_key, provider=excluded.provider;
	`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	zeroTime := time.Unix(0, 0)
	added := 0

	for _, k := range keys {
		h := HashKey(k)
		masked := MaskKey(k)
		res, err := stmt.Exec(h, masked, zeroTime, zeroTime, zeroTime, k, provider)
		if err != nil {
			return 0, err
		}
		rows, err := res.RowsAffected()
		if err == nil && rows > 0 {
			added++
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return added, nil
}

func (s *Store) DeleteKey(hash string) error {
	_, err := s.db.Exec("DELETE FROM keys WHERE key_hash = ?", hash)
	return err
}

func (s *Store) DeleteKeys(hashes []string, provider string) error {
	if len(hashes) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("DELETE FROM keys WHERE key_hash = ? AND provider = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, hash := range hashes {
		if _, err := stmt.Exec(hash, provider); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpdateKeysStatus(hashes []string, provider, status string) error {
	if len(hashes) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("UPDATE keys SET status = ? WHERE key_hash = ? AND provider = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, hash := range hashes {
		if _, err := stmt.Exec(status, hash, provider); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpdateKey(k *DBKey) error {
	_, err := s.db.Exec(`
		UPDATE keys SET
			status = ?,
			limit_remaining = ?,
			usage_today = ?,
			max_limit = ?,
			is_free_tier = ?,
			rate_limit_req = ?,
			rate_limit_interval = ?,
			cooldown_until = ?,
			last_checked_at = ?,
			last_used_at = ?
		WHERE key_hash = ?
	`, k.Status, k.LimitRemaining, k.UsageToday, k.MaxLimit,
		k.IsFreeTier, k.RateLimitReq, k.RateLimitInterval,
		k.CooldownUntil, k.LastCheckedAt, k.LastUsedAt, k.KeyHash)
	return err
}

func (s *Store) GetKeys(provider string) ([]*DBKey, error) {
	rows, err := s.db.Query(`
		SELECT key_hash, masked_key, status, limit_remaining, usage_today, max_limit, 
		       is_free_tier, rate_limit_req, rate_limit_interval, cooldown_until, 
		       last_checked_at, last_used_at, raw_key
		FROM keys
		WHERE provider = ?
	`, provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []*DBKey
	for rows.Next() {
		k := &DBKey{}
		var isFree int
		err := rows.Scan(
			&k.KeyHash, &k.MaskedKey, &k.Status, &k.LimitRemaining, &k.UsageToday, &k.MaxLimit,
			&isFree, &k.RateLimitReq, &k.RateLimitInterval, &k.CooldownUntil,
			&k.LastCheckedAt, &k.LastUsedAt, &k.RawKey,
		)
		if err != nil {
			return nil, err
		}
		k.IsFreeTier = isFree != 0
		res = append(res, k)
	}
	return res, nil
}

func (s *Store) LogRequest(r *DBRequest) error {
	isStreamInt := 0
	if r.IsStream {
		isStreamInt = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO requests (timestamp, key_hash, model, status_code, prompt_tokens, completion_tokens, latency_ms, error_msg, ttft_ms, is_stream, provider)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, r.Timestamp, r.KeyHash, r.Model, r.StatusCode, r.PromptTokens, r.CompletionTokens, r.LatencyMs, r.ErrorMsg, r.TTFTMs, isStreamInt, r.Provider)
	return err
}

func (s *Store) GetRequestLog(provider string, limit int) ([]RequestLogItem, error) {
	if limit < 0 || limit > 500 {
		limit = 100
	}
	query := `
		SELECT id, timestamp, provider, key_hash, model, status_code,
		       COALESCE(error_msg, ''), prompt_tokens + completion_tokens, latency_ms, ttft_ms, is_stream
		FROM requests
		WHERE provider = ?
		ORDER BY timestamp DESC, id DESC
`
	args := []any{provider}
	if limit > 0 {
		query += `		LIMIT ?
`
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := []RequestLogItem{}
	for rows.Next() {
		var item RequestLogItem
		var ts time.Time
		var isStream int
		if err := rows.Scan(&item.ID, &ts, &item.Provider, &item.KeyHash, &item.Model, &item.Status, &item.StatusText, &item.Tokens, &item.LatencyMs, &item.TTFTMs, &isStream); err != nil {
			return nil, err
		}
		item.Timestamp = ts.UTC().Format(time.RFC3339)
		item.IsStream = isStream != 0
		res = append(res, item)
	}
	return res, rows.Err()
}

func (s *Store) ClearRequestLog(provider string) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM requests WHERE provider = ?`, provider)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ponytail: raw logs are written forever. Purging logic can be added when DB size is a concern.
func (s *Store) LogRateLimit(rl *DBRateLimit) error {
	_, err := s.db.Exec(`
		INSERT INTO rate_limits_log (timestamp, key_hash, source, limit_total, limit_remaining, usage, rate_limit_req, rate_limit_interval, reset_raw)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, rl.Timestamp, rl.KeyHash, rl.Source, rl.LimitTotal, rl.LimitRemaining, rl.Usage, rl.RateLimitReq, rl.RateLimitInterval, rl.ResetRaw)
	return err
}

func (s *Store) CacheModels(models []DBModel) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec("DELETE FROM models_cache")
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO models_cache (id, name, rank, context_length, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, m := range models {
		_, err := stmt.Exec(m.ID, m.Name, m.Rank, m.ContextLength, m.UpdatedAt)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) GetCachedModels() ([]DBModel, error) {
	rows, err := s.db.Query(`SELECT id, name, rank, context_length, updated_at FROM models_cache ORDER BY rank ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []DBModel
	for rows.Next() {
		m := DBModel{}
		err := rows.Scan(&m.ID, &m.Name, &m.Rank, &m.ContextLength, &m.UpdatedAt)
		if err != nil {
			return nil, err
		}
		res = append(res, m)
	}
	return res, nil
}

func (s *Store) CacheFreeModels(models []DBModel) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec("DELETE FROM free_models_cache")
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO free_models_cache (id, name, context_length, max_output, type, features, modalities, input_price, output_price, description, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, m := range models {
		_, err := stmt.Exec(m.ID, m.Name, m.ContextLength, m.MaxOutput, m.Type, m.Features, m.Modalities, m.InputPrice, m.OutputPrice, m.Description, m.UpdatedAt)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) GetCachedFreeModels() ([]DBModel, error) {
	rows, err := s.db.Query(`SELECT id, name, context_length, max_output, type, features, modalities, input_price, output_price, description, updated_at FROM free_models_cache ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []DBModel
	for rows.Next() {
		m := DBModel{}
		err := rows.Scan(&m.ID, &m.Name, &m.ContextLength, &m.MaxOutput, &m.Type, &m.Features, &m.Modalities, &m.InputPrice, &m.OutputPrice, &m.Description, &m.UpdatedAt)
		if err != nil {
			return nil, err
		}
		res = append(res, m)
	}
	return res, nil
}

func (s *Store) CacheAihubmixFreeModels(models []DBModel) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec("DELETE FROM aihubmix_free_models_cache")
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`INSERT INTO aihubmix_free_models_cache (id, name, context_length, max_output, type, features, modalities, input_price, output_price, description, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, m := range models {
		if _, err := stmt.Exec(m.ID, m.Name, m.ContextLength, m.MaxOutput, m.Type, m.Features, m.Modalities, m.InputPrice, m.OutputPrice, m.Description, m.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetCachedAihubmixFreeModels() ([]DBModel, error) {
	rows, err := s.db.Query(`SELECT id, name, context_length, max_output, type, features, modalities, input_price, output_price, description, updated_at FROM aihubmix_free_models_cache ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []DBModel
	for rows.Next() {
		m := DBModel{}
		if err := rows.Scan(&m.ID, &m.Name, &m.ContextLength, &m.MaxOutput, &m.Type, &m.Features, &m.Modalities, &m.InputPrice, &m.OutputPrice, &m.Description, &m.UpdatedAt); err != nil {
			return nil, err
		}
		res = append(res, m)
	}
	return res, nil
}

// Stats helper structures
type GeneralStats struct {
	TotalRequests int64 `json:"total_requests"`
	TodayRequests int64 `json:"today_requests"`
	ActiveKeys    int   `json:"active_keys"`
	BlockedKeys   int   `json:"blocked_keys"`
	InvalidKeys   int   `json:"invalid_keys"`
	UncheckedKeys int   `json:"unchecked_keys"`
	TotalKeys     int   `json:"total_keys"`
}

type ModelStats struct {
	Model         string `json:"model"`
	TodayRequests int64  `json:"today_requests"`
	TotalRequests int64  `json:"total_requests"`
	AvgLatencyMs  int64  `json:"avg_latency_ms"`
	TotalTokens   int64  `json:"total_tokens"`
}

type KeyUsageStats struct {
	MaskedKey     string    `json:"masked_key"`
	KeyHash       string    `json:"key_hash"`
	Status        string    `json:"status"`
	TodayUsage    int64     `json:"today_usage"`
	Limit         int64     `json:"limit"`
	TotalRequests int64     `json:"total_requests"`
	ErrorRequests int64     `json:"error_requests"`
	CooldownUntil time.Time `json:"cooldown_until"`
	LastUsedAt    time.Time `json:"last_used_at"`
}

func (s *Store) GetGeneralStats(provider string) (*GeneralStats, error) {
	stats := &GeneralStats{}

	// Counts
	err := s.db.QueryRow(`
		SELECT 
			COUNT(*),
			COALESCE(SUM(CASE WHEN status='active' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status='rate_limited' OR status='day_exhausted' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status='invalid' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status='unchecked' THEN 1 ELSE 0 END), 0)
		FROM keys
		WHERE provider = ?
	`, provider).Scan(&stats.TotalKeys, &stats.ActiveKeys, &stats.BlockedKeys, &stats.InvalidKeys, &stats.UncheckedKeys)
	if err != nil {
		return nil, err
	}

	// Request stats
	err = s.db.QueryRow(`SELECT COUNT(*) FROM requests WHERE provider = ?`, provider).Scan(&stats.TotalRequests)
	if err != nil {
		return nil, err
	}

	now := time.Now().In(time.Local)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	err = s.db.QueryRow(`SELECT COUNT(*) FROM requests WHERE timestamp >= ? AND provider = ?`, todayStart, provider).Scan(&stats.TodayRequests)
	if err != nil {
		return nil, err
	}

	return stats, nil
}

func (s *Store) GetModelStats(provider string) ([]ModelStats, error) {
	now := time.Now().In(time.Local)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	rows, err := s.db.Query(`
		SELECT 
			model, 
			COALESCE(SUM(CASE WHEN timestamp >= ? THEN 1 ELSE 0 END), 0),
			COUNT(*), 
			CAST(AVG(latency_ms) AS INTEGER), 
			SUM(prompt_tokens + completion_tokens) 
		FROM requests
		WHERE provider = ?
		GROUP BY model 
		ORDER BY 2 DESC, COUNT(*) DESC
	`, todayStart, provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := []ModelStats{}
	for rows.Next() {
		m := ModelStats{}
		var totalTokens sql.NullInt64
		err := rows.Scan(&m.Model, &m.TodayRequests, &m.TotalRequests, &m.AvgLatencyMs, &totalTokens)
		if err != nil {
			return nil, err
		}
		m.TotalTokens = totalTokens.Int64
		res = append(res, m)
	}
	return res, nil
}

func (s *Store) GetKeyUsageStats(provider string) ([]KeyUsageStats, error) {
	rows, err := s.db.Query(`
		SELECT 
			k.masked_key, 
			k.key_hash, 
			k.status, 
			k.usage_today, 
			k.max_limit, 
			k.cooldown_until,
			k.last_used_at,
			COUNT(r.id) as total_reqs,
			SUM(CASE WHEN r.status_code >= 400 THEN 1 ELSE 0 END) as err_reqs
		FROM keys k
		LEFT JOIN requests r ON k.key_hash = r.key_hash AND r.provider = k.provider
		WHERE k.provider = ?
		GROUP BY k.key_hash
		ORDER BY k.usage_today DESC, total_reqs DESC
	`, provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := []KeyUsageStats{}
	for rows.Next() {
		k := KeyUsageStats{}
		var totalReqs, errReqs sql.NullInt64
		err := rows.Scan(
			&k.MaskedKey, &k.KeyHash, &k.Status, &k.TodayUsage, &k.Limit, &k.CooldownUntil, &k.LastUsedAt,
			&totalReqs, &errReqs,
		)
		if err != nil {
			return nil, err
		}
		k.TotalRequests = totalReqs.Int64
		k.ErrorRequests = errReqs.Int64
		res = append(res, k)
	}
	return res, nil
}

func (s *Store) GetRateLimitsLog() ([]*DBRateLimit, error) {
	rows, err := s.db.Query(`
		SELECT id, timestamp, key_hash, source, limit_total, limit_remaining, usage, rate_limit_req, rate_limit_interval, reset_raw
		FROM rate_limits_log
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []*DBRateLimit
	for rows.Next() {
		rl := &DBRateLimit{}
		err := rows.Scan(
			&rl.ID, &rl.Timestamp, &rl.KeyHash, &rl.Source, &rl.LimitTotal, &rl.LimitRemaining,
			&rl.Usage, &rl.RateLimitReq, &rl.RateLimitInterval, &rl.ResetRaw,
		)
		if err != nil {
			return nil, err
		}
		res = append(res, rl)
	}
	return res, nil
}
