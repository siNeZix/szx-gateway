package store

import (
	"database/sql"
	"fmt"
	"time"
)

func (s *Store) migrateMySQL() error {
	q := func(identifier string) string { return "`" + identifier + "`" }
	queries := []string{
		`CREATE TABLE IF NOT EXISTS ` + q("keys") + ` (
			` + q("key_hash") + ` VARCHAR(64) PRIMARY KEY, ` + q("masked_key") + ` TEXT NOT NULL, ` + q("status") + ` VARCHAR(32) NOT NULL,
			` + q("limit_remaining") + ` BIGINT NOT NULL DEFAULT 0, ` + q("usage_today") + ` BIGINT NOT NULL DEFAULT 0, ` + q("usage_day") + ` VARCHAR(10) NOT NULL DEFAULT '',
			` + q("max_limit") + ` BIGINT NOT NULL DEFAULT 0, ` + q("is_free_tier") + ` TINYINT NOT NULL DEFAULT 1, ` + q("rate_limit_req") + ` INT NOT NULL DEFAULT 20,
			` + q("rate_limit_interval") + ` VARCHAR(32) NOT NULL DEFAULT '1m', ` + q("cooldown_until") + ` DATETIME(6) NOT NULL,
			` + q("last_checked_at") + ` DATETIME(6) NOT NULL, ` + q("last_used_at") + ` DATETIME(6) NOT NULL, ` + q("raw_key") + ` TEXT NOT NULL,
			` + q("provider") + ` VARCHAR(32) NOT NULL DEFAULT 'openrouter', INDEX ` + q("idx_keys_provider") + ` (` + q("provider") + `)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS requests (
			id BIGINT AUTO_INCREMENT PRIMARY KEY, timestamp DATETIME(6) NOT NULL, key_hash VARCHAR(64) NOT NULL,
			model VARCHAR(512) NOT NULL, status_code INT NOT NULL, prompt_tokens BIGINT NOT NULL DEFAULT 0,
			completion_tokens BIGINT NOT NULL DEFAULT 0, latency_ms BIGINT NOT NULL DEFAULT 0, error_msg TEXT,
			ttft_ms BIGINT NOT NULL DEFAULT 0, is_stream TINYINT NOT NULL DEFAULT 0, provider VARCHAR(32) NOT NULL DEFAULT 'openrouter',
			INDEX idx_requests_timestamp (timestamp), INDEX idx_requests_key_hash (key_hash), INDEX idx_requests_provider (provider)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS rate_limits_log (
			id BIGINT AUTO_INCREMENT PRIMARY KEY, timestamp DATETIME(6) NOT NULL, key_hash VARCHAR(64) NOT NULL,
			source VARCHAR(32) NOT NULL, limit_total BIGINT, limit_remaining BIGINT, ` + "`usage`" + ` BIGINT,
			rate_limit_req BIGINT, rate_limit_interval VARCHAR(32), reset_raw TEXT,
			INDEX idx_rl_timestamp (timestamp), INDEX idx_rl_key_hash (key_hash)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS models_cache (id VARCHAR(512) PRIMARY KEY, name TEXT NOT NULL, ` + "`rank`" + ` INT NOT NULL, context_length BIGINT NOT NULL, updated_at DATETIME(6) NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS free_models_cache (id VARCHAR(512) PRIMARY KEY, name TEXT NOT NULL, context_length BIGINT NOT NULL, max_output BIGINT NOT NULL DEFAULT 0, type VARCHAR(128) NOT NULL DEFAULT '', features TEXT NOT NULL, modalities TEXT NOT NULL, input_price DOUBLE NOT NULL DEFAULT 0, output_price DOUBLE NOT NULL DEFAULT 0, description TEXT NOT NULL, updated_at DATETIME(6) NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS aihubmix_free_models_cache (id VARCHAR(512) PRIMARY KEY, name TEXT NOT NULL, context_length BIGINT NOT NULL DEFAULT 0, max_output BIGINT NOT NULL DEFAULT 0, type VARCHAR(128) NOT NULL DEFAULT '', features TEXT NOT NULL, modalities TEXT NOT NULL, input_price DOUBLE NOT NULL DEFAULT 0, output_price DOUBLE NOT NULL DEFAULT 0, description TEXT NOT NULL, updated_at DATETIME(6) NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS google_free_models_cache (id VARCHAR(512) PRIMARY KEY, name TEXT NOT NULL, context_length BIGINT NOT NULL DEFAULT 0, max_output BIGINT NOT NULL DEFAULT 0, type VARCHAR(128) NOT NULL DEFAULT '', features TEXT NOT NULL, modalities TEXT NOT NULL, input_price DOUBLE NOT NULL DEFAULT 0, output_price DOUBLE NOT NULL DEFAULT 0, description TEXT NOT NULL, updated_at DATETIME(6) NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS model_usage (
			provider VARCHAR(32) NOT NULL, key_hash VARCHAR(64) NOT NULL, model VARCHAR(512) NOT NULL, day VARCHAR(10) NOT NULL,
			requests BIGINT NOT NULL DEFAULT 0, tokens BIGINT NOT NULL DEFAULT 0, exhausted TINYINT NOT NULL DEFAULT 0,
			latency_sum_ms BIGINT NOT NULL DEFAULT 0, errors BIGINT NOT NULL DEFAULT 0, freeze_count INT NOT NULL DEFAULT 0,
			frozen_until VARCHAR(64) NOT NULL DEFAULT '', updated_at DATETIME(6) NOT NULL,
			PRIMARY KEY (provider, key_hash, model, day), INDEX idx_model_usage_day (provider, day, model)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS proxies (
			id BIGINT AUTO_INCREMENT PRIMARY KEY, raw TEXT NOT NULL, scheme VARCHAR(32) NOT NULL, host VARCHAR(255) NOT NULL,
			port VARCHAR(16) NOT NULL, username VARCHAR(128) NOT NULL DEFAULT '', password VARCHAR(128) NOT NULL DEFAULT '',
			status VARCHAR(32) NOT NULL DEFAULT 'unchecked', last_checked_at DATETIME(6) NOT NULL, last_error TEXT NOT NULL,
			created_at DATETIME(6) NOT NULL, UNIQUE KEY uq_proxies (scheme, host, port, username, password)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS proxy_settings (provider VARCHAR(32) PRIMARY KEY, use_for_checker TINYINT NOT NULL DEFAULT 0, use_for_requests TINYINT NOT NULL DEFAULT 0, mode VARCHAR(32) NOT NULL DEFAULT 'after_429') ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS proxy_logs (id BIGINT AUTO_INCREMENT PRIMARY KEY, timestamp DATETIME(6) NOT NULL, proxy_id BIGINT NOT NULL, provider VARCHAR(32) NOT NULL, use_case VARCHAR(64) NOT NULL, success TINYINT NOT NULL, request_bytes BIGINT NOT NULL DEFAULT 0, response_bytes BIGINT NOT NULL DEFAULT 0, latency_ms BIGINT NOT NULL DEFAULT 0, error_msg TEXT NOT NULL, INDEX idx_proxy_logs_timestamp (timestamp)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS model_check_configs (provider VARCHAR(32) NOT NULL, model VARCHAR(512) NOT NULL, enabled TINYINT NOT NULL DEFAULT 0, position INT NOT NULL DEFAULT 0, PRIMARY KEY (provider, model)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS model_check_results (id BIGINT AUTO_INCREMENT PRIMARY KEY, provider VARCHAR(32) NOT NULL, model VARCHAR(512) NOT NULL, timestamp DATETIME(6) NOT NULL, success TINYINT NOT NULL, error TEXT NOT NULL, INDEX idx_model_check_results_lookup (provider, model, timestamp)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
	for _, query := range queries {
		if _, err := s.db.Exec(query); err != nil {
			return fmt.Errorf("mysql migration query failed: %w", err)
		}
	}
	return nil
}

func (s *Store) reserveModelUsageMySQL(provider, keyHash, model, day string, now time.Time, limit int64) (bool, error) {
	res, err := s.db.Exec(`
		INSERT INTO model_usage (provider, key_hash, model, day, requests, updated_at)
		VALUES (?, ?, ?, ?, 1, ?)
		ON DUPLICATE KEY UPDATE
			requests = IF(requests < ? AND exhausted = 0, requests + 1, requests),
			updated_at = IF(requests <= ? AND exhausted = 0, VALUES(updated_at), updated_at)
	`, provider, keyHash, model, day, now.UTC(), limit, limit)
	if err != nil {
		return false, err
	}
	changed, err := res.RowsAffected()
	return changed > 0, err
}

func (s *Store) freezeModelMySQL(provider, keyHash, model string, now time.Time) (time.Duration, error) {
	day := UTCDay(now)
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO model_usage (provider, key_hash, model, day, updated_at) VALUES (?, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE updated_at = updated_at`, provider, keyHash, model, day, now.UTC()); err != nil {
		return 0, err
	}
	var freezeCount int
	if err := tx.QueryRow(`SELECT freeze_count FROM model_usage WHERE provider = ? AND key_hash = ? AND model = ? AND day = ? FOR UPDATE`, provider, keyHash, model, day).Scan(&freezeCount); err != nil {
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("model usage row missing after insert")
		}
		return 0, err
	}
	freezeCount++
	until := now.Add(time.Minute)
	if freezeCount > 1 {
		until = now.Add(24 * time.Hour)
	}
	if _, err := tx.Exec(`UPDATE model_usage SET freeze_count = ?, frozen_until = ?, updated_at = ? WHERE provider = ? AND key_hash = ? AND model = ? AND day = ?`, freezeCount, until.UTC().Format(time.RFC3339), now.UTC(), provider, keyHash, model, day); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return until.Sub(now), nil
}
