package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestModelUsageUsesUTCDay(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	late := time.Date(2026, 1, 2, 23, 30, 0, 0, time.FixedZone("MSK", 3*3600))
	if err := s.AddModelUsage("aihubmix", "k", "m", late, 1, 7, 500, 0); err != nil {
		t.Fatal(err)
	}

	u, err := s.GetModelUsage("aihubmix", "k", "m", time.Date(2026, 1, 2, 20, 31, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if u.Requests != 1 || u.Tokens != 7 || u.Day != "2026-01-02" {
		t.Fatalf("usage = %+v", u)
	}
}

func TestModelExhaustedIsPerModel(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now()
	if err := s.MarkModelExhausted("aihubmix", "k", "m1", now); err != nil {
		t.Fatal(err)
	}
	u1, _ := s.GetModelUsage("aihubmix", "k", "m1", now)
	u2, _ := s.GetModelUsage("aihubmix", "k", "m2", now)
	if !u1.Exhausted {
		t.Fatal("m1 must be exhausted")
	}
	if u2.Exhausted {
		t.Fatal("m2 must stay usable")
	}
}

func TestModelStatsUsesUTCDay(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	for _, r := range []*DBRequest{
		{Timestamp: todayStart.Add(time.Hour), KeyHash: "k", Model: "m", StatusCode: 200, LatencyMs: 100, Provider: "aihubmix"},
		{Timestamp: todayStart.Add(-time.Hour), KeyHash: "k", Model: "m", StatusCode: 200, LatencyMs: 300, Provider: "aihubmix"},
	} {
		if err := s.LogRequest(r); err != nil {
			t.Fatal(err)
		}
	}

	stats, err := s.GetModelStats("aihubmix")
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("stats len = %d", len(stats))
	}
	if stats[0].TodayRequests != 1 || stats[0].TotalRequests != 2 || stats[0].AvgLatencyMs != 200 {
		t.Fatalf("stats = %+v", stats[0])
	}
}

func TestGeneralStatsUsesUTCDay(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	for _, r := range []*DBRequest{
		{Timestamp: todayStart.Add(time.Hour), KeyHash: "k", Model: "m", StatusCode: 200, Provider: "openrouter"},
		{Timestamp: todayStart.Add(-time.Hour), KeyHash: "k", Model: "m", StatusCode: 200, Provider: "openrouter"},
	} {
		if err := s.LogRequest(r); err != nil {
			t.Fatal(err)
		}
	}

	stats, err := s.GetGeneralStats("openrouter")
	if err != nil {
		t.Fatal(err)
	}
	if stats.TodayRequests != 1 || stats.TotalRequests != 2 {
		t.Fatalf("stats = %+v", stats)
	}
}
