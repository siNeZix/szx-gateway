package store

import (
	"os"
	"testing"
	"time"
)

func TestPostgresStore(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}

	s, err := Open("postgres", "", dsn, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	provider := "postgres-test"
	if _, err := s.db.Exec(`DELETE FROM "keys" WHERE provider = ?`, provider); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddKeys([]string{"postgres-test-key"}, provider); err != nil {
		t.Fatal(err)
	}
	keys, err := s.GetKeys(provider)
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys = %d, err = %v", len(keys), err)
	}
	keys[0].IsFreeTier = false
	if err := s.UpdateKey(keys[0], provider); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		reserved, err := s.ReserveModelUsage(provider, keys[0].KeyHash, "model", now, 2)
		if err != nil || !reserved {
			t.Fatalf("reservation %d: reserved=%v err=%v", i, reserved, err)
		}
	}
	reserved, err := s.ReserveModelUsage(provider, keys[0].KeyHash, "model", now, 2)
	if err != nil || reserved {
		t.Fatalf("quota overflow: reserved=%v err=%v", reserved, err)
	}

	if _, err := s.FreezeModel(provider, keys[0].KeyHash, "model", now); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveModelCheckConfig(ModelCheckConfig{Provider: provider, Model: "model", Enabled: true, Position: 1}); err != nil {
		t.Fatal(err)
	}
	configs, err := s.GetEnabledModelChecks()
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) == 0 {
		t.Fatal("model check config was not saved")
	}
	if err := s.AddModelCheckResult(provider, "model", true, ""); err != nil {
		t.Fatal(err)
	}
	result, found, err := s.GetLatestModelCheckResult(provider, "model")
	if err != nil || !found || !result.Success {
		t.Fatalf("model check result = %+v, found=%v, err=%v", result, found, err)
	}
}
