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
