package keys

import (
	"path/filepath"
	"testing"
	"time"

	"szx-gateway/internal/store"
)

// ResetExpiredDailyUsage сбрасывает только ключи, чей LastUsedAt в прошлом UTC-дне,
// и пишет результат в БД. Ключи сегодняшнего дня не трогаются.
func TestResetExpiredDailyUsage(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_reset.db")

	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer s.Close()

	pool, err := NewKeyPool(s, "openrouter")
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	if _, err := pool.AddKeys([]string{"sk-or-v1-a", "sk-or-v1-b", "sk-or-v1-c"}); err != nil {
		t.Fatalf("AddKeys failed: %v", err)
	}

	yesterday := time.Now().Add(-48 * time.Hour)
	today := time.Now()
	yesterdayDay := yesterday.UTC().Format("2006-01-02")
	todayDay := today.UTC().Format("2006-01-02")

	// Готовим три ключа: один вчерашний exhausted, один вчерашний active, один сегодняшний.
	for i, ks := range pool.keys {
		switch i {
		case 0:
			ks.LastUsedAt = yesterday
			ks.UsageDay = yesterdayDay
			ks.UsageToday = 10
			ks.MaxLimit = 10
			ks.Status = "day_exhausted"
		case 1:
			ks.LastUsedAt = yesterday
			ks.UsageDay = yesterdayDay
			ks.UsageToday = 5
			ks.Status = "active"
		case 2:
			ks.LastUsedAt = today
			ks.UsageDay = todayDay
			ks.UsageToday = 3
			ks.Status = "active"
		}
	}

	n := pool.ResetExpiredDailyUsage()
	if n != 2 {
		t.Fatalf("ResetExpiredDailyUsage reset %d keys, want 2", n)
	}

	// In-memory: первые два сброшены, третий нет.
	if pool.keys[0].UsageToday != 0 || pool.keys[0].Status != "active" {
		t.Errorf("k0: usage=%d status=%s, want 0/active", pool.keys[0].UsageToday, pool.keys[0].Status)
	}
	if pool.keys[1].UsageToday != 0 || pool.keys[1].Status != "active" {
		t.Errorf("k1: usage=%d status=%s, want 0/active", pool.keys[1].UsageToday, pool.keys[1].Status)
	}
	if pool.keys[2].UsageToday != 3 || pool.keys[2].Status != "active" {
		t.Errorf("k2: usage=%d status=%s, want 3/active", pool.keys[2].UsageToday, pool.keys[2].Status)
	}

	// БД: SyncKeyToDB должен был записать сброшенные значения.
	dbKeys, err := s.GetKeys("openrouter")
	if err != nil {
		t.Fatalf("GetKeys failed: %v", err)
	}
	for _, dbK := range dbKeys {
		if dbK.UsageToday != 0 {
			t.Errorf("DB key %s still has usage_today=%d, want 0 after reset", dbK.MaskedKey, dbK.UsageToday)
		}
	}
}
