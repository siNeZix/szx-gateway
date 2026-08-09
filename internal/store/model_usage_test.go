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

func TestReserveModelUsageEnforcesPerModelDailyLimit(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now()
	for i := 0; i < 25; i++ {
		reserved, err := s.ReserveModelUsage("google", "k", "gemini-2.5-pro", now, 25)
		if err != nil || !reserved {
			t.Fatalf("reservation %d: reserved=%v err=%v", i, reserved, err)
		}
	}
	reserved, err := s.ReserveModelUsage("google", "k", "gemini-2.5-pro", now, 25)
	if err != nil || reserved {
		t.Fatalf("26th pro request must be denied: reserved=%v err=%v", reserved, err)
	}
	reserved, err = s.ReserveModelUsage("google", "k", "gemini-2.5-flash", now, 500)
	if err != nil || !reserved {
		t.Fatalf("another model must retain its own quota: reserved=%v err=%v", reserved, err)
	}
}

func TestModelStatsUsesLocalDay(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	n := time.Now().In(time.Local)
	now := time.Date(n.Year(), n.Month(), n.Day(), 12, 0, 0, 0, time.Local)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
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

func TestGeneralStatsUsesLocalDay(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now().In(time.Local)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
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

func TestRequestTrendAndLogUseRequests(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Now().In(time.Local).Add(-time.Minute)
	for _, r := range []*DBRequest{
		{Timestamp: now.Add(-time.Hour), KeyHash: "k1", Model: "m1", StatusCode: 200, PromptTokens: 2, CompletionTokens: 3, LatencyMs: 100, TTFTMs: 40, Provider: "openrouter"},
		{Timestamp: now, KeyHash: "k2", Model: "m2", StatusCode: 429, ErrorMsg: "rate limited", LatencyMs: 300, TTFTMs: 300, IsStream: true, Provider: "openrouter"},
	} {
		if err := s.LogRequest(r); err != nil {
			t.Fatal(err)
		}
	}

	trend, err := s.GetModelUsageTrend("openrouter", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(trend) != 1 || trend[0].Requests != 2 || trend[0].Tokens != 5 || trend[0].LatencyAvg != 200 || trend[0].Errors != 1 {
		t.Fatalf("trend = %+v", trend)
	}

	log, err := s.GetRequestLog("openrouter", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 1 || log[0].Model != "m2" || log[0].Status != 429 || log[0].StatusText != "rate limited" || !log[0].IsStream {
		t.Fatalf("log = %+v", log)
	}

	all, err := s.GetRequestLog("openrouter", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all logs = %d", len(all))
	}

	deleted, err := s.ClearRequestLog("openrouter")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d", deleted)
	}
}

func TestUsageBucketsUseLocalTime(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	oldLocal := time.Local
	t.Cleanup(func() { time.Local = oldLocal })
	time.Local = time.FixedZone("TEST", 3*3600)

	now := time.Date(2026, 1, 2, 15, 37, 0, 0, time.Local)
	for _, r := range []*DBRequest{
		{Timestamp: time.Date(2026, 1, 2, 15, 5, 0, 0, time.Local), KeyHash: "k", Model: "m", StatusCode: 200, PromptTokens: 2, CompletionTokens: 3, LatencyMs: 100, Provider: "openrouter"},
		{Timestamp: time.Date(2026, 1, 2, 15, 14, 0, 0, time.Local), KeyHash: "k", Model: "m", StatusCode: 500, LatencyMs: 300, Provider: "openrouter"},
	} {
		if err := s.LogRequest(r); err != nil {
			t.Fatal(err)
		}
	}

	hourly, err := s.GetUsageBuckets("openrouter", now, time.Date(2026, 1, 2, 0, 0, 0, 0, time.Local), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(hourly) != 16 || hourly[15].Bucket != "2026-01-02T15:00:00+03:00" || hourly[15].Requests != 2 || hourly[15].Tokens != 5 || hourly[15].LatencyAvg != 200 || hourly[15].Errors != 1 {
		t.Fatalf("hourly = %+v", hourly)
	}

	fiveMinutes, err := s.GetUsageBuckets("openrouter", now, now.Add(-30*time.Minute), 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(fiveMinutes) != 7 || fiveMinutes[1].Bucket != "2026-01-02T15:10:00+03:00" || fiveMinutes[1].Requests != 1 || fiveMinutes[1].Errors != 1 {
		t.Fatalf("5m = %+v", fiveMinutes)
	}
}
